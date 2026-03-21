package fs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// S3JobType identifies what the S3 worker should do.
type S3JobType int

const (
	S3Upload   S3JobType = iota // upload a local file to S3
	S3Delete                    // delete a file from S3
	S3Download                  // download a file from S3
)

// S3Job is a unit of work for the S3 worker pool.
type S3Job struct {
	Type      S3JobType
	Path      string // remote path
	LocalPath string // local filesystem path (for uploads/downloads)
	Checksum  string // file checksum
	Namespace string // file namespace (for delete ops)
}

// DaemonV2 runs continuous bidirectional sync using channel-based
// architecture. No goroutine ever blocks another. S3 is completely
// isolated from the manifest/UI path.
type DaemonV2 struct {
	store    *Store
	manifest *DriveManifest
	watcher  *Watcher
	config   DaemonConfig
	logger   *slog.Logger

	// Channels — the only way goroutines communicate
	manifestCh chan []FileEvent // watcher → manifest worker
	s3WorkCh   chan S3Job       // manifest worker → S3 workers
	remoteCh   chan []Op        // poller → manifest worker
	activityCh chan struct{}    // S3 workers → activity tracker

	onActivity     func()       // called when sync I/O happens
	onStateChanged func(string) // called with event name when manifest changes
}

// NewDaemonV2 creates a channel-based sync daemon.
func NewDaemonV2(store *Store, config DaemonConfig, logger *slog.Logger) (*DaemonV2, error) {
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

	var manifest *DriveManifest
	if config.ManifestPath != "" {
		manifest = LoadDriveManifestFromPath(config.ManifestPath)
	} else {
		manifest = LoadDriveManifest(config.DriveID)
	}

	return &DaemonV2{
		store:          store,
		manifest:       manifest,
		watcher:        watcher,
		config:         config,
		logger:         logger,
		manifestCh:     make(chan []FileEvent, 100),
		s3WorkCh:       make(chan S3Job, 200),
		remoteCh:       make(chan []Op, 10),
		activityCh:     make(chan struct{}, 1),
		onActivity:     func() {},
		onStateChanged: func(string) {},
	}, nil
}

// Run starts all goroutines and blocks until context is cancelled.
func (d *DaemonV2) Run(ctx context.Context) error {
	d.logger.Info("daemon v2 starting", "root", d.config.LocalRoot)

	// Start goroutines
	go d.watcherLoop(ctx)
	go d.manifestWorker(ctx)
	go d.pollerLoop(ctx)

	// S3 worker pool — 3 workers
	for i := 0; i < 3; i++ {
		go d.s3Worker(ctx, i)
	}

	// Block until cancelled
	<-ctx.Done()
	d.logger.Info("daemon v2 shutting down")
	d.watcher.Close()
	d.manifest.Save()
	return nil
}

// SyncOnce performs a single sync pass (for CLI --once mode).
func (d *DaemonV2) SyncOnce(ctx context.Context) SyncResult {
	return d.fullSync(ctx)
}

// --- Watcher Loop ---
// Reads kqueue events, debounces, sends batches to manifestCh.
// Never touches S3. Never blocks on anything except the channel send.
func (d *DaemonV2) watcherLoop(ctx context.Context) {
	batchTimer := time.NewTimer(300 * time.Millisecond)
	batchTimer.Stop()
	var pending []FileEvent

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-d.watcher.Events():
			if !ok {
				return
			}
			pending = append(pending, event)
			batchTimer.Reset(300 * time.Millisecond)
		case <-batchTimer.C:
			if len(pending) > 0 {
				select {
				case d.manifestCh <- pending:
				default:
					d.logger.Warn("manifest channel full, dropping batch", "events", len(pending))
				}
				pending = nil
			}
		}
	}
}

// --- Manifest Worker ---
// Single goroutine that owns the manifest. Reads from manifestCh
// (local changes) and remoteCh (remote ops). Updates manifest,
// saves to disk, enqueues S3 jobs. Never does S3 calls itself.
func (d *DaemonV2) manifestWorker(ctx context.Context) {
	// Initial sync — reconcile local state + fetch remote ops
	d.fullSync(ctx)

	// Periodic reconciliation timer
	reconcileTicker := time.NewTicker(30 * time.Second)
	defer reconcileTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case events := <-d.manifestCh:
			d.handleLocalEvents(events)

		case ops := <-d.remoteCh:
			d.handleRemoteOps(ctx, ops)

		case <-reconcileTicker.C:
			d.reconcileLocal()
		}
	}
}

// handleLocalEvents processes watcher events. Updates manifest
// immediately, enqueues S3 uploads/deletes.
func (d *DaemonV2) handleLocalEvents(events []FileEvent) {
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

			// Check if actually changed from manifest
			if existing, ok := d.manifest.GetFile(e.Path); ok && existing.Checksum == cksum {
				continue
			}

			d.manifest.SetFile(e.Path, SyncedFile{Checksum: cksum, Size: size, Modified: mod})
			changed = true

			// Enqueue S3 upload (non-blocking)
			select {
			case d.s3WorkCh <- S3Job{Type: S3Upload, Path: e.Path, LocalPath: localPath, Checksum: cksum}:
			default:
				d.logger.Warn("s3 work queue full", "path", e.Path)
			}

		case FileDeleted:
			existing, ok := d.manifest.GetFile(e.Path)
			if !ok {
				continue // not in manifest, nothing to do
			}
			d.manifest.RemoveFile(e.Path)
			changed = true

			ns := ""
			if len(d.config.Namespaces) > 0 {
				ns = d.config.Namespaces[0]
			}
			select {
			case d.s3WorkCh <- S3Job{Type: S3Delete, Path: e.Path, Checksum: existing.Checksum, Namespace: ns}:
			default:
				d.logger.Warn("s3 work queue full", "path", e.Path)
			}
		}
	}

	if changed {
		d.manifest.Save()
		d.onStateChanged("state.changed")
	}
}

// handleRemoteOps processes ops from the poller. Downloads new/modified
// files, deletes locally deleted files.
func (d *DaemonV2) handleRemoteOps(ctx context.Context, ops []Op) {
	changed := false

	for _, op := range ops {
		if op.Device == d.store.deviceID {
			continue
		}

		switch op.Type {
		case OpPut:
			// Skip empty remote files over non-empty local
			emptyHash := "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"
			if existing, ok := d.manifest.GetFile(op.Path); ok {
				if op.Checksum == existing.Checksum {
					continue // already have it
				}
				if op.Size == 0 && op.Checksum == emptyHash && existing.Size > 0 {
					continue // don't wipe
				}
			}

			// Download via S3 worker
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			select {
			case d.s3WorkCh <- S3Job{Type: S3Download, Path: op.Path, LocalPath: localPath, Checksum: op.Checksum}:
			default:
				d.logger.Warn("s3 work queue full for download", "path", op.Path)
			}

		case OpDelete:
			localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			os.Remove(localPath)
			d.manifest.RemoveFile(op.Path)
			changed = true

		case OpDeleteDir:
			prefix := op.Path + "/"
			for path := range d.manifest.Files {
				if strings.HasPrefix(path, prefix) {
					localPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(path))
					os.Remove(localPath)
					d.manifest.RemoveFile(path)
				}
			}
			// Remove the directory itself
			dirPath := filepath.Join(d.config.LocalRoot, filepath.FromSlash(op.Path))
			os.Remove(dirPath)
			changed = true
		}

		if op.Timestamp > d.manifest.LastRemoteOp {
			d.manifest.SetLastRemoteOp(op.Timestamp)
		}
	}

	if changed {
		d.manifest.Save()
		d.onStateChanged("state.changed")
	}
}

// reconcileLocal scans the filesystem and compares against manifest.
// Injects synthetic events for anything the watcher missed.
func (d *DaemonV2) reconcileLocal() {
	localFiles, err := ScanDirectory(d.config.LocalRoot, d.config.IgnoreFunc)
	if err != nil {
		return
	}

	var events []FileEvent

	// Files in manifest but not on disk
	for path := range d.manifest.Files {
		if _, exists := localFiles[path]; !exists {
			events = append(events, FileEvent{Path: path, Type: FileDeleted})
		}
	}

	// Files on disk but not in manifest, or changed
	for path, cksum := range localFiles {
		existing, inManifest := d.manifest.GetFile(path)
		if !inManifest {
			events = append(events, FileEvent{Path: path, Type: FileCreated})
		} else if existing.Checksum != cksum {
			events = append(events, FileEvent{Path: path, Type: FileModified})
		}
	}

	if len(events) > 0 {
		d.handleLocalEvents(events)
	}
}

// fullSync does initial reconciliation + remote op fetch.
func (d *DaemonV2) fullSync(ctx context.Context) SyncResult {
	var result SyncResult

	// 1. Reconcile local state
	d.reconcileLocal()

	// 2. Fetch and process remote ops
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

	// Filter to other devices + matching namespace
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

	if len(remoteOps) > 0 {
		d.handleRemoteOps(ctx, remoteOps)
		result.Downloaded = len(remoteOps)
	}

	// Update cursor
	maxTs := d.manifest.LastRemoteOp
	for _, op := range allOps {
		if op.Timestamp > maxTs {
			maxTs = op.Timestamp
		}
	}
	d.manifest.SetLastRemoteOp(maxTs)
	d.manifest.Save()
	d.onStateChanged("state.changed")

	return result
}

// --- S3 Worker ---
// Reads jobs from s3WorkCh, executes them. If S3 is slow, jobs
// queue up. Nothing else is affected.
func (d *DaemonV2) s3Worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-d.s3WorkCh:
			d.onActivity()
			d.onStateChanged("sync.active")
			d.executeS3Job(ctx, job)
		}
	}
}

func (d *DaemonV2) executeS3Job(ctx context.Context, job S3Job) {
	switch job.Type {
	case S3Upload:
		f, err := os.Open(job.LocalPath)
		if err != nil {
			return
		}
		defer f.Close()
		if err := d.store.Put(ctx, job.Path, f); err != nil {
			d.logger.Warn("s3 upload failed", "path", job.Path, "error", err)
		}

	case S3Delete:
		if err := d.store.Remove(ctx, job.Path); err != nil {
			d.logger.Warn("s3 delete failed", "path", job.Path, "error", err)
		}

	case S3Download:
		dir := filepath.Dir(job.LocalPath)
		os.MkdirAll(dir, 0755)
		f, err := os.Create(job.LocalPath)
		if err != nil {
			d.logger.Warn("create failed", "path", job.Path, "error", err)
			return
		}
		if err := d.store.Get(ctx, job.Path, f); err != nil {
			f.Close()
			os.Remove(job.LocalPath)
			d.logger.Warn("s3 download failed", "path", job.Path, "error", err)
			return
		}
		f.Close()

		// Update manifest after successful download
		cksum, _ := fileChecksum(job.LocalPath)
		info, _ := os.Stat(job.LocalPath)
		size := int64(0)
		mod := ""
		if info != nil {
			size = info.Size()
			mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		d.manifest.SetFile(job.Path, SyncedFile{Checksum: cksum, Size: size, Modified: mod})
		d.manifest.Save()
		d.onStateChanged("state.changed")
	}
}

// --- Poller Loop ---
// Periodically fetches remote ops from S3, sends to remoteCh.
func (d *DaemonV2) pollerLoop(ctx context.Context) {
	// Poll once immediately
	d.pollOnce(ctx)

	ticker := time.NewTicker(time.Duration(d.config.PollSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.pollOnce(ctx)
		}
	}
}

func (d *DaemonV2) pollOnce(ctx context.Context) {
	opsKey, err := d.store.opsKey(ctx)
	if err != nil {
		return
	}

	ops, err := ReadOps(ctx, d.store.backend, d.manifest.LastRemoteOp, opsKey)
	if err != nil {
		return
	}

	if len(ops) > 0 {
		select {
		case d.remoteCh <- ops:
		default:
			d.logger.Warn("remote channel full")
		}
	}
}
