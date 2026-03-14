package skyfs

import (
	"os"
	"path/filepath"
	"strings"
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

	// Skip dotfiles
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return
		}
	}

	if w.ignore != nil && w.ignore(rel) {
		return
	}

	// Watch new directories
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.watcher.Add(event.Name)
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

		delete(w.pending, path)

		eventType := FileModified
		fullPath := filepath.Join(w.root, filepath.FromSlash(path))
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			eventType = FileDeleted
		}

		select {
		case w.events <- FileEvent{Path: path, Type: eventType}:
		default:
			// Channel full, drop event (will be caught on next sync)
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
		if strings.HasPrefix(d.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		return w.watcher.Add(path)
	})
}
