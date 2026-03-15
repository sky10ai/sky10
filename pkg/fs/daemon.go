package fs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DaemonConfig configures the sync daemon.
type DaemonConfig struct {
	SyncConfig
	PollSeconds int // remote poll interval in seconds (default 30)
}

// Daemon runs continuous bidirectional sync: local file watcher +
// remote S3 poller + sync engine.
type Daemon struct {
	engine  *SyncEngine
	watcher *Watcher
	poller  *Poller
	config  DaemonConfig
	logger  *slog.Logger
}

// NewDaemon creates a sync daemon.
func NewDaemon(store *Store, index *Index, config DaemonConfig, logger *slog.Logger) (*Daemon, error) {
	if config.LocalRoot == "" {
		return nil, fmt.Errorf("LocalRoot is required")
	}
	if config.PollSeconds <= 0 {
		config.PollSeconds = 30
	}
	if logger == nil {
		logger = slog.Default()
	}

	engine := NewSyncEngine(store, config.SyncConfig)

	watcher, err := NewWatcher(config.LocalRoot, config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	pollInterval := time.Duration(config.PollSeconds) * time.Second
	poller := NewPoller(store, index, pollInterval)

	return &Daemon{
		engine:  engine,
		watcher: watcher,
		poller:  poller,
		config:  config,
		logger:  logger,
	}, nil
}

// Run starts the daemon and blocks until the context is cancelled.
// It performs an initial full sync, then watches for local and remote changes.
func (d *Daemon) Run(ctx context.Context) error {
	// 1. Initial sync
	d.logger.Info("starting initial sync", "root", d.config.LocalRoot)
	result, err := d.engine.SyncOnce(ctx)
	if err != nil {
		return fmt.Errorf("initial sync: %w", err)
	}
	d.logger.Info("initial sync complete",
		"uploaded", result.Uploaded,
		"downloaded", result.Downloaded,
		"errors", len(result.Errors))

	// 2. Start remote poller in background
	go d.poller.Start(ctx, func(ops []Op) {
		d.logger.Info("remote changes detected", "ops", len(ops))
		d.syncRemoteChanges(ctx, ops)
	})

	// 3. Watch local changes
	d.logger.Info("watching for changes", "poll_interval", d.config.PollSeconds)

	batchTimer := time.NewTimer(2 * time.Second)
	batchTimer.Stop()
	var pendingLocal []FileEvent

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("shutting down")
			d.watcher.Close()
			if len(pendingLocal) > 0 {
				d.syncLocalChanges(context.Background(), pendingLocal)
			}
			return nil

		case event, ok := <-d.watcher.Events():
			if !ok {
				return nil
			}
			pendingLocal = append(pendingLocal, event)
			batchTimer.Reset(2 * time.Second)

		case <-batchTimer.C:
			if len(pendingLocal) > 0 {
				d.syncLocalChanges(ctx, pendingLocal)
				pendingLocal = nil
			}
		}
	}
}

func (d *Daemon) syncLocalChanges(ctx context.Context, events []FileEvent) {
	seen := make(map[string]bool)
	uploaded := 0
	deleted := 0

	for _, e := range events {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true

		switch e.Type {
		case FileCreated, FileModified:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(e.Path))
			f, err := os.Open(localPath)
			if err != nil {
				d.logger.Warn("open failed", "path", e.Path, "error", err)
				continue
			}
			if err := d.engine.store.Put(ctx, e.Path, f); err != nil {
				f.Close()
				d.logger.Warn("upload failed", "path", e.Path, "error", err)
				continue
			}
			f.Close()

			if cksum, err := fileChecksum(localPath); err == nil {
				d.engine.synced[e.Path] = cksum
			}
			uploaded++

		case FileDeleted:
			if err := d.engine.store.Remove(ctx, e.Path); err != nil {
				d.logger.Warn("delete failed", "path", e.Path, "error", err)
				continue
			}
			delete(d.engine.synced, e.Path)
			deleted++
		}
	}

	if uploaded > 0 || deleted > 0 {
		d.logger.Info("local changes synced", "uploaded", uploaded, "deleted", deleted)
	}
}

func (d *Daemon) syncRemoteChanges(ctx context.Context, ops []Op) {
	downloaded := 0
	deleted := 0

	for _, op := range ops {
		if op.Device == d.engine.store.deviceID {
			continue
		}

		switch op.Type {
		case OpPut:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			dir := filepath.Dir(localPath)
			os.MkdirAll(dir, 0755)

			f, err := os.Create(localPath)
			if err != nil {
				d.logger.Warn("create failed", "path", op.Path, "error", err)
				continue
			}
			if err := d.engine.store.Get(ctx, op.Path, f); err != nil {
				f.Close()
				os.Remove(localPath)
				d.logger.Warn("download failed", "path", op.Path, "error", err)
				continue
			}
			f.Close()

			if cksum, err := fileChecksum(localPath); err == nil {
				d.engine.synced[op.Path] = cksum
			}
			downloaded++

		case OpDelete:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			os.Remove(localPath)
			delete(d.engine.synced, op.Path)
			deleted++
		}
	}

	if downloaded > 0 || deleted > 0 {
		d.logger.Info("remote changes applied", "downloaded", downloaded, "deleted", deleted)
	}
}
