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
	DriveID      string // drive ID for manifest persistence
	ManifestPath string // override manifest path (for tests)
	PollSeconds  int    // remote poll interval in seconds (default 30)
}

// Daemon runs continuous bidirectional sync: local file watcher +
// remote S3 poller + sync engine.
type Daemon struct {
	store      *Store
	manifest   *DriveManifest
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

	watcher, err := NewWatcher(config.LocalRoot, config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	pollInterval := time.Duration(config.PollSeconds) * time.Second
	poller := NewPoller(store, index, pollInterval)

	var manifest *DriveManifest
	if config.ManifestPath != "" {
		manifest = LoadDriveManifestFromPath(config.ManifestPath)
	} else {
		manifest = LoadDriveManifest(config.DriveID)
	}

	return &Daemon{
		store:      store,
		manifest:   manifest,
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
	// 1. Initial three-way sync in background
	go func() {
		d.logger.Info("starting initial sync", "root", d.config.LocalRoot)
		d.onActivity()
		result := d.threeWaySync(ctx)
		d.logger.Info("initial sync complete",
			"uploaded", result.Uploaded,
			"downloaded", result.Downloaded,
			"deleted", result.Deleted,
			"conflicts", result.Conflicts,
			"errors", result.Errors)
	}()

	// 2. Start remote poller in background
	go d.poller.Start(ctx, func(ops []Op) {
		d.logger.Info("remote changes detected", "ops", len(ops))
		d.processRemoteOps(ctx, ops)
	})

	// 3. Start upload worker
	go d.uploadWorker(ctx)

	// 4. Periodic reconciliation — catches deletes the watcher misses
	// (e.g. parent directory removed, Finder trash)
	go d.reconcileLoop(ctx)

	// 5. Watch local changes — never blocks on S3
	d.logger.Info("watching for changes", "poll_interval", d.config.PollSeconds)

	batchTimer := time.NewTimer(300 * time.Millisecond)
	batchTimer.Stop()
	var pendingLocal []FileEvent

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("shutting down")
			d.watcher.Close()
			if len(pendingLocal) > 0 {
				d.processLocalEvents(context.Background(), pendingLocal)
			}
			d.manifest.Save()
			return nil

		case event, ok := <-d.watcher.Events():
			if !ok {
				return nil
			}
			pendingLocal = append(pendingLocal, event)
			batchTimer.Reset(300 * time.Millisecond)

		case <-batchTimer.C:
			if len(pendingLocal) > 0 {
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

// SyncResult contains stats from a sync pass.
type SyncResult struct {
	Uploaded   int
	Downloaded int
	Deleted    int
	Conflicts  int
	Errors     int
}

// SyncOnce performs a single three-way sync pass and returns stats.
func (d *Daemon) SyncOnce(ctx context.Context) SyncResult {
	return d.threeWaySync(ctx)
}

// threeWaySync performs a full three-way diff and executes all actions.
func (d *Daemon) threeWaySync(ctx context.Context) SyncResult {
	var result SyncResult

	// 1. Scan local directory
	localFiles, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
	if err != nil {
		d.logger.Warn("scan failed", "error", err)
		result.Errors++
		return result
	}

	// 2. Fetch remote ops since last sync
	opsKey, err := d.store.opsKey(ctx)
	if err != nil {
		d.logger.Warn("ops key failed", "error", err)
		result.Errors++
		return result
	}

	allOps, err := ReadOps(ctx, d.store.backend, d.manifest.LastRemoteOp, opsKey)
	if err != nil {
		d.logger.Warn("reading ops failed", "error", err)
		result.Errors++
		return result
	}

	// Filter to only ops from other devices and matching namespace
	var remoteOps []Op
	for _, op := range allOps {
		if op.Device == d.store.deviceID {
			continue
		}
		if len(d.config.Namespaces) > 0 {
			matched := false
			for _, ns := range d.config.Namespaces {
				if op.Namespace == ns {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		remoteOps = append(remoteOps, op)
	}

	// 3. Three-way diff
	actions := ThreeWayDiff(localFiles, d.manifest, remoteOps)

	// 5. Execute actions
	for _, action := range actions {
		select {
		case <-ctx.Done():
			return result
		default:
		}

		switch action.Type {
		case ActionUpload:
			if d.executeUpload(ctx, action) {
				result.Uploaded++
			} else {
				result.Errors++
			}
		case ActionDownload:
			if d.executeDownload(ctx, action) {
				result.Downloaded++
			} else {
				result.Errors++
			}
		case ActionDeleteLocal:
			d.executeDeleteLocal(action)
			result.Deleted++
		case ActionDeleteRemote:
			if d.executeDeleteRemote(ctx, action) {
				result.Deleted++
			} else {
				result.Errors++
			}
		case ActionConflict:
			d.resolveConflict(ctx, action)
			result.Conflicts++
		}
	}

	// 6. Update last_remote_op cursor
	maxTs := d.manifest.LastRemoteOp
	for _, op := range allOps {
		if op.Timestamp > maxTs {
			maxTs = op.Timestamp
		}
	}
	d.manifest.SetLastRemoteOp(maxTs)
	d.manifest.Save()

	return result
}

func (d *Daemon) executeUpload(ctx context.Context, action SyncAction) bool {
	d.onActivity()
	localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(action.Path))
	f, err := os.Open(localPath)
	if err != nil {
		d.logger.Warn("open failed", "path", action.Path, "error", err)
		return false
	}
	defer f.Close()

	if err := d.store.Put(ctx, action.Path, f); err != nil {
		d.logger.Warn("upload failed", "path", action.Path, "error", err)
		return false
	}

	// Update manifest
	info, _ := os.Stat(localPath)
	d.manifest.SetFile(action.Path, SyncedFile{
		Checksum: action.LocalSum,
		Size:     info.Size(),
		Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	})
	return true
}

func (d *Daemon) executeDownload(ctx context.Context, action SyncAction) bool {
	d.onActivity()
	localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(action.Path))
	dir := filepath.Dir(localPath)
	os.MkdirAll(dir, 0755)

	f, err := os.Create(localPath)
	if err != nil {
		d.logger.Warn("create failed", "path", action.Path, "error", err)
		return false
	}

	if err := d.store.Get(ctx, action.Path, f); err != nil {
		f.Close()
		os.Remove(localPath)
		d.logger.Warn("download failed", "path", action.Path, "error", err)
		return false
	}
	f.Close()

	// Update manifest
	cksum, _ := fileChecksum(localPath)
	info, _ := os.Stat(localPath)
	size := int64(0)
	mod := ""
	if info != nil {
		size = info.Size()
		mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	d.manifest.SetFile(action.Path, SyncedFile{
		Checksum: cksum,
		Size:     size,
		Modified: mod,
	})
	return true
}

func (d *Daemon) executeDeleteLocal(action SyncAction) {
	localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(action.Path))
	os.Remove(localPath)
	d.manifest.RemoveFile(action.Path)
}

func (d *Daemon) executeDeleteRemote(ctx context.Context, action SyncAction) bool {
	if err := d.store.Remove(ctx, action.Path); err != nil {
		d.logger.Warn("remote delete failed", "path", action.Path, "error", err)
		return false
	}
	d.manifest.RemoveFile(action.Path)
	return true
}

// resolveConflict handles a file modified on both sides.
// Strategy: keep both — rename local to .conflict.<device>, download remote.
func (d *Daemon) resolveConflict(ctx context.Context, action SyncAction) {
	d.logger.Warn("conflict", "path", action.Path, "reason", action.Reason)

	localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(action.Path))

	// Rename local version with conflict suffix
	ext := filepath.Ext(localPath)
	base := localPath[:len(localPath)-len(ext)]
	conflictPath := base + ".conflict." + d.store.deviceID + ext
	if err := os.Rename(localPath, conflictPath); err != nil {
		d.logger.Warn("conflict rename failed", "path", action.Path, "error", err)
		return
	}

	// Upload the conflict copy so other devices see it
	conflictRel := action.Path[:len(action.Path)-len(ext)] + ".conflict." + d.store.deviceID + ext
	cf, err := os.Open(conflictPath)
	if err == nil {
		d.store.Put(ctx, conflictRel, cf)
		cf.Close()
		if cksum, err := fileChecksum(conflictPath); err == nil {
			info, _ := os.Stat(conflictPath)
			d.manifest.SetFile(conflictRel, SyncedFile{
				Checksum: cksum,
				Size:     info.Size(),
				Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
			})
		}
	}

	// Download remote version to original path
	if action.RemoteOp != nil {
		dl := SyncAction{Type: ActionDownload, Path: action.Path, RemoteOp: action.RemoteOp}
		d.executeDownload(ctx, dl)
	}
}

// reconcileLoop periodically scans the local directory and reconciles
// against the manifest. Catches deletes the watcher misses (e.g. when
// a parent directory is trashed in Finder — kqueue doesn't fire events
// for individual files inside a deleted directory).
func (d *Daemon) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.reconcile(ctx)
		}
	}
}

func (d *Daemon) reconcile(ctx context.Context) {
	localFiles, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
	if err != nil {
		return
	}

	changed := false

	// Find files in manifest but not on disk — update manifest immediately
	for path := range d.manifest.Files {
		if _, exists := localFiles[path]; !exists {
			d.manifest.RemoveFile(path)
			changed = true
			p := path
			go func() {
				d.store.Remove(ctx, p)
			}()
		}
	}

	// Find files on disk but not in manifest — update manifest immediately
	for path, cksum := range localFiles {
		if _, exists := d.manifest.GetFile(path); !exists {
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(path))
			info, _ := os.Stat(localPath)
			size := int64(0)
			mod := ""
			if info != nil {
				size = info.Size()
				mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}
			d.manifest.SetFile(path, SyncedFile{Checksum: cksum, Size: size, Modified: mod})
			changed = true
			p := path
			go func() {
				f, err := os.Open(localPath)
				if err != nil {
					return
				}
				defer f.Close()
				d.store.Put(ctx, p, f)
			}()
		}
	}

	if changed {
		d.manifest.Save()
	}
}

// uploadWorker processes local file changes in a dedicated goroutine.
func (d *Daemon) uploadWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case events := <-d.localWork:
			d.processLocalEvents(ctx, events)
		}
	}
}

// processLocalEvents handles watcher events (live file changes).
// Manifest updates FIRST (instant), S3 uploads in background.
func (d *Daemon) processLocalEvents(ctx context.Context, events []FileEvent) {
	seen := make(map[string]bool)
	changed := false

	for _, e := range events {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true

		switch e.Type {
		case FileCreated, FileModified:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(e.Path))
			cksum, err := fileChecksum(localPath)
			if err != nil {
				continue
			}
			info, _ := os.Stat(localPath)
			size := int64(0)
			mod := ""
			if info != nil {
				size = info.Size()
				mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}

			// Update manifest IMMEDIATELY — Cirrus sees it now
			d.manifest.SetFile(e.Path, SyncedFile{Checksum: cksum, Size: size, Modified: mod})
			changed = true

			// S3 upload in background
			path := e.Path
			go func() {
				d.onActivity()
				f, err := os.Open(localPath)
				if err != nil {
					return
				}
				defer f.Close()
				if err := d.store.Put(ctx, path, f); err != nil {
					d.logger.Warn("upload failed", "path", path, "error", err)
				}
			}()

		case FileDeleted:
			// Update manifest IMMEDIATELY
			d.manifest.RemoveFile(e.Path)
			changed = true

			// S3 delete in background
			path := e.Path
			go func() {
				if err := d.store.Remove(ctx, path); err != nil {
					d.logger.Warn("remote delete failed", "path", path, "error", err)
				}
			}()
		}
	}

	if changed {
		d.manifest.Save()
	}
}

// processRemoteOps handles new ops from the poller.
func (d *Daemon) processRemoteOps(ctx context.Context, ops []Op) {
	downloaded := 0
	deleted := 0

	for _, op := range ops {
		if op.Device == d.store.deviceID {
			continue
		}

		switch op.Type {
		case OpPut:
			action := SyncAction{
				Type:     ActionDownload,
				Path:     op.Path,
				RemoteOp: &op,
			}
			if d.executeDownload(ctx, action) {
				downloaded++
			}

		case OpDelete:
			action := SyncAction{
				Type: ActionDeleteLocal,
				Path: op.Path,
			}
			d.executeDeleteLocal(action)
			deleted++
		}

		if op.Timestamp > d.manifest.LastRemoteOp {
			d.manifest.SetLastRemoteOp(op.Timestamp)
		}
	}

	if downloaded > 0 || deleted > 0 {
		d.logger.Info("remote changes applied", "downloaded", downloaded, "deleted", deleted)
		d.manifest.Save()
	}
}
