package fs

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// InboxWorker drains the inbox log — applies remote changes locally.
// Reads from the persistent inbox file. If download fails, entries
// stay in the inbox and get retried.
type InboxWorker struct {
	store    *Store
	inbox    *SyncLog[InboxEntry]
	state    *DriveState
	localDir string
	logger   *slog.Logger
	notify   chan struct{}
	onEvent  func(string)
}

// NewInboxWorker creates an inbox worker.
func NewInboxWorker(store *Store, inbox *SyncLog[InboxEntry], state *DriveState, localDir string, logger *slog.Logger) *InboxWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &InboxWorker{
		store:    store,
		inbox:    inbox,
		state:    state,
		localDir: localDir,
		logger:   logger,
		notify:   make(chan struct{}, 1),
		onEvent:  func(string) {},
	}
}

// Poke tells the worker there are new entries to process.
func (w *InboxWorker) Poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// Run processes inbox entries until context is cancelled.
func (w *InboxWorker) Run(ctx context.Context) {
	// Drain any pending entries from last session
	w.drain(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.notify:
			w.drain(ctx)
		}
	}
}

func (w *InboxWorker) drain(ctx context.Context) {
	for {
		entries, err := w.inbox.ReadAll()
		if err != nil || len(entries) == 0 {
			return
		}

		entry := entries[0]
		w.onEvent("sync.active")

		var ok bool
		switch entry.Op {
		case OpPut:
			ok = w.downloadFile(ctx, entry)
		case OpDelete:
			ok = w.deleteLocal(entry)
		}

		if ok {
			w.inbox.Remove(func(e InboxEntry) bool {
				return e.Path == entry.Path && e.Timestamp == entry.Timestamp
			})
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (w *InboxWorker) downloadFile(ctx context.Context, entry InboxEntry) bool {
	localPath := filepath.Join(w.localDir, filepath.FromSlash(entry.Path))

	// Don't download empty remote files over non-empty local files
	emptyHash := "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"
	if entry.Checksum == emptyHash {
		if info, err := os.Stat(localPath); err == nil && info.Size() > 0 {
			w.logger.Info("inbox: skipping empty remote over non-empty local", "path", entry.Path)
			return true
		}
	}

	// Check if we already have this version
	if existing, err := fileChecksum(localPath); err == nil && existing == entry.Checksum {
		return true // already have it
	}

	dir := filepath.Dir(localPath)
	os.MkdirAll(dir, 0755)

	f, err := os.Create(localPath)
	if err != nil {
		w.logger.Warn("inbox: create failed", "path", entry.Path, "error", err)
		return false
	}

	if err := w.store.Get(ctx, entry.Path, f); err != nil {
		f.Close()
		os.Remove(localPath)
		w.logger.Warn("inbox: download failed", "path", entry.Path, "error", err)
		return false
	}
	f.Close()

	// Update state
	cksum, _ := fileChecksum(localPath)
	w.state.SetFile(entry.Path, FileState{
		Checksum:  cksum,
		Namespace: entry.Namespace,
	})
	w.state.Save()

	w.logger.Info("inbox: downloaded", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}

func (w *InboxWorker) deleteLocal(entry InboxEntry) bool {
	localPath := filepath.Join(w.localDir, filepath.FromSlash(entry.Path))
	os.Remove(localPath)

	w.state.RemoveFile(entry.Path)
	w.state.Save()

	w.logger.Info("inbox: deleted locally", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}
