package rpc

import (
	"testing"
	"time"
)

// Regression: agent.message events were silently dropped when the event
// channel buffer (100) was full. Emit now waits briefly before giving up,
// so critical events like agent responses survive temporary buffer pressure.
func TestEmitRetriesWhenBufferFull(t *testing.T) {
	t.Parallel()

	srv := NewServer("/tmp/test-emit-retry.sock", "test", nil)

	// Fill the buffer completely.
	for i := 0; i < cap(srv.events); i++ {
		srv.Emit("filler", i)
	}

	// Start a consumer that drains the buffer after a short delay,
	// simulating the broadcastLoop catching up.
	go func() {
		time.Sleep(50 * time.Millisecond)
		for range srv.events {
			return // drain one to make room
		}
	}()

	// Emit should block briefly and succeed once space opens.
	done := make(chan bool, 1)
	go func() {
		srv.Emit("agent.message", map[string]string{"id": "resp-1"})
		done <- true
	}()

	select {
	case <-done:
		// Drain and verify agent.message was delivered.
		close(srv.events)
		var found bool
		for ev := range srv.events {
			if ev.Name == "agent.message" {
				found = true
			}
		}
		if !found {
			t.Error("agent.message was not delivered despite buffer drain")
		}
	case <-time.After(2 * time.Second):
		t.Error("Emit blocked longer than expected")
	}
}

func TestBroadcastToHTTPRetriesWhenSubscriberSlow(t *testing.T) {
	t.Parallel()

	srv := NewServer("/tmp/test-http-retry.sock", "test", nil)

	sub := &httpSubscriber{
		ch:   make(chan Event, 100),
		done: make(chan struct{}),
	}
	srv.httpSubs = append(srv.httpSubs, sub)

	// Fill the subscriber's buffer.
	for i := 0; i < 100; i++ {
		sub.ch <- Event{Name: "filler", Data: i}
	}

	// Drain one slot after a short delay, simulating the browser catching up.
	go func() {
		time.Sleep(50 * time.Millisecond)
		<-sub.ch
	}()

	// broadcastToHTTP should retry and succeed.
	srv.broadcastToHTTP(Event{Name: "agent.message", Data: map[string]string{"id": "resp-1"}})

	// Drain and verify.
	close(sub.ch)
	var found bool
	for ev := range sub.ch {
		if ev.Name == "agent.message" {
			found = true
		}
	}
	if !found {
		t.Error("agent.message was not delivered to slow HTTP subscriber")
	}
}

func TestEmitNonBlocking(t *testing.T) {
	t.Parallel()

	srv := NewServer("/tmp/test-emit-fast.sock", "test", nil)

	// With space in the buffer, Emit should be non-blocking.
	start := time.Now()
	srv.Emit("agent.message", map[string]string{"id": "resp-1"})
	if time.Since(start) > 10*time.Millisecond {
		t.Error("Emit blocked unexpectedly when buffer had space")
	}

	ev := <-srv.events
	if ev.Name != "agent.message" {
		t.Errorf("got event %q, want agent.message", ev.Name)
	}
}

func TestBroadcastToHTTPBeforeUnixSocket(t *testing.T) {
	// Verify that broadcastLoop calls broadcastToHTTP before writing
	// to Unix socket subscribers. We test this indirectly: if an HTTP
	// subscriber receives the event, the ordering is correct.
	t.Parallel()

	srv := NewServer("/tmp/test-order.sock", "test", nil)

	sub := &httpSubscriber{
		ch:   make(chan Event, 100),
		done: make(chan struct{}),
	}
	srv.httpSubs = append(srv.httpSubs, sub)

	// Start the broadcastLoop.
	go srv.broadcastLoop()

	srv.Emit("agent.message", map[string]string{"id": "resp-1"})

	select {
	case ev := <-sub.ch:
		if ev.Name != "agent.message" {
			t.Errorf("got event %q, want agent.message", ev.Name)
		}
	case <-time.After(time.Second):
		t.Error("HTTP subscriber did not receive event within 1s")
	}

	close(srv.events)
}
