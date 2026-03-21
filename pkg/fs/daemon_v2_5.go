package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// DaemonV2_5 is the sync daemon. Local ops log is the single source of
// truth. The reconciler applies remote changes by diffing the CRDT
// snapshot against the local filesystem.
//
// Goroutines:
//   - watcherLoop: kqueue → WatcherHandler → ops.jsonl + outbox.jsonl
//   - outboxWorker.Run: outbox.jsonl → S3
//   - reconciler.Run: snapshot vs filesystem → download/delete
//   - pollerV2.Run: S3 → ops.jsonl → poke reconciler
type DaemonV2_5 struct {
	watcher        *Watcher
	watcherHandler *WatcherHandler
	outboxWorker   *OutboxWorker
	reconciler     *Reconciler
	poller         *PollerV2
	localLog       *opslog.LocalOpsLog
	outbox         *SyncLog[OutboxEntry]
	config         DaemonConfig
	logger         *slog.Logger
	onEvent        func(string)
}

// NewDaemonV2_5 creates the sync daemon.
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
	opsLogPath := filepath.Join(driveDir, "ops.jsonl")

	// Migrate state.json → ops.jsonl if needed (one-time V2.5→V3 upgrade)
	migrateStateToOpsLog(driveDir, store.deviceID, logger)

	// Create local ops log (single source of truth)
	localLog := opslog.NewLocalOpsLog(opsLogPath, store.deviceID)

	// Create outbox
	outbox := NewSyncLog[OutboxEntry](outboxPath)

	// Namespace
	ns := ""
	if len(config.Namespaces) > 0 {
		ns = config.Namespaces[0]
	}

	// Ignore matcher
	var ignoreFunc func(string) bool
	if config.IgnoreFunc != nil {
		ignoreFunc = config.IgnoreFunc
	}

	// Watcher
	watcher, err := NewWatcher(config.LocalRoot, config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	// Watcher handler
	watcherHandler := NewWatcherHandler(outbox, localLog, config.LocalRoot, ns, logger)

	// Outbox worker
	outboxWorker := NewOutboxWorker(store, outbox, localLog, logger)

	// Reconciler (replaces inbox worker)
	reconciler := NewReconciler(store, localLog, outbox, config.LocalRoot, ignoreFunc, logger)

	// Poller
	pollInterval := time.Duration(config.PollSeconds) * time.Second
	poller := NewPollerV2(store, localLog, pollInterval, ns, logger)

	// Wire poke callbacks
	watcherHandler.pokeOutbox = outboxWorker.Poke
	poller.pokeReconciler = reconciler.Poke

	d := &DaemonV2_5{
		watcher:        watcher,
		watcherHandler: watcherHandler,
		outboxWorker:   outboxWorker,
		reconciler:     reconciler,
		poller:         poller,
		localLog:       localLog,
		outbox:         outbox,
		config:         config,
		logger:         logger,
		onEvent:        func(string) {},
	}

	// Wire event callbacks
	watcherHandler.onEvent = d.emitEvent
	outboxWorker.onEvent = d.emitEvent
	reconciler.onEvent = d.emitEvent

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

	// Watchdog: auto-dump goroutines if any worker is stuck for 2 minutes
	wd := NewWatchdog(d.logger, 2*time.Minute)
	wd.Register("poller")
	wd.Register("outbox")
	d.poller.heartbeat = func() { wd.Heartbeat("poller") }
	d.outboxWorker.heartbeat = func() { wd.Heartbeat("outbox") }

	// Start workers
	go d.outboxWorker.Run(ctx)
	go d.reconciler.Run(ctx)
	go d.poller.Run(ctx)
	go d.watcherLoop(ctx)
	go wd.Run(ctx)

	// Block until cancelled
	<-ctx.Done()
	d.logger.Info("daemon v2.5 shutting down")
	d.watcher.Close()
	return nil
}

// SyncOnce does a one-shot sync: seed, poll remote, reconcile, drain outbox.
func (d *DaemonV2_5) SyncOnce(ctx context.Context) {
	d.seedStateFromDisk()
	d.poller.pollOnce(ctx)
	d.reconciler.reconcile(ctx)
	d.outboxWorker.drain(ctx)
}

// seedStateFromDisk diffs the local filesystem against the CRDT snapshot
// and directly records new/modified/deleted files. No synthetic events —
// appends directly to the local log and outbox.
func (d *DaemonV2_5) seedStateFromDisk() {
	localFiles, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
	if err != nil {
		d.logger.Warn("seed: scan failed", "error", err)
		return
	}

	snap, err := d.localLog.Snapshot()
	if err != nil {
		d.logger.Warn("seed: snapshot failed", "error", err)
		return
	}
	knownFiles := snap.Files()
	d.logger.Info("seed", "local_files", len(localFiles), "known_files", len(knownFiles))

	ns := ""
	if len(d.config.Namespaces) > 0 {
		ns = d.config.Namespaces[0]
	}

	wrote := false

	// Files on disk not in snapshot → new local files → log + outbox.
	// Files with different checksums → modified → log + outbox.
	for path, cksum := range localFiles {
		fi, ok := knownFiles[path]
		if !ok || fi.Checksum != cksum {
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(path))
			info, err := os.Stat(localPath)
			if err != nil {
				continue
			}

			if !ok {
				d.logger.Info("seed: new file", "path", path)
			} else {
				d.logger.Info("seed: modified", "path", path)
			}

			d.localLog.AppendLocal(opslog.Entry{
				Type:      opslog.Put,
				Path:      path,
				Checksum:  cksum,
				Size:      info.Size(),
				Namespace: ns,
			})

			d.outbox.Append(OutboxEntry{
				Op:        OpPut,
				Path:      path,
				Checksum:  cksum,
				Namespace: ns,
				LocalPath: localPath,
				Timestamp: time.Now().Unix(),
			})

			wrote = true
		}
	}

	// Files in snapshot but not on disk: depends on who wrote them.
	// Our device → local delete (user deleted while daemon was off).
	// Other device → pending download (reconciler will fetch it).
	deviceID := d.localLog.DeviceID()
	needDownload := 0
	for path, fi := range knownFiles {
		if _, exists := localFiles[path]; !exists {
			if fi.Device == deviceID {
				d.logger.Info("seed: local delete", "path", path)
				d.localLog.AppendLocal(opslog.Entry{
					Type:      opslog.Delete,
					Path:      path,
					Namespace: fi.Namespace,
				})
				d.outbox.Append(OutboxEntry{
					Op:        OpDelete,
					Path:      path,
					Checksum:  fi.Checksum,
					Namespace: fi.Namespace,
					Timestamp: time.Now().Unix(),
				})
				wrote = true
			} else {
				needDownload++
			}
		}
	}
	if needDownload > 0 {
		d.logger.Info("seed: pending downloads", "count", needDownload)
	}

	if wrote {
		d.outboxWorker.Poke()
	}
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

// migrateStateToOpsLog converts a V2.5 state.json to an ops.jsonl file.
// Only runs once: if ops.jsonl already exists, it's a no-op.
func migrateStateToOpsLog(driveDir, deviceID string, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	opsLogPath := filepath.Join(driveDir, "ops.jsonl")
	statePath := filepath.Join(driveDir, "state.json")

	// Skip if ops.jsonl already exists
	if _, err := os.Stat(opsLogPath); err == nil {
		return
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return // no state.json to migrate
	}

	var state struct {
		LastRemoteOp int64 `json:"last_remote_op"`
		Files        map[string]struct {
			Checksum  string `json:"checksum"`
			Namespace string `json:"namespace"`
		} `json:"files"`
	}
	if json.Unmarshal(data, &state) != nil || len(state.Files) == 0 {
		return
	}

	localLog := opslog.NewLocalOpsLog(opsLogPath, deviceID)
	for path, fs := range state.Files {
		localLog.AppendLocal(opslog.Entry{
			Type:      opslog.Put,
			Path:      path,
			Checksum:  fs.Checksum,
			Namespace: fs.Namespace,
		})
	}

	// LastRemoteOp cursor is not migrated — the poller will re-read
	// from S3 once on first startup and "already have" checks will
	// skip entries that are already in the local log.

	logger.Info("migrated state.json to ops.jsonl", "files", len(state.Files))
}
