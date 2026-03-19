package fs

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// OutboxWorker drains the outbox log — pushes local changes to S3.
// Reads from the persistent outbox file, not an in-memory channel.
// If S3 fails, entries stay in the outbox and get retried.
type OutboxWorker struct {
	store   *Store
	outbox  *SyncLog[OutboxEntry]
	state   *DriveState
	logger  *slog.Logger
	notify  chan struct{} // poked when new entries arrive
	onEvent func(string)  // push events to Cirrus
}

// NewOutboxWorker creates an outbox worker.
func NewOutboxWorker(store *Store, outbox *SyncLog[OutboxEntry], state *DriveState, logger *slog.Logger) *OutboxWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxWorker{
		store:   store,
		outbox:  outbox,
		state:   state,
		logger:  logger,
		notify:  make(chan struct{}, 1),
		onEvent: func(string) {},
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

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("outbox worker stopped")
			return
		case <-w.notify:
			w.drain(ctx)
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

		w.logger.Info("outbox: draining", "pending", len(entries))
		entry := entries[0]
		w.onEvent("sync.active")

		var ok bool
		switch entry.Op {
		case OpPut:
			ok = w.uploadFile(ctx, entry)
		case OpDelete:
			ok = w.writeDeleteOp(ctx, entry)
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

	// Set prev checksum from local state — avoids loadCurrentState (88+ S3 GETs)
	if prev, ok := w.state.GetFile(entry.Path); ok {
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

// writeDeleteOp writes a delete op directly to S3 without calling
// store.Remove() (which does a full state load from S3).
func (w *OutboxWorker) writeDeleteOp(ctx context.Context, entry OutboxEntry) bool {
	opsKey, err := w.store.opsKey(ctx)
	if err != nil {
		w.logger.Warn("outbox delete: ops key failed", "error", err)
		return false
	}

	op := &Op{
		Type:         OpDelete,
		Path:         entry.Path,
		PrevChecksum: entry.Checksum,
		Namespace:    entry.Namespace,
		Device:       w.store.deviceID,
		Timestamp:    time.Now().Unix(),
		Seq:          1,
	}

	if err := WriteOp(ctx, w.store.backend, op, opsKey); err != nil {
		w.logger.Warn("outbox delete: write op failed", "path", entry.Path, "error", err)
		return false
	}

	w.logger.Info("outbox: deleted", "path", entry.Path)
	w.onEvent("state.changed")
	return true
}
