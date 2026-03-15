package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
)

// RPCServer exposes skyfs operations over a Unix domain socket using JSON-RPC 2.0.
type RPCServer struct {
	store    *Store
	sockPath string
	listener net.Listener
	logger   *slog.Logger

	mu      sync.Mutex
	clients map[net.Conn]bool
	events  chan RPCEvent
}

// RPCEvent is a server-push event sent to all connected clients.
type RPCEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// RPCRequest is a JSON-RPC 2.0 request.
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

// RPCResponse is a JSON-RPC 2.0 response.
type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError is a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewRPCServer creates an RPC server for the given store.
func NewRPCServer(store *Store, sockPath string, logger *slog.Logger) *RPCServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &RPCServer{
		store:    store,
		sockPath: sockPath,
		logger:   logger,
		clients:  make(map[net.Conn]bool),
		events:   make(chan RPCEvent, 100),
	}
}

// Serve starts listening and blocks until the context is cancelled.
func (s *RPCServer) Serve(ctx context.Context) error {
	os.Remove(s.sockPath) // clean up stale socket

	var err error
	s.listener, err = net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.sockPath, err)
	}
	defer s.listener.Close()
	defer os.Remove(s.sockPath)

	// Set socket permissions
	os.Chmod(s.sockPath, 0600)

	s.logger.Info("RPC server started", "socket", s.sockPath)

	// Broadcast events to clients
	go s.broadcastLoop()

	// Accept connections
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				s.logger.Warn("accept error", "error", err)
				continue
			}
		}

		s.mu.Lock()
		s.clients[conn] = true
		s.mu.Unlock()

		go s.handleConn(ctx, conn)
	}
}

// Emit sends an event to all connected clients.
func (s *RPCServer) Emit(event string, data interface{}) {
	select {
	case s.events <- RPCEvent{Event: event, Data: data}:
	default:
		// Channel full, drop event
	}
}

func (s *RPCServer) broadcastLoop() {
	for range s.events {
		// Events are logged but not pushed to RPC connections.
		// RPC connections use request-response only.
		// A separate event subscription endpoint can be added later
		// (e.g., a second socket or SSE over HTTP).
		s.logger.Debug("event emitted")
	}
}

func (s *RPCServer) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req RPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				s.logger.Debug("decode error", "error", err)
			}
			return
		}

		resp := s.dispatch(ctx, &req)
		if err := encoder.Encode(resp); err != nil {
			s.logger.Debug("encode error", "error", err)
			return
		}
	}
}

func (s *RPCServer) dispatch(ctx context.Context, req *RPCRequest) *RPCResponse {
	resp := &RPCResponse{JSONRPC: "2.0", ID: req.ID}

	var result interface{}
	var err error

	switch req.Method {
	case "skyfs.list":
		result, err = s.rpcList(ctx, req.Params)
	case "skyfs.info":
		result, err = s.rpcInfo(ctx)
	case "skyfs.put":
		result, err = s.rpcPut(ctx, req.Params)
	case "skyfs.get":
		result, err = s.rpcGet(ctx, req.Params)
	case "skyfs.remove":
		result, err = s.rpcRemove(ctx, req.Params)
	case "skyfs.status":
		result, err = s.rpcStatus(ctx)
	case "skyfs.versions":
		result, err = s.rpcVersions(ctx, req.Params)
	case "skyfs.compact":
		result, err = s.rpcCompact(ctx, req.Params)
	case "skyfs.gc":
		result, err = s.rpcGC(ctx, req.Params)
	default:
		resp.Error = &RPCError{Code: -32601, Message: "method not found: " + req.Method}
		return resp
	}

	if err != nil {
		resp.Error = &RPCError{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	return resp
}

// RPC method implementations

type listParams struct {
	Prefix string `json:"prefix"`
}

type listResult struct {
	Files []fileInfo `json:"files"`
}

type fileInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	Checksum  string `json:"checksum"`
	Namespace string `json:"namespace"`
	Chunks    int    `json:"chunks"`
}

func (s *RPCServer) rpcList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p listParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	entries, err := s.store.List(ctx, p.Prefix)
	if err != nil {
		return nil, err
	}

	files := make([]fileInfo, len(entries))
	for i, e := range entries {
		files[i] = fileInfo{
			Path:      e.Path,
			Size:      e.Size,
			Modified:  e.Modified.Format("2006-01-02T15:04:05Z"),
			Checksum:  e.Checksum,
			Namespace: e.Namespace,
			Chunks:    len(e.Chunks),
		}
	}

	return listResult{Files: files}, nil
}

func (s *RPCServer) rpcInfo(ctx context.Context) (interface{}, error) {
	return s.store.Info(ctx)
}

type putParams struct {
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

type putResult struct {
	Size   int64 `json:"size"`
	Chunks int   `json:"chunks"`
}

func (s *RPCServer) rpcPut(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p putParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	f, err := os.Open(p.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", p.LocalPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", p.LocalPath, err)
	}

	if err := s.store.Put(ctx, p.Path, f); err != nil {
		return nil, err
	}

	s.Emit("file.changed", map[string]string{"path": p.Path, "type": "put"})
	return putResult{Size: info.Size()}, nil
}

type getParams struct {
	Path    string `json:"path"`
	OutPath string `json:"out_path"`
}

type getResult struct {
	Size int64 `json:"size"`
}

func (s *RPCServer) rpcGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	f, err := os.Create(p.OutPath)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", p.OutPath, err)
	}

	if err := s.store.Get(ctx, p.Path, f); err != nil {
		f.Close()
		os.Remove(p.OutPath)
		return nil, err
	}

	stat, _ := f.Stat()
	f.Close()

	return getResult{Size: stat.Size()}, nil
}

type removeParams struct {
	Path string `json:"path"`
}

func (s *RPCServer) rpcRemove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p removeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if err := s.store.Remove(ctx, p.Path); err != nil {
		return nil, err
	}

	s.Emit("file.changed", map[string]string{"path": p.Path, "type": "delete"})
	return map[string]string{"status": "ok"}, nil
}

type statusResult struct {
	Syncing    bool   `json:"syncing"`
	LastSync   string `json:"last_sync,omitempty"`
	PendingOps int    `json:"pending_ops"`
}

func (s *RPCServer) rpcStatus(_ context.Context) (interface{}, error) {
	return statusResult{Syncing: false, PendingOps: 0}, nil
}

type versionsParams struct {
	Path string `json:"path"`
}

func (s *RPCServer) rpcVersions(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p versionsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	versions, err := ListVersions(ctx, s.store, p.Path)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{"versions": versions}, nil
}

type compactParams struct {
	Keep int `json:"keep"`
}

func (s *RPCServer) rpcCompact(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p compactParams
	p.Keep = 3
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	result, err := Compact(ctx, s.store.backend, s.store.identity, p.Keep)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type gcParams struct {
	DryRun bool `json:"dry_run"`
}

func (s *RPCServer) rpcGC(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p gcParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	result, err := GC(ctx, s.store.backend, s.store.identity, p.DryRun)
	if err != nil {
		return nil, err
	}
	return result, nil
}
