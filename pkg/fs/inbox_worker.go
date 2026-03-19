package fs

import (
	"context"
	"io"
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
	w.logger.Info("inbox worker started")
	// Drain any pending entries from last session
	w.drain(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("inbox worker stopped")
			return
		case <-w.notify:
			w.drain(ctx)
		}
	}
}

func (w *InboxWorker) drain(ctx context.Context) {
	for {
		entries, err := w.inbox.ReadAll()
		if err != nil {
			w.logger.Warn("inbox: read failed", "error", err)
			return
		}
		if len(entries) == 0 {
			return
		}

		w.logger.Info("inbox: draining", "pending", len(entries))
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
			w.logger.Warn("inbox: retrying in 5s", "path", entry.Path, "op", string(entry.Op))
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
		w.logger.Info("inbox: already have", "path", entry.Path)
		return true
	}

	// Download to temp dir first, then atomic rename to final path.
	// This prevents the watcher from seeing a 0-byte intermediate file
	// and re-uploading it to S3 before the download finishes.
	tmpDir := filepath.Join(os.TempDir(), "sky10", "inbox")
	os.MkdirAll(tmpDir, 0700)
	tmpFile, err := os.CreateTemp(tmpDir, "dl-*")
	if err != nil {
		w.logger.Warn("inbox: create temp failed", "path", entry.Path, "error", err)
		return false
	}
	tmpPath := tmpFile.Name()

	// Direct chunk download — requires chunk hashes from the op.
	// Old inbox entries without chunks are skipped (they'd trigger
	// loadCurrentState which reads ALL ops from S3 and kills the daemon).
	if len(entry.Chunks) == 0 {
		w.logger.Warn("inbox: skipping entry with no chunks (stale entry)", "path", entry.Path)
		return true // remove from inbox
	}
	w.logger.Debug("inbox: direct chunk download", "path", entry.Path, "chunks", len(entry.Chunks))
	dlErr := w.store.GetChunks(ctx, entry.Chunks, entry.Namespace, tmpFile)
	if dlErr != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		w.logger.Warn("inbox: download failed", "path", entry.Path, "error", dlErr)
		return false
	}
	tmpFile.Close()

	// Atomic move to final location
	dir := filepath.Dir(localPath)
	os.MkdirAll(dir, 0755)
	if err := os.Rename(tmpPath, localPath); err != nil {
		// Cross-device rename fallback: copy + delete
		w.logger.Debug("inbox: rename failed, copying", "error", err)
		if copyErr := copyFile(tmpPath, localPath); copyErr != nil {
			w.logger.Warn("inbox: copy failed", "path", entry.Path, "error", copyErr)
			os.Remove(tmpPath)
			return false
		}
		os.Remove(tmpPath)
	}

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

// copyFile copies src to dst for cross-device moves.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
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
