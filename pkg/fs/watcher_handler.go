package fs

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// WatcherHandler processes file events from the watcher, writes to
// the outbox, and records ops in the local ops log. No S3, no channels
// to other goroutines — just local disk operations.
type WatcherHandler struct {
	outbox            *SyncLog[OutboxEntry]
	localLog          *opslog.LocalOpsLog
	localDir          string
	namespace         string
	logger            *slog.Logger
	stableWriteWindow time.Duration
	pokeOutbox        func()
	onEvent           func(string, map[string]any)
}

// NewWatcherHandler creates a handler that bridges watcher events to the outbox.
func NewWatcherHandler(outbox *SyncLog[OutboxEntry], localLog *opslog.LocalOpsLog, localDir, namespace string, logger *slog.Logger) *WatcherHandler {
	logger = defaultLogger(logger)
	return &WatcherHandler{
		outbox:     outbox,
		localLog:   localLog,
		localDir:   localDir,
		namespace:  namespace,
		logger:     logger,
		pokeOutbox: func() {},
		onEvent:    func(string, map[string]any) {},
	}
}

// HandleEvents processes a batch of file events from the watcher.
// Records ops in the local log, writes to outbox, pokes the outbox worker.
func (h *WatcherHandler) HandleEvents(events []FileEvent) {
	seen := make(map[string]bool)
	wrote := false

	for _, e := range events {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true

		if isConflictCopyPath(e.Path) && e.Type != FileDeleted {
			h.logger.Debug("watcher: skip conflict artifact", "path", e.Path, "type", e.Type)
			continue
		}

		switch e.Type {
		case FileCreated, FileModified:
			localPath := filepath.Join(h.localDir, filepath.FromSlash(e.Path))
			cksum, stable, err := stableFileChecksum(localPath, h.stableWriteWindow)
			if err != nil {
				h.logger.Warn("watcher: checksum failed", "path", e.Path, "error", err)
				continue
			}
			if !stable {
				h.logger.Debug("watcher: file not stable yet", "path", e.Path, "window", h.stableWriteWindow.String())
				continue
			}

			// Skip if unchanged from local log
			if existing, ok := h.localLog.Lookup(e.Path); ok {
				if existing.Checksum == cksum {
					h.logger.Info("watcher: unchanged", "path", e.Path)
					continue
				}
				// Backwards compat: old S3 ops used hash-of-chunk-hashes as
				// checksum, but fileChecksum computes SHA3-256 of raw content.
				// For single-chunk files, chunks[0] IS the content hash.
				if len(existing.Chunks) == 1 && existing.Chunks[0] == cksum {
					h.logger.Info("watcher: unchanged (chunk match)", "path", e.Path)
					continue
				}
			}

			h.logger.Info("watcher: outbox", "path", e.Path)

			info, _ := os.Stat(localPath)
			if info == nil {
				continue
			}

			// Write to outbox only — local log entry written by
			// OutboxWorker after blob upload succeeds (upload-then-record).
			h.outbox.Append(OutboxEntry{
				Op:        OpPut,
				Path:      e.Path,
				Checksum:  cksum,
				Namespace: h.namespace,
				LocalPath: localPath,
				Timestamp: time.Now().Unix(),
			})
			wrote = true

		case DirCreated:
			// Skip if the directory already exists in the snapshot.
			// This prevents the watcher from emitting create_dir ops for
			// directories created by the reconciler (which would poison
			// the CRDT and override a prior delete_dir).
			if snap, _ := h.localLog.Snapshot(); snap != nil {
				if _, ok := snap.Dirs()[e.Path]; ok {
					h.logger.Info("watcher: skip mkdir (already in snapshot)", "path", e.Path)
					continue
				}
			}
			h.logger.Info("watcher: mkdir", "path", e.Path)
			// Outbox only — local log written by OutboxWorker.
			h.outbox.Append(OutboxEntry{
				Op:        OpCreateDir,
				Path:      e.Path,
				Namespace: h.namespace,
				Timestamp: time.Now().Unix(),
			})
			wrote = true

		case SymlinkCreated:
			localPath := filepath.Join(h.localDir, filepath.FromSlash(e.Path))
			target, err := os.Readlink(localPath)
			if err != nil {
				h.logger.Warn("watcher: readlink failed", "path", e.Path, "error", err)
				continue
			}

			// Skip if unchanged from local log
			if existing, ok := h.localLog.Lookup(e.Path); ok {
				if existing.LinkTarget == target {
					h.logger.Info("watcher: symlink unchanged", "path", e.Path)
					continue
				}
			}

			h.logger.Info("watcher: symlink outbox", "path", e.Path, "target", target)

			cksum := symlinkChecksum(target)

			// Outbox only — local log written by OutboxWorker.
			h.outbox.Append(OutboxEntry{
				Op:         OpSymlink,
				Path:       e.Path,
				Checksum:   cksum,
				LinkTarget: target,
				Namespace:  h.namespace,
				Timestamp:  time.Now().Unix(),
			})
			wrote = true

		case FileDeleted:
			existing, ok := h.localLog.Lookup(e.Path)
			if !ok {
				// Might be a directory — emit deletes for all tracked files under it
				h.HandleDirectoryTrash(e.Path)
				continue
			}

			h.logger.Info("watcher: delete", "path", e.Path)

			// Outbox only — local log written by OutboxWorker.
			h.outbox.Append(OutboxEntry{
				Op:        OpDelete,
				Path:      e.Path,
				Checksum:  existing.Checksum,
				Namespace: existing.Namespace,
				Timestamp: time.Now().Unix(),
			})
			wrote = true
		}
	}

	if wrote {
		h.onEvent("state.changed", nil)
		h.pokeOutbox()
	}
}

// HandleDirectoryTrash emits a single delete_dir op when a watched
// directory disappears. kqueue fires one event for the directory, not
// per-file. The CRDT's buildSnapshot handles the prefix delete.
func (h *WatcherHandler) HandleDirectoryTrash(dirPath string) {
	snap, err := h.localLog.Snapshot()
	if err != nil {
		h.logger.Warn("watcher: snapshot failed for directory trash", "error", err)
		return
	}

	// Check that tracked files or dirs exist under this prefix,
	// or the directory itself is explicitly tracked.
	prefix := dirPath + "/"
	ns := ""
	found := false
	for path, fi := range snap.Files() {
		if strings.HasPrefix(path, prefix) {
			ns = fi.Namespace
			found = true
			break
		}
	}
	if !found {
		// Check if the directory itself or sub-dirs are tracked
		if di, ok := snap.Dirs()[dirPath]; ok {
			ns = di.Namespace
			found = true
		} else {
			for path, di := range snap.Dirs() {
				if strings.HasPrefix(path, prefix) {
					ns = di.Namespace
					found = true
					break
				}
			}
		}
	}
	if !found {
		return
	}

	h.logger.Info("watcher: delete_dir", "path", dirPath)

	// Outbox only — local log written by OutboxWorker.
	h.outbox.Append(OutboxEntry{
		Op:        OpDeleteDir,
		Path:      dirPath,
		Namespace: ns,
		Timestamp: time.Now().Unix(),
	})

	h.onEvent("state.changed", nil)
	h.pokeOutbox()
}
