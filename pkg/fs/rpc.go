package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// RPCServer exposes skyfs operations over a Unix domain socket using JSON-RPC 2.0.
type RPCServer struct {
	store    *Store
	sockPath string
	version  string
	listener net.Listener
	logger   *slog.Logger
	logBuf   *LogBuffer

	syncMu     sync.Mutex
	syncCancel context.CancelFunc
	syncDir    string
	syncing    bool

	driveManager *DriveManager

	activityMu   sync.Mutex
	lastActivity time.Time
	startTime    time.Time

	mu               sync.Mutex
	clients          map[net.Conn]bool
	subscribers      map[net.Conn]*json.Encoder // push event connections
	events           chan RPCEvent
	completedInvites map[string]bool // cached: invites fully approved
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
func NewRPCServer(store *Store, sockPath string, driveCfgPath string, version string, logger *slog.Logger) *RPCServer {
	logBuf := NewLogBuffer(1000)
	if logger == nil {
		logger = NewDaemonLogger(logBuf)
	} else {
		logger = slog.New(NewLogBufferHandler(logBuf, logger.Handler()))
	}
	srv := &RPCServer{
		store:            store,
		sockPath:         sockPath,
		version:          version,
		logger:           logger,
		logBuf:           logBuf,
		startTime:        time.Now(),
		clients:          make(map[net.Conn]bool),
		subscribers:      make(map[net.Conn]*json.Encoder),
		events:           make(chan RPCEvent, 100),
		completedInvites: make(map[string]bool),
		driveManager:     NewDriveManager(store, driveCfgPath),
	}
	srv.driveManager.Logger = logger
	// Wire logger to S3 backend for request/response logging
	type logSetter interface{ SetLogger(*slog.Logger) }
	if ls, ok := store.backend.(logSetter); ok {
		ls.SetLogger(logger)
	}
	srv.driveManager.OnActivity = srv.MarkActivity
	srv.driveManager.OnStateChanged = func(event string, data map[string]any) {
		srv.Emit(event, data)
	}
	return srv
}

// Serve starts listening and blocks until the context is cancelled.
func (s *RPCServer) Serve(ctx context.Context) error {
	os.Remove(s.sockPath) // clean up stale socket
	os.MkdirAll(filepath.Dir(s.sockPath), 0755)

	var err error
	s.listener, err = net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.sockPath, err)
	}
	defer s.listener.Close()
	defer os.Remove(s.sockPath)

	// Set socket permissions
	os.Chmod(s.sockPath, 0600)

	s.logger.Info("RPC server started", "socket", s.sockPath, "version", s.version)

	// Broadcast events to clients
	go s.broadcastLoop()

	// Auto-start all enabled drives
	s.driveManager.StartAll(s.logger)

	// Auto-approve pending join requests every 20 seconds
	go s.autoApproveLoop(ctx)

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
	for event := range s.events {
		msg := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "event",
			"params":  map[string]interface{}{"event": event.Event, "data": event.Data},
		}

		// Snapshot subscribers under lock, then write without holding it.
		// Writing to a dead connection can block indefinitely — holding
		// the lock during that write deadlocks the entire RPC server.
		s.mu.Lock()
		subs := make(map[net.Conn]*json.Encoder, len(s.subscribers))
		for conn, enc := range s.subscribers {
			subs[conn] = enc
		}
		s.mu.Unlock()

		for conn, enc := range subs {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := enc.Encode(msg); err != nil {
				s.mu.Lock()
				delete(s.subscribers, conn)
				s.mu.Unlock()
				conn.Close()
			}
		}
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

		// Subscribe hijacks the connection for push events
		if req.Method == "skyfs.subscribe" {
			s.logger.Debug("rpc", "method", "skyfs.subscribe")
			resp := &RPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"status": "subscribed"}}
			encoder.Encode(resp)
			s.mu.Lock()
			s.subscribers[conn] = encoder
			s.mu.Unlock()
			// Block until context is cancelled — connection stays open
			<-ctx.Done()
			return
		}

		start := time.Now()
		resp := s.dispatch(ctx, &req)
		ms := time.Since(start).Milliseconds()
		if resp.Error != nil {
			s.logger.Warn("rpc", "method", req.Method, "ms", ms, "error", resp.Error.Message)
		} else {
			s.logger.Debug("rpc", "method", req.Method, "ms", ms)
		}
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
	case "skyfs.ping":
		return &RPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"status": "ok"}}
	case "skyfs.health":
		result, err = s.rpcHealth(ctx)
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
	case "skyfs.reset":
		result, err = s.rpcReset(ctx)
	case "skyfs.syncStart":
		result, err = s.rpcSyncStart(ctx, req.Params)
	case "skyfs.syncStop":
		result, err = s.rpcSyncStop(ctx)
	case "skyfs.syncStatus":
		result, err = s.rpcSyncStatus(ctx)
	case "skyfs.driveCreate":
		result, err = s.rpcDriveCreate(ctx, req.Params)
	case "skyfs.driveRemove":
		result, err = s.rpcDriveRemove(ctx, req.Params)
	case "skyfs.driveList":
		result, err = s.rpcDriveList(ctx)
	case "skyfs.driveStart":
		result, err = s.rpcDriveStart(ctx, req.Params)
	case "skyfs.driveStop":
		result, err = s.rpcDriveStop(ctx, req.Params)
	case "skyfs.deviceList":
		result, err = s.rpcDeviceList(ctx)
	case "skyfs.deviceRemove":
		result, err = s.rpcDeviceRemove(ctx, req.Params)
	case "skyfs.invite":
		result, err = s.rpcInvite(ctx)
	case "skyfs.join":
		result, err = s.rpcJoin(ctx, req.Params)
	case "skyfs.approve":
		result, err = s.rpcApprove(ctx)
	case "skyfs.syncActivity":
		result, err = s.rpcSyncActivity(ctx)
	case "skyfs.debugDump":
		result, err = s.rpcDebugDump(ctx)
	case "skyfs.debugList":
		result, err = s.rpcDebugList(ctx)
	case "skyfs.debugGet":
		result, err = s.rpcDebugGet(ctx, req.Params)
	case "skyfs.s3List":
		result, err = s.rpcS3List(ctx, req.Params)
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
	Dirs  []dirEntry `json:"dirs,omitempty"`
}

type dirEntry struct {
	Path      string `json:"path"`
	Namespace string `json:"namespace,omitempty"`
}

type fileInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	Checksum  string `json:"checksum"`
	Namespace string `json:"namespace"`
	Chunks    int    `json:"chunks"`
}

func (s *RPCServer) rpcList(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p listParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	// Copy drives under read lock, then release before doing file I/O
	s.driveManager.mu.RLock()
	drivesCopy := make([]*Drive, 0, len(s.driveManager.drives))
	for _, d := range s.driveManager.drives {
		drivesCopy = append(drivesCopy, d)
	}
	s.driveManager.mu.RUnlock()

	var files []fileInfo
	var dirs []dirEntry
	for _, drive := range drivesCopy {
		localLog := opslog.NewLocalOpsLog(
			filepath.Join(driveDataDir(drive.ID), "ops.jsonl"),
			s.store.deviceID,
		)
		snap, err := localLog.Snapshot()
		if err != nil {
			continue
		}
		for path, fi := range snap.Files() {
			if p.Prefix != "" && (len(path) < len(p.Prefix) || path[:len(p.Prefix)] != p.Prefix) {
				continue
			}
			localPath := filepath.Join(drive.LocalPath, filepath.FromSlash(path))
			var size int64
			var mod string
			if info, err := os.Stat(localPath); err == nil {
				size = info.Size()
				mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}
			files = append(files, fileInfo{
				Path:      path,
				Size:      size,
				Modified:  mod,
				Checksum:  fi.Checksum,
				Namespace: fi.Namespace,
				Chunks:    len(fi.Chunks),
			})
		}
		for path, di := range snap.Dirs() {
			if p.Prefix != "" && (len(path) < len(p.Prefix) || path[:len(p.Prefix)] != p.Prefix) {
				continue
			}
			dirs = append(dirs, dirEntry{
				Path:      path,
				Namespace: di.Namespace,
			})
		}
	}

	return listResult{Files: files, Dirs: dirs}, nil
}

func (s *RPCServer) rpcInfo(_ context.Context) (interface{}, error) {
	info := &StoreInfo{
		ID: s.store.identity.Address(),
	}

	// Copy drives under read lock, then release before doing file I/O
	s.driveManager.mu.RLock()
	infoDrives := make([]*Drive, 0, len(s.driveManager.drives))
	for _, d := range s.driveManager.drives {
		infoDrives = append(infoDrives, d)
	}
	s.driveManager.mu.RUnlock()

	namespaces := make(map[string]bool)
	for _, drive := range infoDrives {
		localLog := opslog.NewLocalOpsLog(
			filepath.Join(driveDataDir(drive.ID), "ops.jsonl"),
			s.store.deviceID,
		)
		snap, err := localLog.Snapshot()
		if err != nil {
			continue
		}
		snapFiles := snap.Files()
		info.FileCount += len(snapFiles)
		for path := range snapFiles {
			localPath := filepath.Join(drive.LocalPath, filepath.FromSlash(path))
			if fi, err := os.Stat(localPath); err == nil {
				info.TotalSize += fi.Size()
			}
		}
		if drive.Namespace != "" {
			namespaces[drive.Namespace] = true
		}
	}

	for ns := range namespaces {
		info.Namespaces = append(info.Namespaces, ns)
	}
	return info, nil
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
	Syncing bool   `json:"syncing"`
	SyncDir string `json:"sync_dir,omitempty"`
}

func (s *RPCServer) rpcStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return statusResult{Syncing: s.syncing, SyncDir: s.syncDir}, nil
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

	s.Emit("compact.start", map[string]any{"phase": "reading ops"})

	result, err := Compact(ctx, s.store.backend, s.store.identity, p.Keep)
	if err != nil {
		s.Emit("compact.error", map[string]any{"error": err.Error()})
		return nil, err
	}

	s.Emit("compact.complete", map[string]any{
		"ops_compacted":     result.OpsCompacted,
		"ops_deleted":       result.OpsDeleted,
		"snapshots_kept":    result.SnapshotsKept,
		"snapshots_deleted": result.SnapshotsDeleted,
	})
	return result, nil
}

func (s *RPCServer) rpcReset(ctx context.Context) (interface{}, error) {
	deleted := 0

	// Delete all S3 ops
	if keys, err := s.store.backend.List(ctx, "ops/"); err == nil {
		for _, key := range keys {
			s.store.backend.Delete(ctx, key)
			deleted++
		}
	}

	// Delete all S3 snapshots
	if keys, err := s.store.backend.List(ctx, "manifests/snapshot-"); err == nil {
		for _, key := range keys {
			s.store.backend.Delete(ctx, key)
			deleted++
		}
	}

	// Delete local drive state files
	home, _ := os.UserHomeDir()
	drivesDir := filepath.Join(home, ".sky10", "fs", "drives")
	stateFiles := []string{"ops.jsonl", "outbox.jsonl", "state.json", "inbox.jsonl", "manifest.json"}
	localDeleted := 0
	if entries, err := os.ReadDir(drivesDir); err == nil {
		for _, d := range entries {
			if !d.IsDir() {
				continue
			}
			for _, f := range stateFiles {
				if os.Remove(filepath.Join(drivesDir, d.Name(), f)) == nil {
					localDeleted++
				}
			}
		}
	}

	s.logger.Info("reset complete", "s3_deleted", deleted, "local_deleted", localDeleted)
	return map[string]interface{}{
		"s3_deleted":    deleted,
		"local_deleted": localDeleted,
	}, nil
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

// --- Sync control ---

type syncStartParams struct {
	Dir         string `json:"dir"`
	PollSeconds int    `json:"poll_seconds"`
}

func (s *RPCServer) rpcSyncStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p syncStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Dir == "" {
		return nil, fmt.Errorf("dir is required")
	}
	if p.PollSeconds <= 0 {
		p.PollSeconds = 30
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// Stop existing sync if running
	if s.syncCancel != nil {
		s.syncCancel()
	}

	syncCtx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncDir = p.Dir
	s.syncing = true

	ignoreMatcher := NewIgnoreMatcher(p.Dir)
	cfg := SyncConfig{
		LocalRoot:  p.Dir,
		IgnoreFunc: ignoreMatcher.IgnoreFunc(),
	}
	daemonCfg := DaemonConfig{
		SyncConfig:  cfg,
		PollSeconds: p.PollSeconds,
	}

	daemon, err := NewDaemon(s.store, nil, daemonCfg, s.logger)
	if err != nil {
		s.syncing = false
		s.syncCancel = nil
		return nil, fmt.Errorf("creating daemon: %w", err)
	}

	go func() {
		daemon.Run(syncCtx)
		s.syncMu.Lock()
		s.syncing = false
		s.syncDir = ""
		s.syncCancel = nil
		s.syncMu.Unlock()
		s.logger.Info("sync stopped", "dir", p.Dir)
	}()

	s.logger.Info("sync started", "dir", p.Dir, "poll", p.PollSeconds)
	return map[string]string{"status": "started", "dir": p.Dir}, nil
}

func (s *RPCServer) rpcSyncStop(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.syncCancel == nil {
		return map[string]string{"status": "not syncing"}, nil
	}

	s.syncCancel()
	s.syncCancel = nil
	return map[string]string{"status": "stopping"}, nil
}

func (s *RPCServer) rpcSyncStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	syncing := s.syncing
	syncDir := s.syncDir
	s.syncMu.Unlock()

	s.activityMu.Lock()
	active := time.Since(s.lastActivity) < 15*time.Second
	s.activityMu.Unlock()

	return map[string]interface{}{
		"syncing":  syncing || active,
		"sync_dir": syncDir,
	}, nil
}

func (s *RPCServer) rpcHealth(_ context.Context) (interface{}, error) {
	uptime := time.Since(s.startTime)

	s.activityMu.Lock()
	lastActivity := s.lastActivity
	s.activityMu.Unlock()

	// Per-drive health — use RLock, check daemons map directly
	// (don't call IsRunning which also takes the lock)
	s.driveManager.mu.RLock()
	driveCount := len(s.driveManager.drives)
	runningCount := len(s.driveManager.daemons)
	var driveIDs []string
	for id := range s.driveManager.drives {
		driveIDs = append(driveIDs, id)
	}
	s.driveManager.mu.RUnlock()

	// Outbox pending — no lock needed, just reading files
	outboxTotal := 0
	for _, id := range driveIDs {
		dir := driveDataDir(id)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			outboxTotal += len(entries)
		}
	}

	s.mu.Lock()
	subscribers := len(s.subscribers)
	clients := len(s.clients)
	s.mu.Unlock()

	var lastActivityAgo string
	if lastActivity.IsZero() {
		lastActivityAgo = "never"
	} else {
		lastActivityAgo = time.Since(lastActivity).Truncate(time.Second).String()
	}

	return map[string]interface{}{
		"status":            "ok",
		"version":           s.version,
		"uptime":            uptime.Truncate(time.Second).String(),
		"drives":            driveCount,
		"drives_running":    runningCount,
		"outbox_pending":    outboxTotal,
		"last_activity_ago": lastActivityAgo,
		"rpc_clients":       clients,
		"rpc_subscribers":   subscribers,
	}, nil
}

// MarkActivity records that sync I/O is happening right now.
func (s *RPCServer) MarkActivity() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}

// --- Drive management ---

type driveCreateParams struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Namespace string `json:"namespace"`
}

type driveInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPath string `json:"local_path"`
	Namespace string `json:"namespace"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
}

func (s *RPCServer) rpcDriveCreate(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" || p.Path == "" {
		return nil, fmt.Errorf("name and path are required")
	}
	if p.Namespace == "" {
		p.Namespace = p.Name
	}

	drive, err := s.driveManager.CreateDrive(p.Name, p.Path, p.Namespace)
	if err != nil {
		return nil, err
	}

	// Auto-start
	s.driveManager.StartDrive(drive.ID, s.logger)

	return driveInfo{
		ID: drive.ID, Name: drive.Name, LocalPath: drive.LocalPath,
		Namespace: drive.Namespace, Enabled: drive.Enabled, Running: true,
	}, nil
}

type driveIDParams struct {
	ID string `json:"id"`
}

func (s *RPCServer) rpcDriveRemove(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "ok"}, s.driveManager.RemoveDrive(p.ID)
}

func (s *RPCServer) rpcDriveList(_ context.Context) (interface{}, error) {
	drives := s.driveManager.ListDrives()
	result := make([]driveInfo, len(drives))
	for i, d := range drives {
		result[i] = driveInfo{
			ID: d.ID, Name: d.Name, LocalPath: d.LocalPath,
			Namespace: d.Namespace, Enabled: d.Enabled,
			Running: s.driveManager.IsRunning(d.ID),
		}
	}
	return map[string]interface{}{"drives": result}, nil
}

func (s *RPCServer) rpcDriveStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "started"}, s.driveManager.StartDrive(p.ID, s.logger)
}

func (s *RPCServer) rpcDriveStop(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	s.driveManager.StopDrive(p.ID)
	return map[string]string{"status": "stopped"}, nil
}

// --- Device registry ---

func (s *RPCServer) rpcDeviceList(ctx context.Context) (interface{}, error) {
	devices, err := ListDevices(ctx, s.store.backend)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"devices":     devices,
		"this_device": s.store.identity.Address(),
	}, nil
}

func (s *RPCServer) rpcDeviceRemove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Pubkey == s.store.identity.Address() {
		return nil, fmt.Errorf("cannot remove this device")
	}
	id := shortPubkeyID(p.Pubkey)
	key := "devices/" + id + ".json"
	if err := s.store.backend.Delete(ctx, key); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func (s *RPCServer) rpcInvite(ctx context.Context) (interface{}, error) {
	accessKey := os.Getenv("S3_ACCESS_KEY_ID")
	secretKey := os.Getenv("S3_SECRET_ACCESS_KEY")

	// Read endpoint/bucket from config file
	home, _ := os.UserHomeDir()
	cfgData, err := os.ReadFile(home + "/.sky10/fs/config.json")
	var endpoint, bucket, region string
	var pathStyle bool
	if err == nil {
		var cfg struct {
			Endpoint       string `json:"endpoint"`
			Bucket         string `json:"bucket"`
			Region         string `json:"region"`
			ForcePathStyle bool   `json:"force_path_style"`
		}
		json.Unmarshal(cfgData, &cfg)
		endpoint = cfg.Endpoint
		bucket = cfg.Bucket
		region = cfg.Region
		pathStyle = cfg.ForcePathStyle
	}

	code, err := CreateInvite(ctx, s.store.backend, InviteConfig{
		Endpoint:       endpoint,
		Bucket:         bucket,
		Region:         region,
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		ForcePathStyle: pathStyle,
		DevicePubKey:   s.store.identity.Address(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"code": code}, nil
}

func (s *RPCServer) rpcJoin(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		InviteID string `json:"invite_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.InviteID == "" {
		return nil, fmt.Errorf("invite_id required")
	}

	// Submit this device's pubkey to the invite mailbox
	if err := SubmitJoin(ctx, s.store.backend, p.InviteID, s.store.identity.Address()); err != nil {
		return nil, fmt.Errorf("submitting join: %w", err)
	}

	// Poll for approval (up to 60 seconds)
	for i := 0; i < 12; i++ {
		granted, err := IsGranted(ctx, s.store.backend, p.InviteID)
		if err != nil {
			return nil, err
		}
		if granted {
			// Register this device
			RegisterDevice(ctx, s.store.backend, s.store.identity.Address(), GetDeviceName(), s.version)
			return map[string]string{"status": "approved"}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return map[string]string{"status": "pending"}, nil
}

func (s *RPCServer) rpcApprove(ctx context.Context) (interface{}, error) {
	// Find pending invites
	inviteKeys, err := s.store.backend.List(ctx, "invites/")
	if err != nil {
		return nil, err
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}

	approved := 0
	for inviteID := range inviteIDs {
		joinerAddr, err := CheckJoinRequest(ctx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(ctx, s.store.backend, inviteID)
		if granted {
			if s.joinerHasAllKeys(ctx, joinerAddr) {
				continue
			}
		}
		if err := ApproveJoin(ctx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			continue
		}
		// Register the joiner as a device
		approved++
		// Don't cleanup — joiner needs to poll and see the granted marker
	}

	return map[string]int{"approved": approved}, nil
}

// autoApproveLoop polls for pending join requests and approves them automatically.
// The invite code itself is the authorization — no manual step needed.
func (s *RPCServer) autoApproveLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	// Run once immediately on startup
	s.tryAutoApprove(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tryAutoApprove(ctx)
		}
	}
}

func (s *RPCServer) tryAutoApprove(ctx context.Context) {
	// Hard timeout: entire cycle must finish in 10 seconds.
	// If any S3 call hangs, we bail out instead of blocking forever.
	cycleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s.logger.Debug("auto-approve: checking")
	inviteKeys, err := s.store.backend.List(cycleCtx, "invites/")
	if err != nil {
		s.logger.Warn("auto-approve: list failed", "error", err)
		return
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}
	s.logger.Debug("auto-approve: invites", "count", len(inviteIDs))

	for inviteID := range inviteIDs {
		// Skip invites we've already confirmed are fully complete
		s.mu.Lock()
		if s.completedInvites[inviteID] {
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		joinerAddr, err := CheckJoinRequest(cycleCtx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(cycleCtx, s.store.backend, inviteID)
		if granted {
			if s.joinerHasAllKeys(cycleCtx, joinerAddr) {
				s.logger.Debug("auto-approve: already complete", "invite", inviteID[:8])
				s.mu.Lock()
				s.completedInvites[inviteID] = true
				s.mu.Unlock()
				continue
			}
		}
		if err := ApproveJoin(cycleCtx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			s.logger.Warn("auto-approve failed", "invite", inviteID, "error", err)
			continue
		}
		s.logger.Info("auto-approved device", "address", joinerAddr)
	}
}

// joinerHasAllKeys checks if the joiner has a wrapped key for every
// namespace that this device (the approver) has access to.
func (s *RPCServer) joinerHasAllKeys(ctx context.Context, joinerAddr string) bool {
	joinerID := shortPubkeyID(joinerAddr)
	myID := shortPubkeyID(s.store.identity.Address())

	allKeys, err := s.store.backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return false
	}

	// Find namespaces we have access to (our device-specific key or the base key)
	myNamespaces := make(map[string]bool)
	for _, k := range allKeys {
		ns := extractNamespaceName(k)
		// Check if we can unwrap this key (it's ours)
		if strings.Contains(k, "."+myID+".") || strings.HasSuffix(k, ns+".ns.enc") {
			myNamespaces[ns] = true
		}
	}

	// Check joiner has a key for each namespace
	for ns := range myNamespaces {
		joinerKeyPath := "keys/namespaces/" + ns + "." + joinerID + ".ns.enc"
		if _, err := s.store.backend.Head(ctx, joinerKeyPath); err != nil {
			return false
		}
	}

	return true
}

// --- Sync activity ---

type activityEntry struct {
	Direction string `json:"direction"` // "up" or "down"
	Op        string `json:"op"`        // "put" or "delete"
	Path      string `json:"path"`
	DriveID   string `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Timestamp int64  `json:"ts"`
}

func (s *RPCServer) rpcSyncActivity(_ context.Context) (interface{}, error) {
	s.driveManager.mu.RLock()
	drives := make(map[string]*Drive, len(s.driveManager.drives))
	for id, d := range s.driveManager.drives {
		drives[id] = d
	}
	s.driveManager.mu.RUnlock()

	pending := make([]activityEntry, 0)

	for id, d := range drives {
		dir := driveDataDir(id)

		// Read outbox (pending uploads)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			for _, e := range entries {
				pending = append(pending, activityEntry{
					Direction: "up",
					Op:        string(e.Op),
					Path:      e.Path,
					DriveID:   id,
					DriveName: d.Name,
					Timestamp: e.Timestamp,
				})
			}
		}

	}

	return map[string]interface{}{"pending": pending}, nil
}

// --- Debug dump ---

func (s *RPCServer) rpcDebugDump(ctx context.Context) (interface{}, error) {
	hostname, _ := os.Hostname()
	deviceAddr := s.store.identity.Address()
	deviceID := shortPubkeyID(deviceAddr)
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")

	dump := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"device":    hostname,
		"device_id": deviceID,
		"pubkey":    deviceAddr,
		"version":   s.version,
	}

	// Collect per-drive data — all local reads, no S3
	s.driveManager.mu.RLock()
	drivesCopy := make(map[string]*Drive, len(s.driveManager.drives))
	for id, d := range s.driveManager.drives {
		drivesCopy[id] = d
	}
	s.driveManager.mu.RUnlock()

	driveDumps := make([]map[string]interface{}, 0)
	for id, d := range drivesCopy {
		dd := map[string]interface{}{
			"id":         id,
			"name":       d.Name,
			"local_path": d.LocalPath,
			"namespace":  d.Namespace,
			"enabled":    d.Enabled,
			"running":    s.driveManager.IsRunning(id),
		}

		dir := driveDataDir(id)

		// Ops log snapshot (local file read)
		localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), s.store.deviceID)
		if snap, err := localLog.Snapshot(); err == nil {
			dd["snapshot_files"] = snap.Files()
			dd["snapshot_file_count"] = snap.Len()
			dd["last_remote_op"] = localLog.LastRemoteOp()
		}

		// Outbox (local file read)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			dd["outbox"] = entries
			dd["outbox_count"] = len(entries)
		}

		// Local files on disk
		if files, _, err := ScanDirectory(d.LocalPath, nil); err == nil {
			localFiles := make(map[string]string)
			for path, cksum := range files {
				localFiles[path] = cksum
			}
			dd["local_files"] = localFiles
			dd["local_file_count"] = len(localFiles)
		}

		driveDumps = append(driveDumps, dd)
	}
	dump["drives"] = driveDumps

	// S3 calls with short timeouts — each one independent
	s3ctx, s3cancel := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel()

	if keys, err := s.store.backend.List(s3ctx, "ops/"); err == nil {
		dump["remote_ops_count"] = len(keys)
		if len(keys) > 20 {
			keys = keys[len(keys)-20:]
		}
		dump["remote_ops_recent"] = keys
	} else {
		dump["remote_ops_error"] = err.Error()
	}

	s3ctx2, s3cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel2()

	if devices, err := ListDevices(s3ctx2, s.store.backend); err == nil {
		dump["devices"] = devices
	} else {
		dump["devices_error"] = err.Error()
	}

	s3ctx3, s3cancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel3()

	if keys, err := s.store.backend.List(s3ctx3, "keys/namespaces/"); err == nil {
		dump["namespace_keys"] = keys
	} else {
		dump["namespace_keys_error"] = err.Error()
	}

	// Logs — full daemon.log file
	logPath := DaemonLogPath()
	if logData, err := os.ReadFile(logPath); err == nil {
		dump["logs_raw"] = string(logData)
	}
	// Also include ring buffer in case file read fails
	dump["logs"] = s.logBuf.Lines()

	// Upload to S3 — no wall-clock timeout. The HTTP client has its own
	// idle/read timeouts for dead connections. A fixed deadline kills
	// active uploads that are streaming bytes but happen to be large.
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling debug dump: %w", err)
	}

	key := fmt.Sprintf("debug/%s/%s.json", deviceID, ts)
	r := strings.NewReader(string(data))
	if err := s.store.backend.Put(ctx, key, r, int64(len(data))); err != nil {
		return nil, fmt.Errorf("uploading debug dump: %w", err)
	}

	s.logger.Info("debug dump uploaded", "key", key, "size", len(data))

	return map[string]interface{}{
		"status": "uploaded",
		"key":    key,
		"size":   len(data),
	}, nil
}

func (s *RPCServer) rpcDebugList(ctx context.Context) (interface{}, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	keys, err := s.store.backend.List(listCtx, "debug/")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"keys": keys}, nil
}

func (s *RPCServer) rpcDebugGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rc, err := s.store.backend.Get(getCtx, p.Key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var parsed interface{}
	json.Unmarshal(data, &parsed)
	return parsed, nil
}

func (s *RPCServer) rpcS3List(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Prefix string `json:"prefix"`
	}
	if params != nil {
		json.Unmarshal(params, &p)
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	keys, err := s.store.backend.List(listCtx, p.Prefix)
	if err != nil {
		return nil, err
	}

	// Group by next path component to show "directories"
	type s3Entry struct {
		Key  string `json:"key"`
		Size int64  `json:"size"`
	}
	var files []s3Entry
	dirSet := make(map[string]bool)

	prefixLen := len(p.Prefix)
	for _, key := range keys {
		rest := key[prefixLen:]
		if idx := strings.Index(rest, "/"); idx >= 0 {
			dir := p.Prefix + rest[:idx+1]
			dirSet[dir] = true
		} else {
			meta, err := s.store.backend.Head(listCtx, key)
			size := int64(0)
			if err == nil {
				size = meta.Size
			}
			files = append(files, s3Entry{Key: key, Size: size})
		}
	}

	var dirs []string
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	return map[string]interface{}{
		"files":  files,
		"dirs":   dirs,
		"prefix": p.Prefix,
		"total":  len(keys),
	}, nil
}

func splitInvitePath2(key string) string {
	if len(key) < 9 || key[:8] != "invites/" {
		return ""
	}
	rest := key[8:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return ""
}
