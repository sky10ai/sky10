package fs

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// SnapshotUploader serializes the local CRDT snapshot, encrypts it, and
// uploads to S3. Triggered by local state changes (outbox drain, seed).
// Writes both a "latest" key (for sync) and a timestamped key (for history).
type SnapshotUploader struct {
	backend  adapter.Backend
	localLog *opslog.LocalOpsLog
	deviceID string
	nsID     string
	encKey   []byte // namespace encryption key
	logger   *slog.Logger
	notify   chan struct{}
	onEvent  func(string, map[string]any)
}

// NewSnapshotUploader creates a snapshot uploader.
func NewSnapshotUploader(
	backend adapter.Backend,
	localLog *opslog.LocalOpsLog,
	deviceID, nsID string,
	encKey []byte,
	logger *slog.Logger,
) *SnapshotUploader {
	logger = defaultLogger(logger)
	return &SnapshotUploader{
		backend:  backend,
		localLog: localLog,
		deviceID: deviceID,
		nsID:     nsID,
		encKey:   encKey,
		logger:   logger,
		notify:   make(chan struct{}, 1),
		onEvent:  func(string, map[string]any) {},
	}
}

// Poke signals that local state changed and a snapshot upload may be needed.
func (u *SnapshotUploader) Poke() {
	select {
	case u.notify <- struct{}{}:
	default:
	}
}

// Run listens for pokes, debounces, and uploads. Blocks until ctx is cancelled.
func (u *SnapshotUploader) Run(ctx context.Context) {
	u.logger.Info("snapshot uploader started")
	for {
		select {
		case <-ctx.Done():
			u.logger.Info("snapshot uploader stopped")
			return
		case <-u.notify:
			// Debounce: wait for activity to settle.
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-u.notify:
				timer.Reset(2 * time.Second)
			case <-timer.C:
			}
			timer.Stop()

			if err := u.Upload(ctx); err != nil {
				u.logger.Warn("snapshot upload failed", "error", err)
			}
		}
	}
}

// Upload serializes the current CRDT snapshot and uploads to S3.
func (u *SnapshotUploader) Upload(ctx context.Context) error {
	snap, err := u.localLog.Snapshot()
	if err != nil {
		return err
	}

	data, err := opslog.MarshalSnapshot(snap)
	if err != nil {
		return err
	}

	encrypted, err := Encrypt(data, u.encKey)
	if err != nil {
		return err
	}

	ts := time.Now().Unix()

	// Upload latest (sync reads this).
	latestKey := snapshotLatestKey(u.nsID, u.deviceID)
	if err := u.backend.Put(ctx, latestKey, bytes.NewReader(encrypted), int64(len(encrypted))); err != nil {
		return err
	}

	// Upload timestamped copy (history).
	histKey := snapshotHistoryKey(u.nsID, u.deviceID, ts)
	if err := u.backend.Put(ctx, histKey, bytes.NewReader(encrypted), int64(len(encrypted))); err != nil {
		u.logger.Warn("snapshot history upload failed", "error", err)
		// Non-fatal — latest was already uploaded.
	}

	u.logger.Info("snapshot uploaded", "files", snap.Len(), "key", latestKey)
	u.onEvent("snapshot.uploaded", map[string]any{"files": snap.Len()})
	return nil
}
