package rpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type flushRecorder struct {
	header http.Header

	mu      sync.Mutex
	onFlush func()
}

func (r *flushRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *flushRecorder) Write(p []byte) (int, error) {
	return len(p), nil
}

func (r *flushRecorder) WriteHeader(statusCode int) {}

func (r *flushRecorder) Flush() {
	r.mu.Lock()
	onFlush := r.onFlush
	r.mu.Unlock()
	if onFlush != nil {
		onFlush()
	}
}

func TestHandleHTTPEventsRegistersSubscriberBeforeFirstFlush(t *testing.T) {
	t.Parallel()

	srv := NewServer("/tmp/test-http-events.sock", "test", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flushed := make(chan int, 1)
	recorder := &flushRecorder{}
	recorder.onFlush = func() {
		select {
		case flushed <- srv.SubscriberCount():
		default:
		}
		cancel()
	}

	req := httptest.NewRequest(http.MethodGet, "/rpc/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		srv.handleHTTPEvents(recorder, req)
		close(done)
	}()

	select {
	case count := <-flushed:
		if count != 1 {
			t.Fatalf("subscriber count at first flush = %d, want 1", count)
		}
	case <-time.After(time.Second):
		t.Fatal("first flush not observed")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleHTTPEvents did not return after context cancel")
	}

	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
}

func TestServeHTTPDefaultsToLoopback(t *testing.T) {
	t.Parallel()

	srv := startHTTPTestServer(t, func(ctx context.Context, srv *Server) error {
		return srv.ServeHTTP(ctx, 0)
	})

	host, port, err := net.SplitHostPort(srv.HTTPAddr())
	if err != nil {
		t.Fatalf("parse HTTP server address %q: %v", srv.HTTPAddr(), err)
	}
	if host != DefaultHTTPBindAddress {
		t.Fatalf("HTTP bind host = %q, want %q", host, DefaultHTTPBindAddress)
	}

	client := http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(DefaultHTTPBindAddress, port) + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHTTPListenAddressUsesExplicitBindAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		bind string
		port int
		want string
	}{
		{
			name: "empty bind defaults loopback",
			bind: "",
			port: 9101,
			want: "127.0.0.1:9101",
		},
		{
			name: "explicit wildcard",
			bind: "0.0.0.0",
			port: 9101,
			want: "0.0.0.0:9101",
		},
		{
			name: "ipv6 loopback",
			bind: "::1",
			port: 9101,
			want: "[::1]:9101",
		},
		{
			name: "trimmed host",
			bind: " localhost ",
			port: 0,
			want: "localhost:0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := httpListenAddress(tc.bind, tc.port); got != tc.want {
				t.Fatalf("httpListenAddress(%q, %d) = %q, want %q", tc.bind, tc.port, got, tc.want)
			}
		})
	}
}

func startHTTPTestServer(t *testing.T, serve func(context.Context, *Server) error) *Server {
	t.Helper()

	srv := NewServer(filepath.Join(t.TempDir(), "test.sock"), "test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, srv)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for srv.HTTPAddr() == "" {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("timed out waiting for HTTP server bind")
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("ServeHTTP returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for HTTP server shutdown")
		}
	})

	return srv
}
