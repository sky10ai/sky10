package fs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	outbox   *SyncLog[OutboxEntry]
	localDir string
	ignore   func(string) bool
	logger   *slog.Logger
	notify   chan struct{}
	onEvent  func(string)
}

// NewReconciler creates a reconciler that applies remote changes locally.
func NewReconciler(store *Store, localLog *opslog.LocalOpsLog, outbox *SyncLog[OutboxEntry], localDir string, ignore func(string) bool, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		store:    store,
		localLog: localLog,
		outbox:   outbox,
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
	snapshotDirs := snap.Dirs()
	localFiles, localSymlinks, err := ScanDirectory(r.localDir, r.ignore)
	if err != nil {
		r.logger.Warn("reconcile: scan failed", "error", err)
		return
	}

	r.logger.Info("reconcile: start", "snapshot_files", len(snapshotFiles), "local_files", len(localFiles), "snapshot_dirs", len(snapshotDirs))

	active := false
	failed := 0
	downloaded := 0
	deleted := 0
	skipped := 0

	// Build set of paths with pending deletes in the outbox.
	// The watcher already decided these should be deleted — don't
	// re-download them just because old remote puts are in the log.
	pendingDeletes := make(map[string]bool)
	var pendingDeleteDirs []string
	if entries, err := r.outbox.ReadAll(); err == nil {
		for _, e := range entries {
			switch e.Op {
			case OpDelete:
				pendingDeletes[e.Path] = true
			case OpDeleteDir:
				pendingDeleteDirs = append(pendingDeleteDirs, e.Path+"/")
			}
		}
	}

	// Collect files and symlinks that need syncing.
	type dlTarget struct {
		path string
		fi   opslog.FileInfo
	}
	var targets []dlTarget
	var symlinkTargets []dlTarget
	for path, fi := range snapshotFiles {
		if pendingDeletes[path] {
			skipped++
			continue
		}
		if r.underPendingDeleteDir(path, pendingDeleteDirs) {
			skipped++
			continue
		}
		localChecksum, onDisk := localFiles[path]
		if !onDisk || !checksumMatch(localChecksum, fi) {
			if fi.LinkTarget != "" {
				symlinkTargets = append(symlinkTargets, dlTarget{path, fi})
			} else {
				targets = append(targets, dlTarget{path, fi})
			}
		}
	}

	// Create/update symlinks (fast — no S3 I/O).
	for _, t := range symlinkTargets {
		if ctx.Err() != nil {
			break
		}
		if r.createSymlink(t.path, t.fi.LinkTarget, localSymlinks[t.path]) {
			downloaded++
			active = true
		} else {
			failed++
		}
	}

	// Download regular files in parallel (up to 4 at a time).
	if len(targets) > 0 {
		r.logger.Info("reconcile: downloading", "files", len(targets))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		var dlCount, failCount atomic.Int32

		for _, t := range targets {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(path string, fi opslog.FileInfo) {
				defer wg.Done()
				defer func() { <-sem }()
				if r.downloadFile(ctx, path, fi) {
					dlCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}(t.path, t.fi)
		}
		wg.Wait()

		downloaded += int(dlCount.Load())
		failed += int(failCount.Load())
		active = active || downloaded > 0
	}

	// Files on disk that were explicitly deleted by a remote op → delete.
	// Only delete when the CRDT has a delete/delete_dir op for the path.
	// Files not in the snapshot but without a delete op are untracked
	// local files (watcher hasn't processed them yet) — leave them alone.
	deletedPaths := snap.DeletedFiles()
	for path := range localFiles {
		if ctx.Err() != nil {
			return
		}
		if deletedPaths[path] {
			r.deleteFile(path)
			deleted++
			active = true
		}
	}

	// Create directories from snapshot's dirs set (from create_dir ops).
	if r.createDirectories(snapshotDirs) {
		active = true
	}

	// Directories on disk with no files/dirs in snapshot → remove.
	// Driven by delete_dir ops propagated through the CRDT.
	if r.reconcileDirectories(snapshotFiles, snapshotDirs) {
		active = true
	}

	r.logger.Info("reconcile: done", "downloaded", downloaded, "deleted", deleted, "failed", failed, "skipped", skipped)

	if active {
		r.onEvent("state.changed")
	}

	// If any downloads failed, schedule a retry. Short delay avoids
	// hammering S3 on persistent errors but recovers quickly from
	// transient failures.
	if failed > 0 {
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				r.Poke()
			}
		}()
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

	// No wall-clock timeout — each chunk has its own 30s idle/stall
	// detection via transfer.Reader in downloadChunks. As long as bytes
	// are flowing, the download runs forever.
	dlErr := r.store.GetChunks(ctx, fi.Chunks, fi.Namespace, tmpFile)
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

// createSymlink creates or updates a symlink on disk. Returns true on success.
// localTarget is the current local symlink target (empty if not a symlink).
func (r *Reconciler) createSymlink(path, target, localTarget string) bool {
	if localTarget == target {
		return false // already correct
	}

	localPath := filepath.Join(r.localDir, filepath.FromSlash(path))
	dir := filepath.Dir(localPath)
	os.MkdirAll(dir, 0755)

	// Remove whatever is at the path (regular file, wrong symlink, etc.)
	os.Remove(localPath)

	if err := os.Symlink(target, localPath); err != nil {
		r.logger.Warn("reconcile: symlink failed", "path", path, "target", target, "error", err)
		return false
	}
	r.logger.Info("reconcile: symlink", "path", path, "target", target)
	return true
}

func (r *Reconciler) deleteFile(path string) {
	localPath := filepath.Join(r.localDir, filepath.FromSlash(path))
	os.Remove(localPath)
	r.logger.Info("reconcile: deleted", "path", path)
}

// underPendingDeleteDir returns true if path falls under any pending
// delete_dir prefix in the outbox.
func (r *Reconciler) underPendingDeleteDir(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// createDirectories creates directories from the snapshot's dirs set
// that don't exist on disk. Returns true if any were created.
func (r *Reconciler) createDirectories(snapshotDirs map[string]opslog.DirInfo) bool {
	created := false
	for dir := range snapshotDirs {
		localPath := filepath.Join(r.localDir, filepath.FromSlash(dir))
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			os.MkdirAll(localPath, 0755)
			r.logger.Info("reconcile: created dir", "path", dir)
			created = true
		}
	}
	return created
}

// reconcileDirectories removes directories that have no files or explicit
// dir entries in the snapshot. Returns true if any directories were removed.
func (r *Reconciler) reconcileDirectories(snapshotFiles map[string]opslog.FileInfo, snapshotDirs map[string]opslog.DirInfo) bool {
	// Build set of directory paths that should exist:
	// 1. Directories implied by files
	// 2. Explicitly created directories (from create_dir ops)
	liveDirs := make(map[string]bool)

	// Dirs implied by files
	for p := range snapshotFiles {
		dir := p
		for {
			i := strings.LastIndex(dir, "/")
			if i < 0 {
				break
			}
			dir = dir[:i]
			if liveDirs[dir] {
				break // ancestors already marked
			}
			liveDirs[dir] = true
		}
	}

	// Explicitly created dirs + their parents
	for dir := range snapshotDirs {
		liveDirs[dir] = true
		d := dir
		for {
			i := strings.LastIndex(d, "/")
			if i < 0 {
				break
			}
			d = d[:i]
			if liveDirs[d] {
				break
			}
			liveDirs[d] = true
		}
	}

	// Walk local dir for subdirectories.
	var stale []string
	filepath.WalkDir(r.localDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == r.localDir {
			return nil
		}
		rel, _ := filepath.Rel(r.localDir, path)
		rel = filepath.ToSlash(rel)
		if r.ignore != nil && r.ignore(rel) {
			return filepath.SkipDir
		}
		if !liveDirs[rel] {
			stale = append(stale, rel)
		}
		return nil
	})

	if len(stale) == 0 {
		return false
	}

	// Sort deepest first so children are removed before parents.
	sort.Slice(stale, func(i, j int) bool {
		return len(stale[i]) > len(stale[j])
	})

	removed := false
	for _, dir := range stale {
		localPath := filepath.Join(r.localDir, filepath.FromSlash(dir))
		// RemoveAll is the nuclear option — it deletes the directory and
		// everything inside it. This is necessary because macOS Finder
		// creates .DS_Store files in every directory it touches. These
		// files are never synced (DefaultIgnorePatterns), so the CRDT
		// doesn't know about them. A simple os.Remove fails on non-empty
		// directories, leaving stale dirs behind after every delete_dir.
		//
		// Known risk: if a user creates a new file and the watcher hasn't
		// picked it up yet (~500ms debounce), RemoveAll will delete it.
		// The previous os.Remove was safe against this because it refused
		// to remove non-empty dirs. We accept this tradeoff because the
		// window is small and the alternative (directories that never get
		// cleaned up) is worse. A future fix could scan for non-ignored
		// files before removing and fall back to os.Remove if any exist.
		if err := os.RemoveAll(localPath); err == nil {
			r.logger.Info("reconcile: removed dir", "path", dir)
			removed = true
		}
	}
	return removed
}
