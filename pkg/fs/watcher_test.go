package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: flushPending must not permanently drop events when the channel
// is full. Previously, events were deleted from the pending map BEFORE the
// channel send, so when the channel was full the event was silently lost
// with no retry. With 107 files and a capacity-100 channel, ~7 events
// were permanently dropped — causing missing files on the remote device.
func TestFlushPendingRetainsEventsWhenChannelFull(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const total = 120
	const chanCap = 100

	w := &Watcher{
		root:     root,
		events:   make(chan FileEvent, chanCap),
		pending:  make(map[string]time.Time),
		debounce: 0,
	}

	// Create files on disk and add to pending (past debounce window)
	pastDebounce := time.Now().Add(-time.Second)
	for i := 0; i < total; i++ {
		path := fmt.Sprintf("file-%03d.txt", i)
		os.WriteFile(filepath.Join(root, path), []byte("data"), 0644)
		w.pending[path] = pastDebounce
	}

	// Don't consume from events channel — it fills to capacity
	w.flushPending()

	sent := len(w.events)
	retained := len(w.pending)

	if sent != chanCap {
		t.Errorf("channel has %d events, want %d", sent, chanCap)
	}

	// The events that couldn't be sent MUST remain in pending
	// for retry on the next flushPending tick.
	want := total - chanCap
	if retained != want {
		t.Errorf("pending has %d events, want %d — dropped events are permanently lost", retained, want)
	}

	// Drain some events to make room
	for i := 0; i < want; i++ {
		<-w.events
	}

	// Second flush should send the retained events
	w.flushPending()

	if len(w.pending) != 0 {
		t.Errorf("after retry flush: pending still has %d events, want 0", len(w.pending))
	}
	if len(w.events) != chanCap {
		t.Errorf("after retry flush: channel has %d events, want %d", len(w.events), chanCap)
	}
}
