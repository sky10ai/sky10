package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
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
