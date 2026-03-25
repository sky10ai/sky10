package fs

import (
	"log/slog"
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
	SymlinkCreated
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

	// Use Lstat so symlinks are not followed.
	info, lstatErr := os.Lstat(event.Name)

	// Watch new directories and emit events for any files already inside.
	// Uses addRecursive to catch nested subdirs, and walks the full tree
	// to find files that landed before the watch was registered.
	// Only real directories — symlinks to directories are treated as symlinks.
	if event.Has(fsnotify.Create) {
		if lstatErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			w.addRecursive(event.Name)
			// Emit DirCreated so the handler can track it
			select {
			case w.events <- FileEvent{Path: rel, Type: DirCreated}:
			default:
			}
			// Walk the entire subtree for files created before watches were set
			filepath.WalkDir(event.Name, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				childRel, _ := filepath.Rel(w.root, path)
				childRel = filepath.ToSlash(childRel)
				if w.ignore != nil && w.ignore(childRel) {
					return nil
				}
				w.mu.Lock()
				w.pending[childRel] = time.Now()
				w.mu.Unlock()
				return nil
			})
			return
		}
	}

	// Skip real directory events (not symlinks to directories)
	if lstatErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
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
		fi, err := os.Lstat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				eventType = FileDeleted
			} else {
				continue // transient error, retry next tick
			}
		} else if fi.Mode()&os.ModeSymlink != 0 {
			eventType = SymlinkCreated
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
			// Root must be accessible; non-root errors (dangling symlinks,
			// permission denied, TOCTOU races) are skipped so one bad
			// entry doesn't prevent the watcher from starting.
			if path == root {
				return err
			}
			slog.Warn("watcher: skipping path", "path", path, "error", err)
			return nil
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
		// kqueue stats directory contents when adding a watch.
		// If the directory contains dangling symlinks, Add fails
		// with ENOENT. Skip non-root failures so one bad symlink
		// doesn't prevent the watcher from starting. Return nil
		// (not SkipDir) so children are still visited — they may
		// be watchable even if the parent isn't.
		if err := w.watcher.Add(path); err != nil {
			if path == root {
				return err
			}
			slog.Warn("watcher: cannot watch directory", "path", path, "error", err)
			return nil
		}
		return nil
	})
}
