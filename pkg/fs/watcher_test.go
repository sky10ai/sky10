package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Regression: when a directory tree is created inside a watched root (e.g.
// Finder bulk-copying a folder), handleEvent must detect ALL files, including
// those inside nested subdirectories. Previously, handleEvent used
// w.watcher.Add (single dir) instead of addRecursive, and the ReadDir scan
// skipped subdirectories. Files inside nested dirs created before the watch
// was registered were permanently invisible.
//
// This test creates the entire tree BEFORE calling handleEvent — guaranteeing
// the race condition where subdirs exist before their parent is watched.
func TestWatcherNestedDirectoryCreate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Create watcher internals directly (like TestFlushPending)
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer fsw.Close()

	w := &Watcher{
		root:     root,
		events:   make(chan FileEvent, 100),
		ignore:   nil,
		watcher:  fsw,
		done:     make(chan struct{}),
		pending:  make(map[string]time.Time),
		debounce: 0,
	}
	w.addRecursive(root)

	// Create the ENTIRE nested tree on disk BEFORE any events fire.
	// This simulates the race: Finder created everything before the
	// watcher registered watches on the subdirectories.
	base := filepath.Join(root, "theme", "example.iapresenter")
	os.MkdirAll(filepath.Join(base, "assets"), 0755)
	os.WriteFile(filepath.Join(base, "info.json"), []byte(`{"v":1}`), 0644)
	os.WriteFile(filepath.Join(base, "text.md"), []byte("# Hello"), 0644)
	os.WriteFile(filepath.Join(base, "assets", "logo.png"), []byte("png"), 0644)
	os.WriteFile(filepath.Join(base, "assets", "icon.png"), []byte("icon"), 0644)

	// Simulate the kqueue Create event for "theme/" — the top-level dir.
	// The entire subtree already exists. handleEvent must recurse into it.
	w.handleEvent(fsnotify.Event{
		Name: filepath.Join(root, "theme"),
		Op:   fsnotify.Create,
	})

	// Check that ALL files ended up in pending (including nested ones)
	w.mu.Lock()
	pending := make(map[string]bool)
	for path := range w.pending {
		pending[path] = true
	}
	w.mu.Unlock()

	for _, path := range []string{
		"theme/example.iapresenter/info.json",
		"theme/example.iapresenter/text.md",
		"theme/example.iapresenter/assets/logo.png",
		"theme/example.iapresenter/assets/icon.png",
	} {
		if !pending[path] {
			t.Errorf("file %q not in pending — nested dir scan missed it", path)
		}
	}
}

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
