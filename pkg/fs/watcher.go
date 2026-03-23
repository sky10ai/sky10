package fs

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileEventType describes the kind of filesystem event.
type FileEventType int

const (
	FileCreated FileEventType = iota
	FileModified
	FileDeleted
	FileRenamed
	DirCreated
)

// FileEvent represents a filesystem change.
type FileEvent struct {
	Path string // relative to watch root
	Type FileEventType
}

// Watcher monitors a directory for filesystem changes with debouncing.
type Watcher struct {
	root    string
	events  chan FileEvent
	ignore  func(string) bool
	watcher *fsnotify.Watcher
	done    chan struct{}

	mu       sync.Mutex
	pending  map[string]time.Time // debounce: path → last event time
	debounce time.Duration
}

// NewWatcher creates a filesystem watcher for the given root directory.
// Events are debounced — rapid changes to the same file are coalesced.
func NewWatcher(root string, ignore func(string) bool) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:     root,
		events:   make(chan FileEvent, 100),
		ignore:   ignore,
		watcher:  fsw,
		done:     make(chan struct{}),
		pending:  make(map[string]time.Time),
		debounce: 500 * time.Millisecond,
	}

	// Add root and all subdirectories
	if err := w.addRecursive(root); err != nil {
		fsw.Close()
		return nil, err
	}

	go w.loop()
	return w, nil
}

// Events returns the channel of debounced filesystem events.
func (w *Watcher) Events() <-chan FileEvent {
	return w.events
}

// Close stops the watcher and closes the events channel.
func (w *Watcher) Close() error {
	close(w.done)
	return w.watcher.Close()
}

func (w *Watcher) loop() {
	defer close(w.events)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case <-w.watcher.Errors:
			// Log and continue — don't crash on transient errors

		case <-ticker.C:
			w.flushPending()
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	rel, err := filepath.Rel(w.root, event.Name)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)

	if w.ignore != nil && w.ignore(rel) {
		return
	}

	// Watch new directories and emit events for any files already inside
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.watcher.Add(event.Name)
			// Emit DirCreated so the handler can track it
			select {
			case w.events <- FileEvent{Path: rel, Type: DirCreated}:
			default:
			}
			// Scan for files that were created before the watch was registered
			entries, _ := os.ReadDir(event.Name)
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				childRel := rel + "/" + e.Name()
				if w.ignore == nil || !w.ignore(childRel) {
					w.mu.Lock()
					w.pending[childRel] = time.Now()
					w.mu.Unlock()
				}
			}
			return
		}
	}

	// Skip directory events
	if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
		return
	}

	w.mu.Lock()
	w.pending[rel] = time.Now()
	w.mu.Unlock()
}

func (w *Watcher) flushPending() {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	for path, lastEvent := range w.pending {
		if now.Sub(lastEvent) < w.debounce {
			continue // still within debounce window
		}

		eventType := FileModified
		fullPath := filepath.Join(w.root, filepath.FromSlash(path))
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			eventType = FileDeleted
		}

		select {
		case w.events <- FileEvent{Path: path, Type: eventType}:
			delete(w.pending, path)
		default:
			// Channel full — leave in pending for next tick
		}
	}
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && w.ignore != nil {
			rel, _ := filepath.Rel(root, path)
			if w.ignore(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
		}
		return w.watcher.Add(path)
	})
}
