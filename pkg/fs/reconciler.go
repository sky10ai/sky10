package fs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// Reconciler applies remote changes to the local filesystem by diffing
// the CRDT snapshot against the local directory. Replaces the InboxWorker.
//
// After the poller appends new remote ops to the local log, it pokes
// the Reconciler. The Reconciler builds the snapshot, scans the
// filesystem, and downloads/deletes files to match.
//
// This naturally compacts intermediate states — if a file was created
// then deleted remotely, the snapshot shows nothing and no download
// happens.
type Reconciler struct {
	store    *Store
	localLog *opslog.LocalOpsLog
	localDir string
	ignore   func(string) bool
	logger   *slog.Logger
	notify   chan struct{}
	onEvent  func(string)
}

// NewReconciler creates a reconciler that applies remote changes locally.
func NewReconciler(store *Store, localLog *opslog.LocalOpsLog, localDir string, ignore func(string) bool, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		store:    store,
		localLog: localLog,
		localDir: localDir,
		ignore:   ignore,
		logger:   logger,
		notify:   make(chan struct{}, 1),
		onEvent:  func(string) {},
	}
}

// Poke tells the reconciler there are new ops to process.
func (r *Reconciler) Poke() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// Run reconciles on startup then waits for pokes until context is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("reconciler started")
	r.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopped")
			return
		case <-r.notify:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	snap, err := r.localLog.Snapshot()
	if err != nil {
		r.logger.Warn("reconcile: snapshot failed", "error", err)
		return
	}

	snapshotFiles := snap.Files()
	localFiles, err := ScanDirectory(r.localDir, r.ignore)
	if err != nil {
		r.logger.Warn("reconcile: scan failed", "error", err)
		return
	}

	active := false

	// Files in snapshot but not on disk (or wrong checksum) → download
	for path, fi := range snapshotFiles {
		if ctx.Err() != nil {
			return
		}
		localChecksum, onDisk := localFiles[path]
		if !onDisk || !checksumMatch(localChecksum, fi) {
			if r.downloadFile(ctx, path, fi) {
				active = true
			}
		}
	}

	// Files on disk but not in snapshot → delete (remote delete).
	// Safe because seed + watcher ensure local files are tracked
	// in the local log before the reconciler runs.
	for path := range localFiles {
		if ctx.Err() != nil {
			return
		}
		if _, inSnapshot := snapshotFiles[path]; !inSnapshot {
			r.deleteFile(path)
			active = true
		}
	}

	if active {
		r.onEvent("state.changed")
	}
}

func (r *Reconciler) downloadFile(ctx context.Context, path string, fi opslog.FileInfo) bool {
	localPath := filepath.Join(r.localDir, filepath.FromSlash(path))

	// Don't download empty remote files over non-empty local files
	emptyHash := "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"
	if fi.Checksum == emptyHash {
		if info, err := os.Stat(localPath); err == nil && info.Size() > 0 {
			r.logger.Info("reconcile: skip empty over non-empty", "path", path)
			return false
		}
	}

	// Double-check: file might already match on disk (scan race)
	if existing, err := fileChecksum(localPath); err == nil && checksumMatch(existing, fi) {
		return false
	}

	// Need chunk hashes for download
	if len(fi.Chunks) == 0 {
		r.logger.Warn("reconcile: no chunks, skipping", "path", path)
		return false
	}

	r.onEvent("sync.active")

	// Download to temp dir first, then atomic rename.
	// Prevents the watcher from seeing a 0-byte intermediate file.
	tmpDir := filepath.Join(os.TempDir(), "sky10", "reconcile")
	os.MkdirAll(tmpDir, 0700)
	tmpFile, err := os.CreateTemp(tmpDir, "dl-*")
	if err != nil {
		r.logger.Warn("reconcile: create temp failed", "path", path, "error", err)
		return false
	}
	tmpPath := tmpFile.Name()

	// Per-file download timeout: 2 minutes. Prevents a hung S3 GET
	// from blocking the reconciler forever.
	dlCtx, dlCancel := context.WithTimeout(ctx, 2*time.Minute)
	dlErr := r.store.GetChunks(dlCtx, fi.Chunks, fi.Namespace, tmpFile)
	dlCancel()
	if dlErr != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		r.logger.Warn("reconcile: download failed", "path", path, "error", dlErr)
		return false
	}
	tmpFile.Close()

	// Atomic move to final location
	dir := filepath.Dir(localPath)
	os.MkdirAll(dir, 0755)
	if err := os.Rename(tmpPath, localPath); err != nil {
		// Cross-device rename fallback
		r.logger.Debug("reconcile: rename failed, copying", "error", err)
		if copyErr := copyFile(tmpPath, localPath); copyErr != nil {
			r.logger.Warn("reconcile: copy failed", "path", path, "error", copyErr)
			os.Remove(tmpPath)
			return false
		}
		os.Remove(tmpPath)
	}

	r.logger.Info("reconcile: downloaded", "path", path)
	return true
}

// copyFile copies src to dst for cross-device moves.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// checksumMatch checks if a content hash matches a FileInfo's checksum,
// handling both the new scheme (content hash) and the old scheme
// (hash-of-chunk-hashes, where chunks[0] == content hash for single-chunk files).
func checksumMatch(contentHash string, fi opslog.FileInfo) bool {
	if fi.Checksum == contentHash {
		return true
	}
	if len(fi.Chunks) == 1 && fi.Chunks[0] == contentHash {
		return true
	}
	return false
}

func (r *Reconciler) deleteFile(path string) {
	localPath := filepath.Join(r.localDir, filepath.FromSlash(path))
	os.Remove(localPath)
	r.logger.Info("reconcile: deleted", "path", path)
}
