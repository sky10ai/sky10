package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// httpSubscriber tracks an SSE connection for the broadcast loop.
type httpSubscriber struct {
	ch   chan RPCEvent
	done chan struct{}
}

// ServeHTTP starts an HTTP server alongside the Unix socket.
// POST /rpc — JSON-RPC 2.0 request/response
// GET /rpc/events — SSE stream of push events
// GET /health — status check
func (s *RPCServer) ServeHTTP(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rpc", s.handleHTTPRPC)
	mux.HandleFunc("GET /rpc/events", s.handleHTTPEvents)
	mux.HandleFunc("GET /health", s.handleHTTPHealth)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	s.logger.Info("HTTP RPC server started", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *RPCServer) handleHTTPRPC(w http.ResponseWriter, r *http.Request) {

	var req RPCRequest
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

func (s *RPCServer) handleHTTPHealth(w http.ResponseWriter, r *http.Request) {
	// Health is unauthenticated — just returns status
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

func (s *RPCServer) handleHTTPEvents(w http.ResponseWriter, r *http.Request) {
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
		ch:   make(chan RPCEvent, 100),
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
				"event": event.Event,
				"data":  event.Data,
			})
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Event, data)
			flusher.Flush()
		}
	}
}

// broadcastToHTTP fans out an event to all SSE subscribers.
func (s *RPCServer) broadcastToHTTP(event RPCEvent) {
	s.httpSubMu.RLock()
	subs := make([]*httpSubscriber, len(s.httpSubs))
	copy(subs, s.httpSubs)
	s.httpSubMu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- event:
		default:
			// Drop if subscriber is slow
		}
	}
}
