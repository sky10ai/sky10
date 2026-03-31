package kv

import (
	"bytes"
	"context"
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

// Uploader serializes the local KV snapshot, encrypts it, and uploads
// to S3. Triggered by local state changes (Set/Delete).
type Uploader struct {
	backend  adapter.Backend
	localLog *LocalLog
	deviceID string
	nsID     string
	encKey   []byte
	logger   *slog.Logger
	notify   chan struct{}
	onEvent  func(string, map[string]any)
}

// NewUploader creates a KV snapshot uploader.
func NewUploader(
	backend adapter.Backend,
	localLog *LocalLog,
	deviceID, nsID string,
	encKey []byte,
	logger *slog.Logger,
) *Uploader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Uploader{
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
func (u *Uploader) Poke() {
	select {
	case u.notify <- struct{}{}:
	default:
	}
}

// Run listens for pokes, debounces, and uploads. Blocks until ctx done.
func (u *Uploader) Run(ctx context.Context) {
	u.logger.Info("kv uploader started")
	for {
		select {
		case <-ctx.Done():
			u.logger.Info("kv uploader stopped")
			return
		case <-u.notify:
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
				u.logger.Warn("kv snapshot upload failed", "error", err)
			}
		}
	}
}

// Upload serializes the current snapshot and uploads to S3.
func (u *Uploader) Upload(ctx context.Context) error {
	snap, err := u.localLog.Snapshot()
	if err != nil {
		return err
	}

	data, err := MarshalSnapshot(snap)
	if err != nil {
		return err
	}

	encrypted, err := encrypt(data, u.encKey)
	if err != nil {
		return err
	}

	ts := time.Now().Unix()

	latestKey := snapshotLatestKey(u.nsID, u.deviceID)
	if err := u.backend.Put(ctx, latestKey, bytes.NewReader(encrypted), int64(len(encrypted))); err != nil {
		return err
	}

	histKey := snapshotHistoryKey(u.nsID, u.deviceID, ts)
	if err := u.backend.Put(ctx, histKey, bytes.NewReader(encrypted), int64(len(encrypted))); err != nil {
		u.logger.Warn("kv snapshot history upload failed", "error", err)
	}

	u.logger.Info("kv snapshot uploaded", "keys", snap.Len(), "key", latestKey)
	u.onEvent("kv.snapshot.uploaded", map[string]any{"keys": snap.Len()})
	return nil
}
