package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Server is a JSON-RPC 2.0 server that listens on a Unix socket and
// optionally HTTP. Handlers register for method namespaces.
type Server struct {
	sockPath string
	version  string
	listener net.Listener
	logger   *slog.Logger

	mu          sync.Mutex
	clients     map[net.Conn]bool
	subscribers map[net.Conn]*json.Encoder
	events      chan Event
	handlers    []Handler

	// HTTP
	httpAddr   string
	httpSubMu  sync.RWMutex
	httpSubs   []*httpSubscriber
	httpRoutes []httpRoute

	// Callbacks
	onServe func() // called after listener is bound, before accept loop
}

type httpRoute struct {
	pattern string
	handler http.HandlerFunc
}

// HandleHTTP registers an HTTP handler on the server's HTTP mux.
// Must be called before ServeHTTP.
func (s *Server) HandleHTTP(pattern string, handler http.HandlerFunc) {
	s.httpRoutes = append(s.httpRoutes, httpRoute{pattern, handler})
}

// NewServer creates an RPC server.
func NewServer(sockPath, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		sockPath:    sockPath,
		version:     version,
		logger:      logger,
		clients:     make(map[net.Conn]bool),
		subscribers: make(map[net.Conn]*json.Encoder),
		events:      make(chan Event, 100),
	}
}

// RegisterHandler adds a handler for RPC method dispatch.
func (s *Server) RegisterHandler(h Handler) {
	s.handlers = append(s.handlers, h)
}

// OnServe sets a callback invoked after the socket listener binds but
// before the accept loop starts. Used for one-time initialization like
// auto-starting drives.
func (s *Server) OnServe(fn func()) {
	s.onServe = fn
}

// Version returns the server version string.
func (s *Server) Version() string { return s.version }

// Logger returns the server logger.
func (s *Server) Logger() *slog.Logger { return s.logger }

// Serve starts listening and blocks until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	os.Remove(s.sockPath)
	os.MkdirAll(filepath.Dir(s.sockPath), 0755)

	var err error
	s.listener, err = net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.sockPath, err)
	}
	defer s.listener.Close()
	defer os.Remove(s.sockPath)

	os.Chmod(s.sockPath, 0600)
	s.logger.Info("RPC server started", "socket", s.sockPath, "version", s.version)

	go s.broadcastLoop()

	if s.onServe != nil {
		s.onServe()
	}

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

// Emit sends an event to all connected subscribers.
func (s *Server) Emit(event string, data interface{}) {
	select {
	case s.events <- Event{Name: event, Data: data}:
	default:
	}
}

// ClientCount returns the number of connected clients.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// SubscriberCount returns the number of event subscribers.
func (s *Server) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req Request
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				s.logger.Debug("decode error", "error", err)
			}
			return
		}

		// Subscribe hijacks the connection for push events.
		// Accept both "subscribe" and legacy "skyfs.subscribe".
		if req.Method == "subscribe" || req.Method == "skyfs.subscribe" {
			s.logger.Debug("rpc", "method", req.Method)
			resp := &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"status": "subscribed"}}
			encoder.Encode(resp)
			s.mu.Lock()
			s.subscribers[conn] = encoder
			s.mu.Unlock()
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

func (s *Server) dispatch(ctx context.Context, req *Request) *Response {
	resp := &Response{JSONRPC: "2.0", ID: req.ID}

	for _, h := range s.handlers {
		result, err, ok := h.Dispatch(ctx, req.Method, req.Params)
		if ok {
			if err != nil {
				resp.Error = &Error{Code: -32000, Message: err.Error()}
			} else {
				resp.Result = result
			}
			return resp
		}
	}

	resp.Error = &Error{Code: -32601, Message: "method not found: " + req.Method}
	return resp
}

func (s *Server) broadcastLoop() {
	for event := range s.events {
		msg := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "event",
			"params":  map[string]interface{}{"event": event.Name, "data": event.Data},
		}

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

		s.broadcastToHTTP(event)
	}
}
