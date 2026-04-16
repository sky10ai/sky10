package fs

import (
	"context"
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
	store                  *Store
	localLog               *opslog.LocalOpsLog
	outbox                 *SyncLog[OutboxEntry]
	localDir               string
	stagingDir             string
	ignore                 func(string) bool
	logger                 *slog.Logger
	notify                 chan struct{}
	onEvent                func(string, map[string]any)
	driveName              string
	pokeOutbox             func() // wake the outbox worker after sweep re-queues files
	maxConcurrentDownloads int
}

const defaultReconcilerDownloadLimit = 4

// NewReconciler creates a reconciler that applies remote changes locally.
func NewReconciler(store *Store, localLog *opslog.LocalOpsLog, outbox *SyncLog[OutboxEntry], localDir string, ignore func(string) bool, logger *slog.Logger) *Reconciler {
	logger = defaultLogger(logger)
	return &Reconciler{
		store:                  store,
		localLog:               localLog,
		outbox:                 outbox,
		localDir:               localDir,
		ignore:                 ignore,
		logger:                 logger,
		notify:                 make(chan struct{}, 1),
		onEvent:                func(string, map[string]any) {},
		maxConcurrentDownloads: defaultReconcilerDownloadLimit,
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
	r.reconcileUntilSettled(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler stopped")
			return
		case <-r.notify:
			r.reconcileUntilSettled(ctx)
		}
	}
}

func (r *Reconciler) reconcileUntilSettled(ctx context.Context) {
	r.reconcile(ctx)
	for {
		select {
		case <-r.notify:
			r.reconcile(ctx)
		default:
			return
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
	pathIssues := activeSnapshotPathIssues(snap)
	localFiles, localSymlinks, err := ScanDirectory(r.localDir, r.ignore)
	if err != nil {
		r.logger.Warn("reconcile: scan failed", "error", err)
		return
	}

	r.logger.Info("reconcile: start", "snapshot_files", len(snapshotFiles), "local_files", len(localFiles), "snapshot_dirs", len(snapshotDirs))
	if len(pathIssues) > 0 {
		for _, issue := range pathIssues {
			r.logger.Warn("reconcile: path policy issue", "kind", issue.Kind, "paths", strings.Join(issue.Paths, ", "), "reason", issue.Reason)
		}
	}

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
	pending := 0
	for path, fi := range snapshotFiles {
		if pathIssueBlocksPath(path, pathIssues) {
			skipped++
			continue
		}
		if pendingDeletes[path] {
			skipped++
			continue
		}
		if r.underPendingDeleteDir(path, pendingDeleteDirs) {
			skipped++
			continue
		}
		// Empty files (size=0, chunks=0) are valid — create them on disk.
		// Only skip entries with size>0 but no chunks (impossible with
		// upload-then-record, but defensive).
		if len(fi.Chunks) == 0 && fi.LinkTarget == "" && fi.Size > 0 {
			pending++
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
		total := len(targets)
		r.logger.Info("reconcile: downloading", "files", total)
		r.onEvent("download.start", map[string]any{
			"drive": r.driveName,
			"total": total,
		})
		limit := r.maxConcurrentDownloads
		if limit <= 0 {
			limit = 1
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, limit)
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
					count := int(dlCount.Add(1))
					if count%10 == 0 || count == total {
						r.onEvent("download.progress", map[string]any{
							"drive": r.driveName,
							"path":  path,
							"done":  count,
							"total": total,
						})
					}
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
		if pathIssueBlocksPath(path, pathIssues) {
			continue
		}
		if deletedPaths[path] {
			r.deleteFile(path)
			deleted++
			active = true
		}
	}

	// Create directories from snapshot's dirs set (from create_dir ops).
	if r.createDirectories(snapshotDirs, pathIssues) {
		active = true
	}

	// Directories on disk with no files/dirs in snapshot → remove.
	// Driven by delete_dir ops propagated through the CRDT.
	if r.reconcileDirectories(snapshotFiles, snapshotDirs, pathIssues) {
		active = true
	}

	r.logger.Info("reconcile: done", "downloaded", downloaded, "deleted", deleted, "failed", failed, "skipped", skipped, "pending", pending)

	// Emit sync.complete whenever work was attempted (active) OR when
	// download.start was emitted. Without the latter, the UI stays stuck
	// at "downloading N files" when all downloads fail or are skipped.
	if active || len(targets) > 0 {
		r.onEvent("sync.complete", map[string]any{
			"drive":      r.driveName,
			"downloaded": downloaded,
			"deleted":    deleted,
			"failed":     failed,
		})
	}
	if active {
		r.onEvent("state.changed", nil)
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

func (r *Reconciler) effectiveStagingDir() string {
	if r.stagingDir != "" {
		return r.stagingDir
	}
	return filepath.Join(os.TempDir(), "sky10", "reconcile")
}

func (r *Reconciler) localPath(path string) (string, error) {
	return LogicalPathToLocal(r.localDir, path)
}

func (r *Reconciler) downloadFile(ctx context.Context, path string, fi opslog.FileInfo) bool {
	localPath, err := r.localPath(path)
	if err != nil {
		r.logger.Warn("reconcile: invalid local path", "path", path, "error", err)
		return false
	}

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

	// Empty file: create it directly, no download needed.
	if len(fi.Chunks) == 0 && fi.Size == 0 {
		dir := filepath.Dir(localPath)
		os.MkdirAll(dir, 0755)
		if err := os.WriteFile(localPath, nil, 0644); err != nil {
			r.logger.Warn("reconcile: create empty file failed", "path", path, "error", err)
			return false
		}
		r.logger.Info("reconcile: created empty file", "path", path)
		return true
	}

	// No chunks but size>0 — shouldn't happen with upload-then-record.
	if len(fi.Chunks) == 0 {
		r.logger.Warn("reconcile: no chunks, skipping", "path", path)
		return false
	}

	r.onEvent("sync.active", nil)

	// Download to temp dir first, then atomic rename.
	// Prevents the watcher from seeing a 0-byte intermediate file.
	tmpFile, tmpPath, err := createStagingTempFile(r.effectiveStagingDir(), "dl-*")
	if err != nil {
		r.logger.Warn("reconcile: create temp failed", "path", path, "error", err)
		return false
	}
	session, err := newTransferSession(transferSessionsDirFromStaging(r.effectiveStagingDir()), "download", tmpPath, localPath)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		r.logger.Warn("reconcile: create transfer session failed", "path", path, "error", err)
		return false
	}
	if err := session.updateProgress(0, fi.Size, ""); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		session.remove()
		r.logger.Warn("reconcile: initialize transfer session failed", "path", path, "error", err)
		return false
	}
	reuse, err := newLocalFileChunkReuse(localPath, fi.Chunks)
	if err != nil {
		r.logger.Warn("reconcile: local file reuse unavailable", "path", path, "error", err)
	}
	if reuse != nil {
		defer reuse.Close()
	}

	// No wall-clock timeout — each chunk has its own 30s idle/stall
	// detection via transfer.Reader in downloadChunks. As long as bytes
	// are flowing, the download runs forever.
	progress := func(kind chunkSourceKind, bytesDone int64) {
		if err := session.updateProgress(bytesDone, fi.Size, normalizeReadSource(kind)); err != nil {
			r.logger.Warn("reconcile: update transfer progress failed", "path", path, "error", err)
		}
	}
	dlErr := r.store.getChunksWithReuseAndProgress(ctx, fi.Chunks, fi.Namespace, tmpFile, reuse, progress)
	if dlErr != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		session.remove()
		r.logger.Warn("reconcile: download failed", "path", path, "error", dlErr)
		return false
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		session.remove()
		r.logger.Warn("reconcile: close temp failed", "path", path, "error", err)
		return false
	}
	if err := session.markStaged(); err != nil {
		os.Remove(tmpPath)
		session.remove()
		r.logger.Warn("reconcile: mark staged failed", "path", path, "error", err)
		return false
	}

	if err := publishStagedFile(tmpPath, localPath); err != nil {
		r.logger.Warn("reconcile: publish failed", "path", path, "error", err)
		return false
	}
	if err := session.remove(); err != nil {
		r.logger.Warn("reconcile: remove transfer session failed", "path", path, "error", err)
	}

	r.logger.Info("reconcile: downloaded", "path", path)
	return true
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

	localPath, err := r.localPath(path)
	if err != nil {
		r.logger.Warn("reconcile: invalid symlink path", "path", path, "error", err)
		return false
	}
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
	localPath, err := r.localPath(path)
	if err != nil {
		r.logger.Warn("reconcile: invalid delete path", "path", path, "error", err)
		return
	}
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
//
// Re-checks the current snapshot before each creation: a delete_dir may
// have been appended by the watcher since reconciliation started, and
// creating the dir would trigger a watcher create_dir that poisons the
// CRDT (later create beats earlier delete → directory keeps coming back).
func (r *Reconciler) createDirectories(snapshotDirs map[string]opslog.DirInfo, pathIssues []pathPolicyIssue) bool {
	created := false
	for dir := range snapshotDirs {
		if pathIssueBlocksPath(dir, pathIssues) {
			continue
		}
		// Guard against stale snapshot: re-check that the dir is still
		// in the current snapshot before creating it on disk.
		currentSnap, _ := r.localLog.Snapshot()
		if currentSnap != nil {
			if _, ok := currentSnap.Dirs()[dir]; !ok {
				continue
			}
		}
		localPath, err := r.localPath(dir)
		if err != nil {
			r.logger.Warn("reconcile: invalid directory path", "path", dir, "error", err)
			continue
		}
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
func (r *Reconciler) reconcileDirectories(snapshotFiles map[string]opslog.FileInfo, snapshotDirs map[string]opslog.DirInfo, pathIssues []pathPolicyIssue) bool {
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
		rel, err := LocalPathToLogical(r.localDir, path)
		if err != nil {
			return filepath.SkipDir
		}
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
		if pathIssueBlocksPath(dir, pathIssues) {
			continue
		}
		localPath, err := r.localPath(dir)
		if err != nil {
			r.logger.Warn("reconcile: invalid stale dir path", "path", dir, "error", err)
			continue
		}
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
