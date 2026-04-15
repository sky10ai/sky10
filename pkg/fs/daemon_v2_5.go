package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// DaemonConfig configures the sync daemon.
type DaemonConfig struct {
	SyncConfig
	DriveID           string        // drive ID for state persistence
	DriveName         string        // human-readable name for progress events
	ManifestPath      string        // override state path (for tests)
	PollSeconds       int           // remote poll interval in seconds (default 30)
	ScanSeconds       int           // local full-scan interval in seconds (default 60)
	ReconcileSeconds  int           // periodic reconcile interval in seconds (default 60)
	StableWriteWindow time.Duration // watcher/periodic scan settle window (default 2s)
}

// DaemonV2_5 is the sync daemon. Local ops log is the single source of
// truth. The reconciler applies remote changes by diffing the CRDT
// snapshot against the local filesystem.
//
// Goroutines:
//   - watcherLoop: kqueue → WatcherHandler → outbox.jsonl
//   - outboxWorker.Run: outbox.jsonl → S3 blobs → ops.jsonl
//   - reconciler.Run: snapshot vs filesystem → download/delete
//   - snapshotPoller.Run: remote snapshots → baseline diff → ops.jsonl
//   - snapshotUploader.Run: ops.jsonl → encrypted snapshot → S3
type DaemonV2_5 struct {
	store            *Store
	watcher          *Watcher
	watcherHandler   *WatcherHandler
	outboxWorker     *OutboxWorker
	reconciler       *Reconciler
	snapshotUploader *SnapshotUploader
	snapshotPoller   *SnapshotPoller
	localLog         *opslog.LocalOpsLog
	outbox           *SyncLog[OutboxEntry]
	config           DaemonConfig
	driveDir         string
	logger           *slog.Logger
	onEvent          func(string, map[string]any)
}

// NewDaemonV2_5 creates the sync daemon.
func NewDaemonV2_5(store *Store, config DaemonConfig, logger *slog.Logger) (*DaemonV2_5, error) {
	if config.LocalRoot == "" {
		return nil, fmt.Errorf("LocalRoot is required")
	}
	if config.PollSeconds <= 0 {
		config.PollSeconds = 30
	}
	if config.ScanSeconds <= 0 {
		config.ScanSeconds = 60
	}
	if config.ReconcileSeconds <= 0 {
		config.ReconcileSeconds = 60
	}
	if config.StableWriteWindow == 0 {
		config.StableWriteWindow = 2 * time.Second
	}
	if config.StableWriteWindow < 0 {
		config.StableWriteWindow = 0
	}
	logger = componentLogger(logger)

	// Determine paths
	driveDir := driveDataDir(config.DriveID)
	if config.ManifestPath != "" {
		driveDir = filepath.Dir(config.ManifestPath)
	}
	os.MkdirAll(driveDir, 0700)
	if err := ensureTransferWorkspace(driveDir); err != nil {
		return nil, fmt.Errorf("creating transfer workspace: %w", err)
	}

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
	watcherHandler.stableWriteWindow = config.StableWriteWindow

	// Outbox worker
	outboxWorker := NewOutboxWorker(store, outbox, localLog, logger)

	// Reconciler
	reconciler := NewReconciler(store, localLog, outbox, config.LocalRoot, ignoreFunc, logger)
	reconciler.stagingDir = transferStagingDir(driveDir)

	// Namespace ID for S3 path scoping and encryption.
	nsForKey := ns
	if nsForKey == "" {
		nsForKey = "default"
	}
	store.SetNamespaceID(nsForKey)

	// Snapshot uploader + poller (created with nil encKey — resolved lazily on first use)
	baselineDir := filepath.Join(driveDir, "baselines")
	pollInterval := time.Duration(config.PollSeconds) * time.Second
	snapshotUploader := NewSnapshotUploader(store.backend, localLog, store.deviceID, nsForKey, nil, logger)
	snapshotPoller := NewSnapshotPoller(store.backend, localLog, store.deviceID, nsForKey, nil, pollInterval, NewBaselineStore(baselineDir), logger)

	// Wire poke callbacks
	watcherHandler.pokeOutbox = outboxWorker.Poke
	reconciler.pokeOutbox = outboxWorker.Poke
	snapshotPoller.pokeReconciler = reconciler.Poke
	snapshotPoller.pokeUploader = snapshotUploader.Poke

	d := &DaemonV2_5{
		store:            store,
		watcher:          watcher,
		watcherHandler:   watcherHandler,
		outboxWorker:     outboxWorker,
		reconciler:       reconciler,
		snapshotUploader: snapshotUploader,
		snapshotPoller:   snapshotPoller,
		localLog:         localLog,
		outbox:           outbox,
		config:           config,
		driveDir:         driveDir,
		logger:           logger,
		onEvent:          func(string, map[string]any) {},
	}

	// Wire event callbacks and drive name
	watcherHandler.onEvent = d.emitEvent
	outboxWorker.onEvent = d.emitEvent
	outboxWorker.driveName = config.DriveName
	reconciler.onEvent = d.emitEvent
	reconciler.driveName = config.DriveName
	snapshotUploader.onEvent = d.emitEvent
	snapshotPoller.onEvent = d.emitEvent
	snapshotPoller.driveName = config.DriveName

	return d, nil
}

func (d *DaemonV2_5) emitEvent(event string, data map[string]any) {
	d.onEvent(event, data)
	// Poke snapshot uploader when local state changes.
	if event == "state.changed" && d.snapshotUploader != nil {
		d.snapshotUploader.Poke()
	}
}

func (d *DaemonV2_5) prepareTransferWorkspace() {
	stats, err := recoverTransferWorkspace(d.driveDir, d.logger)
	if err != nil {
		d.logger.Warn("transfer workspace setup failed", "error", err)
		return
	}
	if stats.Republished > 0 || stats.CleanedSessions > 0 || stats.CleanedStaging > 0 {
		d.logger.Info("transfer workspace recovered",
			"republished", stats.Republished,
			"cleaned_sessions", stats.CleanedSessions,
			"cleaned_staging", stats.CleanedStaging,
			"drive", d.config.DriveName,
		)
	}
}

func (d *DaemonV2_5) localScanLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.config.ScanSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.logger.Debug("periodic local scan", "drive", d.config.DriveName)
			d.seedStateFromDiskWithStableWindow(d.config.StableWriteWindow)
		}
	}
}

func (d *DaemonV2_5) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(d.config.ReconcileSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.logger.Debug("periodic reconcile poke", "drive", d.config.DriveName)
			d.reconciler.Poke()
		}
	}
}

// Run starts all goroutines and blocks until context is cancelled.
func (d *DaemonV2_5) Run(ctx context.Context) error {
	d.logger.Info("daemon starting", "root", d.config.LocalRoot)
	d.prepareTransferWorkspace()

	// Resolve namespace encryption key for snapshot upload/download.
	if err := d.resolveSnapshotKey(ctx); err != nil {
		d.logger.Warn("snapshot key resolution failed — sync will work but no snapshot exchange", "error", err)
	}

	// Startup sequence (order matters — see snapshot-exchange-architecture.md):
	// 1. Seed from disk (diff local filesystem vs LOCAL CRDT — merge base)
	// 2. Poll remote snapshots (baseline diff → merge into CRDT)
	// 3. Reconcile (download new, delete removed)
	// 4. Upload our snapshot
	d.seedStateFromDisk()
	d.snapshotPoller.pollOnce(ctx)
	d.reconciler.reconcile(ctx)
	d.outboxWorker.drain(ctx)
	if err := d.snapshotUploader.Upload(ctx); err != nil {
		d.logger.Warn("initial snapshot upload failed", "error", err)
	}

	// Watchdog
	wd := NewWatchdog(d.logger, 2*time.Minute)
	wd.Register("outbox")
	wd.Register("poller")
	d.outboxWorker.heartbeat = func() { wd.Heartbeat("outbox") }
	d.snapshotPoller.heartbeat = func() { wd.Heartbeat("poller") }

	// Start workers
	go d.outboxWorker.Run(ctx)
	go d.reconciler.Run(ctx)
	go d.snapshotPoller.Run(ctx)
	go d.snapshotUploader.Run(ctx)
	go d.localScanLoop(ctx)
	go d.reconcileLoop(ctx)
	go d.watcherLoop(ctx)
	go wd.Run(ctx)

	<-ctx.Done()
	d.logger.Info("daemon shutting down")
	d.watcher.Close()
	return nil
}

// SyncOnce does a one-shot sync: seed, poll, reconcile, drain, upload.
func (d *DaemonV2_5) SyncOnce(ctx context.Context) {
	d.prepareTransferWorkspace()
	d.resolveSnapshotKey(ctx)
	d.seedStateFromDisk()
	d.snapshotPoller.pollOnce(ctx)
	d.reconciler.reconcile(ctx)
	d.outboxWorker.drain(ctx)
	d.snapshotUploader.Upload(ctx)
}

// resolveSnapshotKey fetches the namespace encryption key and resolves the
// opaque namespace ID. Sets both on the uploader, poller, and store.
func (d *DaemonV2_5) resolveSnapshotKey(ctx context.Context) error {
	nsName := d.snapshotUploader.nsID // initially set to namespace name
	encKey, err := d.store.getOrCreateNamespaceKey(ctx, nsName)
	if err != nil {
		return fmt.Errorf("resolving namespace key %q: %w", nsName, err)
	}

	// Resolve opaque namespace ID (or generate on first use)
	nsID, err := loadCachedNSID(nsName)
	if err != nil {
		nsID, err = resolveNSID(ctx, d.store.backend, nsName, encKey)
		if err != nil {
			return fmt.Errorf("resolving nsID for %q: %w", nsName, err)
		}
		cacheNSID(nsName, nsID)
	}
	d.logger.Info("namespace resolved", "name", nsName, "nsid", nsID)

	d.snapshotUploader.encKey = encKey
	d.snapshotUploader.nsID = nsID
	d.snapshotPoller.encKey = encKey
	d.snapshotPoller.nsID = nsID
	d.store.SetNamespaceID(nsID)
	return nil
}

// seedStateFromDisk diffs the local filesystem against the CRDT snapshot
// and directly records new/modified/deleted files. No synthetic events —
// appends directly to the local log and outbox.
func (d *DaemonV2_5) seedStateFromDisk() {
	d.seedStateFromDiskWithStableWindow(0)
}

func (d *DaemonV2_5) seedStateFromDiskWithStableWindow(stableWindow time.Duration) {
	localFiles, localSymlinks, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
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
	deletedFiles := snap.DeletedFiles()
	d.logger.Info("seed", "local_files", len(localFiles), "known_files", len(knownFiles), "deleted_files", len(deletedFiles))

	ns := ""
	if len(d.config.Namespaces) > 0 {
		ns = d.config.Namespaces[0]
	}

	wrote := false

	// Files on disk not in snapshot → new local files → outbox only.
	// Files with different checksums → modified → outbox only.
	// Upload-then-record: local log entry written by OutboxWorker after
	// blob upload succeeds.
	for path, cksum := range localFiles {
		if deletedFiles[path] {
			continue
		}
		fi, ok := knownFiles[path]
		if !ok || fi.Checksum != cksum {
			// Handle symlinks separately — no file upload needed.
			if target, isSymlink := localSymlinks[path]; isSymlink {
				if ok && fi.LinkTarget == target {
					continue // symlink target unchanged
				}
				d.logger.Info("seed: symlink", "path", path, "target", target)
				d.outbox.Append(OutboxEntry{
					Op:         OpSymlink,
					Path:       path,
					Checksum:   cksum,
					LinkTarget: target,
					Namespace:  ns,
					Timestamp:  time.Now().Unix(),
				})
				wrote = true
				continue
			}

			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(path))
			if _, err := os.Stat(localPath); err != nil {
				continue
			}
			if stableWindow > 0 {
				stableChecksum, stable, err := stableFileChecksum(localPath, stableWindow)
				if err != nil {
					d.logger.Warn("seed: stability check failed", "path", path, "error", err)
					continue
				}
				if !stable {
					d.logger.Debug("seed: file not stable yet", "path", path, "window", stableWindow.String())
					continue
				}
				cksum = stableChecksum
				if ok && fi.Checksum == cksum {
					continue
				}
			}

			if !ok {
				d.logger.Info("seed: new file", "path", path)
			} else {
				d.logger.Info("seed: modified", "path", path)
			}

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

	// Files in CRDT but not on disk → local delete. The local CRDT is
	// the merge base: if a file was known and is now gone from disk,
	// the user deleted it while the daemon was off. No device attribution
	// needed — the snapshot poller will merge remote state AFTER seed.
	for path, fi := range knownFiles {
		if _, exists := localFiles[path]; !exists {
			d.logger.Info("seed: local delete", "path", path)
			d.localLog.AppendLocal(opslog.Entry{
				Type:      opslog.Delete,
				Path:      path,
				Namespace: fi.Namespace,
			})
			wrote = true
		}
	}

	// Empty directories on disk not in snapshot → outbox only.
	knownDirs := snap.Dirs()
	emptyDirs := ScanEmptyDirectories(d.config.LocalRoot, d.config.IgnoreFunc)
	for _, dir := range emptyDirs {
		if _, ok := knownDirs[dir]; !ok {
			d.logger.Info("seed: new dir", "path", dir)
			d.outbox.Append(OutboxEntry{
				Op:        OpCreateDir,
				Path:      dir,
				Namespace: ns,
				Timestamp: time.Now().Unix(),
			})
			wrote = true
		}
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
	dir, err := config.DrivesDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, driveID)
}

// migrateStateToOpsLog converts a V2.5 state.json to an ops.jsonl file.
// Only runs once: if ops.jsonl already exists, it's a no-op.
func migrateStateToOpsLog(driveDir, deviceID string, logger *slog.Logger) {
	logger = defaultLogger(logger)
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
