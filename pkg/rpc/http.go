package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultHTTPPort is the preferred port for the HTTP RPC server.
	DefaultHTTPPort = 9101
	// DefaultHTTPBindAddress keeps the daemon-local HTTP RPC listener private
	// unless a caller explicitly opts into a wider bind address.
	DefaultHTTPBindAddress = "127.0.0.1"
)

type httpSubscriber struct {
	ch   chan Event
	done chan struct{}
}

// ServeHTTP starts an HTTP server alongside the Unix socket.
func (s *Server) ServeHTTP(ctx context.Context, port int) error {
	return s.ServeHTTPOn(ctx, DefaultHTTPBindAddress, port)
}

// ServeHTTPOn starts an HTTP server on an explicit bind address.
func (s *Server) ServeHTTPOn(ctx context.Context, bindAddress string, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rpc", s.handleHTTPRPC)
	mux.HandleFunc("GET /rpc/events", s.handleHTTPEvents)
	mux.HandleFunc("GET /health", s.handleHTTPHealth)

	// Register any custom HTTP routes (upload, download, etc.)
	for _, route := range s.httpRoutes {
		mux.HandleFunc(route.pattern, route.handler)
	}

	// Serve embedded web UI if assets are available, otherwise
	// keep the JSON info endpoint at root for API-only mode.
	if WebDist != nil {
		if _, err := fs.ReadFile(WebDist, "web/dist/index.html"); err == nil {
			mux.Handle("/", webUIHandler())
		} else {
			mux.HandleFunc("GET /{$}", s.handleHTTPRoot)
		}
	} else {
		mux.HandleFunc("GET /{$}", s.handleHTTPRoot)
	}

	addr := httpListenAddress(bindAddress, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", httpListenAddress(bindAddress, 0))
		if err != nil {
			return fmt.Errorf("http listen: %w", err)
		}
	}

	actualAddr := ln.Addr().String()
	s.mu.Lock()
	s.httpAddr = actualAddr
	s.mu.Unlock()

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	s.logger.Info("HTTP RPC server started", "addr", actualAddr)
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func httpListenAddress(bindAddress string, port int) string {
	bindAddress = strings.TrimSpace(bindAddress)
	if bindAddress == "" {
		bindAddress = DefaultHTTPBindAddress
	}
	return net.JoinHostPort(bindAddress, strconv.Itoa(port))
}

// HTTPAddr returns the address the HTTP server is listening on.
func (s *Server) HTTPAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.httpAddr
}

func (s *Server) handleHTTPRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"name":    "sky10",
		"version": s.version,
		"rpc":     "POST /rpc",
		"events":  "GET /rpc/events",
		"health":  "GET /health",
	})
}

func (s *Server) handleHTTPRPC(w http.ResponseWriter, r *http.Request) {
	// Allow cross-origin requests from the Vite dev server.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	start := time.Now()
	ctx := WithCallerInfo(r.Context(), "http", r.RemoteAddr)
	resp := s.dispatch(ctx, &req)
	ms := time.Since(start).Milliseconds()
	if resp.Error != nil {
		s.logger.Warn("http-rpc", "method", req.Method, "ms", ms, "error", resp.Error.Message)
	} else {
		s.logger.Debug("http-rpc", "method", req.Method, "ms", ms)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHTTPHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

func (s *Server) handleHTTPEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub := &httpSubscriber{
		ch:   make(chan Event, 100),
		done: make(chan struct{}),
	}

	s.httpSubMu.Lock()
	s.httpSubs = append(s.httpSubs, sub)
	s.httpSubMu.Unlock()

	defer func() {
		close(sub.done)
		s.httpSubMu.Lock()
		for i, ss := range s.httpSubs {
			if ss == sub {
				s.httpSubs = append(s.httpSubs[:i], s.httpSubs[i+1:]...)
				break
			}
		}
		s.httpSubMu.Unlock()
	}()

	s.logger.Info("SSE client connected", "remote", r.RemoteAddr)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-sub.ch:
			data, _ := json.Marshal(map[string]interface{}{
				"event": event.Name,
				"data":  event.Data,
			})
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, data)
			flusher.Flush()
		}
	}
}

func (s *Server) broadcastToHTTP(event Event) {
	s.httpSubMu.RLock()
	subs := make([]*httpSubscriber, len(s.httpSubs))
	copy(subs, s.httpSubs)
	s.httpSubMu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- event:
			continue
		default:
		}

		// Subscriber channel full — wait briefly before dropping.
		t := time.NewTimer(200 * time.Millisecond)
		select {
		case sub.ch <- event:
		case <-sub.done:
		case <-t.C:
			s.logger.Warn("SSE subscriber event dropped",
				"event", event.Name)
		}
		t.Stop()
	}
}
