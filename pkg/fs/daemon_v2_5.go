package fs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// DaemonV2_5 is the inbox/outbox sync daemon. All components communicate
// through persistent JSONL logs on disk. No goroutine blocks another.
//
// Goroutines:
//   - watcherLoop: kqueue → WatcherHandler → outbox.jsonl
//   - outboxWorker.Run: outbox.jsonl → S3
//   - inboxWorker.Run: inbox.jsonl → filesystem
//   - pollerV2.Run: S3 → inbox.jsonl
type DaemonV2_5 struct {
	watcher        *Watcher
	watcherHandler *WatcherHandler
	outboxWorker   *OutboxWorker
	inboxWorker    *InboxWorker
	poller         *PollerV2
	state          *DriveState
	config         DaemonConfig
	logger         *slog.Logger
	onEvent        func(string)
}

// NewDaemonV2_5 creates the inbox/outbox daemon.
func NewDaemonV2_5(store *Store, config DaemonConfig, logger *slog.Logger) (*DaemonV2_5, error) {
	if config.LocalRoot == "" {
		return nil, fmt.Errorf("LocalRoot is required")
	}
	if config.PollSeconds <= 0 {
		config.PollSeconds = 30
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Determine paths
	driveDir := driveDataDir(config.DriveID)
	if config.ManifestPath != "" {
		driveDir = filepath.Dir(config.ManifestPath)
	}
	os.MkdirAll(driveDir, 0700)

	outboxPath := filepath.Join(driveDir, "outbox.jsonl")
	inboxPath := filepath.Join(driveDir, "inbox.jsonl")
	statePath := filepath.Join(driveDir, "state.json")

	// Load state
	state := LoadDriveStateFromPath(statePath)

	// Create logs
	outbox := NewSyncLog[OutboxEntry](outboxPath)
	inbox := NewSyncLog[InboxEntry](inboxPath)

	// Namespace
	ns := ""
	if len(config.Namespaces) > 0 {
		ns = config.Namespaces[0]
	}

	// Watcher
	watcher, err := NewWatcher(config.LocalRoot, config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	// Watcher handler
	watcherHandler := NewWatcherHandler(outbox, state, config.LocalRoot, ns, logger)

	// Outbox worker
	outboxWorker := NewOutboxWorker(store, outbox, state, logger)

	// Inbox worker
	inboxWorker := NewInboxWorker(store, inbox, state, config.LocalRoot, logger)

	// Poller
	pollInterval := time.Duration(config.PollSeconds) * time.Second
	poller := NewPollerV2(store, inbox, state, pollInterval, ns, logger)

	// Wire poke callbacks
	watcherHandler.pokeOutbox = outboxWorker.Poke
	poller.pokeInbox = inboxWorker.Poke

	d := &DaemonV2_5{
		watcher:        watcher,
		watcherHandler: watcherHandler,
		outboxWorker:   outboxWorker,
		inboxWorker:    inboxWorker,
		poller:         poller,
		state:          state,
		config:         config,
		logger:         logger,
		onEvent:        func(string) {},
	}

	// Wire event callbacks
	watcherHandler.onEvent = d.emitEvent
	outboxWorker.onEvent = d.emitEvent
	inboxWorker.onEvent = d.emitEvent

	return d, nil
}

func (d *DaemonV2_5) emitEvent(event string) {
	d.onEvent(event)
}

// Run starts all goroutines and blocks until context is cancelled.
func (d *DaemonV2_5) Run(ctx context.Context) error {
	d.logger.Info("daemon v2.5 starting", "root", d.config.LocalRoot)

	// Seed state from local filesystem on first run
	d.seedStateFromDisk()

	// Start workers
	go d.outboxWorker.Run(ctx)
	go d.inboxWorker.Run(ctx)
	go d.poller.Run(ctx)
	go d.watcherLoop(ctx)

	// Block until cancelled
	<-ctx.Done()
	d.logger.Info("daemon v2.5 shutting down")
	d.watcher.Close()
	d.state.Save()
	return nil
}

// SyncOnce does a one-shot sync: seed state, poll remote, drain queues.
func (d *DaemonV2_5) SyncOnce(ctx context.Context) {
	d.seedStateFromDisk()
	d.poller.pollOnce(ctx)
	d.inboxWorker.drain(ctx)
	d.outboxWorker.drain(ctx)
}

// seedStateFromDisk populates state from the local filesystem for files
// not yet tracked. This handles first run and daemon restarts where
// files were added while the daemon was off.
func (d *DaemonV2_5) seedStateFromDisk() {
	localFiles, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
	if err != nil {
		d.logger.Warn("seed: scan failed", "error", err)
		return
	}
	d.logger.Info("seed", "local_files", len(localFiles), "state_files", len(d.state.Files))

	var events []FileEvent

	// Files on disk not in state → treat as new.
	// Do NOT update state here — the watcher handler checks state to
	// decide if a file changed. If we set state first, the handler sees
	// a matching checksum and skips the file (never queues for upload).
	// State gets updated by the watcher handler after it queues the outbox entry.
	for path, cksum := range localFiles {
		existing, ok := d.state.GetFile(path)
		if !ok {
			d.logger.Info("seed: new file", "path", path)
			events = append(events, FileEvent{Path: path, Type: FileCreated})
		} else if existing.Checksum != cksum {
			d.logger.Info("seed: modified", "path", path)
			events = append(events, FileEvent{Path: path, Type: FileModified})
		}
	}

	// Files in state not on disk → treat as deleted
	for path := range d.state.Files {
		if _, exists := localFiles[path]; !exists {
			d.logger.Info("seed: deleted", "path", path)
			events = append(events, FileEvent{Path: path, Type: FileDeleted})
		}
	}

	if len(events) > 0 {
		d.logger.Info("seed: events", "count", len(events))
		d.watcherHandler.HandleEvents(events)
	}

	d.state.Save()
}

// watcherLoop reads kqueue events, debounces, sends to handler.
func (d *DaemonV2_5) watcherLoop(ctx context.Context) {
	d.logger.Info("watcher loop started")
	batchTimer := time.NewTimer(300 * time.Millisecond)
	batchTimer.Stop()
	var pending []FileEvent

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("watcher loop stopped")
			return
		case event, ok := <-d.watcher.Events():
			if !ok {
				d.logger.Warn("watcher channel closed")
				return
			}
			d.logger.Info("kqueue", "path", event.Path, "type", event.Type)
			pending = append(pending, event)
			batchTimer.Reset(300 * time.Millisecond)
		case <-batchTimer.C:
			if len(pending) > 0 {
				d.logger.Info("watcher: flushing batch", "events", len(pending))
				d.watcherHandler.HandleEvents(pending)
				pending = nil
			}
		}
	}
}

func driveDataDir(driveID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sky10", "fs", "drives", driveID)
}
