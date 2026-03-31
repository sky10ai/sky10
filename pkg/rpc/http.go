package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// DefaultHTTPPort is the preferred port for the HTTP RPC server.
const DefaultHTTPPort = 9101

type httpSubscriber struct {
	ch   chan Event
	done chan struct{}
}

// ServeHTTP starts an HTTP server alongside the Unix socket.
func (s *Server) ServeHTTP(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleHTTPRoot)
	mux.HandleFunc("POST /rpc", s.handleHTTPRPC)
	mux.HandleFunc("GET /rpc/events", s.handleHTTPEvents)
	mux.HandleFunc("GET /health", s.handleHTTPHealth)

	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
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
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	start := time.Now()
	resp := s.dispatch(r.Context(), &req)
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
	flusher.Flush()

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
		default:
		}
	}
}
