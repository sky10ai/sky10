package fs

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// OutboxWorker drains the outbox log — pushes local changes to S3.
// Reads from the persistent outbox file, not an in-memory channel.
// If S3 fails, entries stay in the outbox and get retried.
type OutboxWorker struct {
	store     *Store
	outbox    *SyncLog[OutboxEntry]
	localLog  *opslog.LocalOpsLog
	logger    *slog.Logger
	notify    chan struct{} // poked when new entries arrive
	onEvent   func(string)  // push events to Cirrus
	heartbeat func()        // watchdog heartbeat
}

// NewOutboxWorker creates an outbox worker.
func NewOutboxWorker(store *Store, outbox *SyncLog[OutboxEntry], localLog *opslog.LocalOpsLog, logger *slog.Logger) *OutboxWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxWorker{
		store:     store,
		outbox:    outbox,
		localLog:  localLog,
		logger:    logger,
		notify:    make(chan struct{}, 1),
		onEvent:   func(string) {},
		heartbeat: func() {},
	}
}

// Poke tells the worker there are new entries to process.
func (w *OutboxWorker) Poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// Run processes outbox entries until context is cancelled.
// Drains on startup (crash recovery), then waits for pokes.
func (w *OutboxWorker) Run(ctx context.Context) {
	w.logger.Info("outbox worker started")
	// Drain any pending entries from last session
	w.drain(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		w.heartbeat()
		select {
		case <-ctx.Done():
			w.logger.Info("outbox worker stopped")
			return
		case <-w.notify:
			w.drain(ctx)
		case <-ticker.C:
			// Periodic heartbeat so watchdog knows we're alive while idle
		}
	}
}

func (w *OutboxWorker) drain(ctx context.Context) {
	for {
		entries, err := w.outbox.ReadAll()
		if err != nil {
			w.logger.Warn("outbox: read failed", "error", err)
			return
		}
		if len(entries) == 0 {
			return
		}

		w.heartbeat()
		w.logger.Info("outbox: draining", "pending", len(entries))
		entry := entries[0]
		w.onEvent("sync.active")

		var ok bool
		switch entry.Op {
		case OpPut:
			ok = w.uploadFile(ctx, entry)
		case OpDelete:
			ok = w.writeDeleteOp(ctx, entry)
		case OpDeleteDir:
			ok = w.writeDeleteDirOp(ctx, entry)
		case OpCreateDir:
			ok = w.writeCreateDirOp(ctx, entry)
		case OpSymlink:
			ok = w.writeSymlinkOp(ctx, entry)
		}

		if ok {
			w.outbox.Remove(func(e OutboxEntry) bool {
				return e.Path == entry.Path && e.Timestamp == entry.Timestamp
			})
		} else {
			w.logger.Warn("outbox: retrying in 5s", "path", entry.Path, "op", string(entry.Op))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (w *OutboxWorker) uploadFile(ctx context.Context, entry OutboxEntry) bool {
	f, err := os.Open(entry.LocalPath)
	if err != nil {
		// File gone — probably deleted before upload finished. Remove from outbox.
		w.logger.Warn("outbox upload: file gone", "path", entry.Path)
		return true
	}
	defer f.Close()

	w.logger.Info("outbox: uploading", "path", entry.Path)

	// Set prev checksum from local log — avoids loadCurrentState (88+ S3 GETs)
	if prev, ok := w.localLog.Lookup(entry.Path); ok {
		w.store.SetPrevChecksum(prev.Checksum)
	}
	if err := w.store.Put(ctx, entry.Path, f); err != nil {
		w.logger.Warn("outbox upload failed", "path", entry.Path, "error", err)
		return false
	}

	w.logger.Info("outbox: uploaded", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}

// writeCreateDirOp writes a create_dir op via the store's OpsLog.
func (w *OutboxWorker) writeCreateDirOp(ctx context.Context, entry OutboxEntry) bool {
	op := &Op{
		Type:      OpCreateDir,
		Path:      entry.Path,
		Namespace: entry.Namespace,
	}
	if err := w.store.writeOp(ctx, op); err != nil {
		w.logger.Warn("outbox create_dir: write op failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: create_dir", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}

// writeDeleteDirOp writes a delete_dir op via the store's OpsLog.
func (w *OutboxWorker) writeDeleteDirOp(ctx context.Context, entry OutboxEntry) bool {
	op := &Op{
		Type:      OpDeleteDir,
		Path:      entry.Path,
		Namespace: entry.Namespace,
	}
	if err := w.store.writeOp(ctx, op); err != nil {
		w.logger.Warn("outbox delete_dir: write op failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: delete_dir", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}

// writeSymlinkOp writes a symlink op via the store's OpsLog.
// No file upload — symlinks are metadata-only ops.
func (w *OutboxWorker) writeSymlinkOp(ctx context.Context, entry OutboxEntry) bool {
	op := &Op{
		Type:       OpSymlink,
		Path:       entry.Path,
		Checksum:   entry.Checksum,
		LinkTarget: entry.LinkTarget,
		Namespace:  entry.Namespace,
	}
	if err := w.store.writeOp(ctx, op); err != nil {
		w.logger.Warn("outbox symlink: write op failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: symlink", "path", entry.Path, "target", entry.LinkTarget)
	w.onEvent("state.changed")
	return true
}

// writeDeleteOp writes a delete op via the store's OpsLog.
func (w *OutboxWorker) writeDeleteOp(ctx context.Context, entry OutboxEntry) bool {
	op := &Op{
		Type:         OpDelete,
		Path:         entry.Path,
		PrevChecksum: entry.Checksum,
		Namespace:    entry.Namespace,
	}

	if err := w.store.writeOp(ctx, op); err != nil {
		w.logger.Warn("outbox delete: write op failed", "path", entry.Path, "error", err)
		return false
	}

	w.logger.Info("outbox: deleted", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}
