package fs

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// WatcherHandler processes file events from the watcher, writes to
// the outbox, and records ops in the local ops log. No S3, no channels
// to other goroutines — just local disk operations.
type WatcherHandler struct {
	outbox     *SyncLog[OutboxEntry]
	localLog   *opslog.LocalOpsLog
	localDir   string
	namespace  string
	logger     *slog.Logger
	pokeOutbox func()
	onEvent    func(string)
}

// NewWatcherHandler creates a handler that bridges watcher events to the outbox.
func NewWatcherHandler(outbox *SyncLog[OutboxEntry], localLog *opslog.LocalOpsLog, localDir, namespace string, logger *slog.Logger) *WatcherHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatcherHandler{
		outbox:     outbox,
		localLog:   localLog,
		localDir:   localDir,
		namespace:  namespace,
		logger:     logger,
		pokeOutbox: func() {},
		onEvent:    func(string) {},
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

		switch e.Type {
		case FileCreated, FileModified:
			localPath := filepath.Join(h.localDir, filepath.FromSlash(e.Path))
			cksum, err := fileChecksum(localPath)
			if err != nil {
				h.logger.Warn("watcher: checksum failed", "path", e.Path, "error", err)
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

			// Record op in local log
			h.localLog.AppendLocal(opslog.Entry{
				Type:      opslog.Put,
				Path:      e.Path,
				Checksum:  cksum,
				Size:      info.Size(),
				Namespace: h.namespace,
			})

			// Write to outbox
			h.outbox.Append(OutboxEntry{
				Op:        OpPut,
				Path:      e.Path,
				Checksum:  cksum,
				Namespace: h.namespace,
				LocalPath: localPath,
				Timestamp: time.Now().Unix(),
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

			// Record delete op in local log
			h.localLog.AppendLocal(opslog.Entry{
				Type:      opslog.Delete,
				Path:      e.Path,
				Namespace: existing.Namespace,
			})

			// Write to outbox with checksum/namespace from snapshot
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
		h.onEvent("state.changed")
		h.pokeOutbox()
	}
}

// HandleDirectoryTrash emits delete events for all files that were
// in a directory that was trashed. Called when a watched directory
// disappears — kqueue doesn't fire per-file events in this case.
func (h *WatcherHandler) HandleDirectoryTrash(dirPath string) {
	snap, err := h.localLog.Snapshot()
	if err != nil {
		h.logger.Warn("watcher: snapshot failed for directory trash", "error", err)
		return
	}
	prefix := dirPath + "/"
	var events []FileEvent
	for path := range snap.Files() {
		if len(path) > len(prefix) && path[:len(prefix)] == prefix {
			events = append(events, FileEvent{Path: path, Type: FileDeleted})
		}
	}
	if len(events) > 0 {
		h.HandleEvents(events)
	}
}
