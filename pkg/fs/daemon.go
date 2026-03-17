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
	engine     *SyncEngine
	watcher    *Watcher
	poller     *Poller
	config     DaemonConfig
	logger     *slog.Logger
	onActivity func() // called when sync I/O happens

	// work queue — watcher feeds events, worker goroutine processes them
	localWork chan []FileEvent
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
		engine:     engine,
		watcher:    watcher,
		poller:     poller,
		config:     config,
		logger:     logger,
		onActivity: func() {},
		localWork:  make(chan []FileEvent, 50),
	}, nil
}

// Run starts the daemon and blocks until the context is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	// 1. Initial sync in background — don't block the watcher
	go func() {
		d.logger.Info("starting initial sync", "root", d.config.LocalRoot)
		d.onActivity()
		result, err := d.engine.SyncOnce(ctx)
		if err != nil {
			d.logger.Warn("initial sync failed", "error", err)
			return
		}
		d.logger.Info("initial sync complete",
			"uploaded", result.Uploaded,
			"downloaded", result.Downloaded,
			"errors", len(result.Errors))
	}()

	// 2. Start remote poller in background
	go d.poller.Start(ctx, func(ops []Op) {
		d.logger.Info("remote changes detected", "ops", len(ops))
		d.syncRemoteChanges(ctx, ops)
	})

	// 3. Start upload worker — processes local changes without blocking the watcher
	go d.uploadWorker(ctx)

	// 4. Watch local changes — this loop NEVER blocks on S3
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
				d.drainLocal(pendingLocal)
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
				// Send to worker — non-blocking (buffered channel)
				select {
				case d.localWork <- pendingLocal:
				default:
					d.logger.Warn("upload queue full, dropping batch", "events", len(pendingLocal))
				}
				pendingLocal = nil
			}
		}
	}
}

// uploadWorker processes local file changes in a dedicated goroutine.
func (d *Daemon) uploadWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case events := <-d.localWork:
			d.syncLocalChanges(ctx, events)
		}
	}
}

// drainLocal sends remaining events synchronously on shutdown.
func (d *Daemon) drainLocal(events []FileEvent) {
	d.syncLocalChanges(context.Background(), events)
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
			d.onActivity()
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
				d.engine.state.LocalChecksums[e.Path] = cksum
			}
			uploaded++

		case FileDeleted:
			if err := d.engine.store.Remove(ctx, e.Path); err != nil {
				d.logger.Warn("delete failed", "path", e.Path, "error", err)
				continue
			}
			delete(d.engine.state.LocalChecksums, e.Path)
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
			d.onActivity()
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
				d.engine.state.LocalChecksums[op.Path] = cksum
			}
			downloaded++

		case OpDelete:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			os.Remove(localPath)
			delete(d.engine.state.LocalChecksums, op.Path)
			deleted++
		}
	}

	if downloaded > 0 || deleted > 0 {
		d.logger.Info("remote changes applied", "downloaded", downloaded, "deleted", deleted)
	}
}
