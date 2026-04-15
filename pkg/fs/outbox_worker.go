package fs

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

type outboxProcessResult int

const (
	outboxProcessApplied outboxProcessResult = iota
	outboxProcessRetry
	outboxProcessSuperseded
)

// OutboxWorker drains the outbox log — pushes local changes to S3.
// Reads from the persistent outbox file, not an in-memory channel.
// If S3 fails, entries stay in the outbox and get retried.
type OutboxWorker struct {
	store     *Store
	outbox    *SyncLog[OutboxEntry]
	localLog  *opslog.LocalOpsLog
	logger    *slog.Logger
	notify    chan struct{}                // poked when new entries arrive
	onEvent   func(string, map[string]any) // push events to caller
	heartbeat func()                       // watchdog heartbeat
	driveName string
}

// NewOutboxWorker creates an outbox worker.
func NewOutboxWorker(store *Store, outbox *SyncLog[OutboxEntry], localLog *opslog.LocalOpsLog, logger *slog.Logger) *OutboxWorker {
	logger = defaultLogger(logger)
	return &OutboxWorker{
		store:     store,
		outbox:    outbox,
		localLog:  localLog,
		logger:    logger,
		notify:    make(chan struct{}, 1),
		onEvent:   func(string, map[string]any) {},
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
		total := len(entries)
		entry := entries[0]
		w.logger.Info("outbox: draining", "pending", total)
		w.onEvent("sync.active", nil)
		w.onEvent("upload.start", map[string]any{
			"drive": w.driveName,
			"path":  entry.Path,
			"op":    string(entry.Op),
			"total": total,
		})

		var result outboxProcessResult
		switch entry.Op {
		case OpPut:
			result = w.uploadFile(ctx, entry)
		case OpDelete:
			result = outboxResultFromBool(w.writeDeleteOp(ctx, entry))
		case OpDeleteDir:
			result = outboxResultFromBool(w.writeDeleteDirOp(ctx, entry))
		case OpCreateDir:
			result = outboxResultFromBool(w.writeCreateDirOp(ctx, entry))
		case OpSymlink:
			result = outboxResultFromBool(w.writeSymlinkOp(ctx, entry))
		}

		if result == outboxProcessApplied || result == outboxProcessSuperseded {
			w.outbox.Remove(func(e OutboxEntry) bool {
				return e.Path == entry.Path && e.Timestamp == entry.Timestamp
			})
		}
		if result == outboxProcessApplied {
			w.onEvent("upload.complete", map[string]any{
				"drive": w.driveName,
				"path":  entry.Path,
				"op":    string(entry.Op),
				"total": total,
			})
		}
		if result == outboxProcessRetry {
			w.logger.Warn("outbox: retrying in 5s", "path", entry.Path, "op", string(entry.Op))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func outboxResultFromBool(ok bool) outboxProcessResult {
	if ok {
		return outboxProcessApplied
	}
	return outboxProcessRetry
}

func (w *OutboxWorker) uploadFile(ctx context.Context, entry OutboxEntry) outboxProcessResult {
	currentChecksum, stable, err := stableFileChecksum(entry.LocalPath, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			w.logger.Warn("outbox upload: checksum failed", "path", entry.Path, "error", err)
			return outboxProcessRetry
		}
		return w.recordDeleteForMissingFile(entry)
	}
	if !stable {
		w.logger.Warn("outbox upload: file changed while checksumming; retrying", "path", entry.Path)
		return outboxProcessRetry
	}
	if entry.Checksum != "" && currentChecksum != entry.Checksum {
		if !w.refreshPutEntry(entry, currentChecksum) {
			return outboxProcessRetry
		}
		return outboxProcessSuperseded
	}

	f, err := os.Open(entry.LocalPath)
	if err != nil {
		if !os.IsNotExist(err) {
			w.logger.Warn("outbox upload: open failed", "path", entry.Path, "error", err)
			return outboxProcessRetry
		}
		return w.recordDeleteForMissingFile(entry)
	}
	defer f.Close()

	w.logger.Info("outbox: uploading", "path", entry.Path)

	// Set prev checksum from local log — avoids loadCurrentState (88+ S3 GETs)
	if prev, ok := w.localLog.Lookup(entry.Path); ok {
		w.store.SetPrevChecksum(prev.Checksum)
	}
	if err := w.store.Put(ctx, entry.Path, f); err != nil {
		w.logger.Warn("outbox upload failed", "path", entry.Path, "error", err)
		return outboxProcessRetry
	}

	// Upload-then-record: write the entry to the local log only AFTER the
	// blob upload succeeds. Use the outbox entry's timestamp to preserve
	// correct LWW ordering (event time, not upload completion time).
	result := w.store.LastPutResult()
	if result == nil {
		w.logger.Warn("outbox upload: missing put result", "path", entry.Path)
		return outboxProcessRetry
	}
	if entry.Checksum != "" && result.Checksum != entry.Checksum {
		w.logger.Warn("outbox upload: file changed during upload; retrying with fresh state",
			"path", entry.Path,
			"queued_checksum", entry.Checksum,
			"uploaded_checksum", result.Checksum,
		)
		return outboxProcessRetry
	}
	w.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.Put,
		Path:      entry.Path,
		Chunks:    result.Chunks,
		Size:      result.Size,
		Checksum:  result.Checksum,
		Namespace: entry.Namespace,
		Timestamp: entry.Timestamp,
	})

	w.logger.Info("outbox: uploaded", "path", entry.Path)
	w.onEvent("state.changed", nil)
	return outboxProcessApplied
}

func (w *OutboxWorker) recordDeleteForMissingFile(entry OutboxEntry) outboxProcessResult {
	// File gone — deleted before upload finished. Append a delete to
	// the local log so the stale put is superseded. Without this, the
	// watcher's dedup check matches the old checksum when the file
	// reappears, and the blob never gets uploaded.
	w.logger.Warn("outbox upload: file gone, recording delete", "path", entry.Path)
	w.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.Delete,
		Path:      entry.Path,
		Namespace: entry.Namespace,
	})
	return outboxProcessApplied
}

func (w *OutboxWorker) refreshPutEntry(entry OutboxEntry, checksum string) bool {
	refreshed := entry
	refreshed.Checksum = checksum
	refreshed.Timestamp = time.Now().Unix()
	if err := w.outbox.Append(refreshed); err != nil {
		w.logger.Warn("outbox upload: refresh failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox upload: queued content changed; refreshing put entry",
		"path", entry.Path,
		"old_checksum", entry.Checksum,
		"new_checksum", checksum,
	)
	w.onEvent("state.changed", nil)
	return true
}

// writeCreateDirOp records a create_dir in the local log.
func (w *OutboxWorker) writeCreateDirOp(ctx context.Context, entry OutboxEntry) bool {
	if err := w.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.CreateDir,
		Path:      entry.Path,
		Namespace: entry.Namespace,
		Timestamp: entry.Timestamp,
	}); err != nil {
		w.logger.Warn("outbox create_dir: write failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: create_dir", "path", entry.Path)
	w.onEvent("state.changed", nil)
	return true
}

// writeDeleteDirOp records a delete_dir in the local log.
func (w *OutboxWorker) writeDeleteDirOp(ctx context.Context, entry OutboxEntry) bool {
	if err := w.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.DeleteDir,
		Path:      entry.Path,
		Namespace: entry.Namespace,
		Timestamp: entry.Timestamp,
	}); err != nil {
		w.logger.Warn("outbox delete_dir: write failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: delete_dir", "path", entry.Path)
	w.onEvent("state.changed", nil)
	return true
}

// writeSymlinkOp records a symlink in the local log.
func (w *OutboxWorker) writeSymlinkOp(ctx context.Context, entry OutboxEntry) bool {
	if err := w.localLog.AppendLocal(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       entry.Path,
		Checksum:   entry.Checksum,
		LinkTarget: entry.LinkTarget,
		Namespace:  entry.Namespace,
		Timestamp:  entry.Timestamp,
	}); err != nil {
		w.logger.Warn("outbox symlink: write failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: symlink", "path", entry.Path, "target", entry.LinkTarget)
	w.onEvent("state.changed", nil)
	return true
}

// writeDeleteOp records a delete in the local log.
func (w *OutboxWorker) writeDeleteOp(ctx context.Context, entry OutboxEntry) bool {
	if err := w.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.Delete,
		Path:      entry.Path,
		Namespace: entry.Namespace,
		Timestamp: entry.Timestamp,
	}); err != nil {
		w.logger.Warn("outbox delete: write failed", "path", entry.Path, "error", err)
		return false
	}
	w.logger.Info("outbox: deleted", "path", entry.Path)
	w.onEvent("state.changed", nil)
	return true
}
