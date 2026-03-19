package fs

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// WatcherHandler processes file events from the watcher, writes to
// the outbox, and updates the drive state. No S3, no channels to
// other goroutines — just local disk operations.
type WatcherHandler struct {
	outbox     *SyncLog[OutboxEntry]
	state      *DriveState
	localDir   string
	namespace  string
	logger     *slog.Logger
	pokeOutbox func()
	onEvent    func(string)
}

// NewWatcherHandler creates a handler that bridges watcher events to the outbox.
func NewWatcherHandler(outbox *SyncLog[OutboxEntry], state *DriveState, localDir, namespace string, logger *slog.Logger) *WatcherHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatcherHandler{
		outbox:     outbox,
		state:      state,
		localDir:   localDir,
		namespace:  namespace,
		logger:     logger,
		pokeOutbox: func() {},
		onEvent:    func(string) {},
	}
}

// HandleEvents processes a batch of file events from the watcher.
// Updates state immediately, writes to outbox, pokes the outbox worker.
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

			// Skip if unchanged from state
			if existing, ok := h.state.GetFile(e.Path); ok && existing.Checksum == cksum {
				h.logger.Info("watcher: unchanged", "path", e.Path)
				continue
			}

			h.logger.Info("watcher: outbox", "path", e.Path)

			info, _ := os.Stat(localPath)
			if info == nil {
				continue
			}

			// Update state immediately
			h.state.SetFile(e.Path, FileState{
				Checksum:  cksum,
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
			existing, ok := h.state.GetFile(e.Path)
			if !ok {
				h.logger.Info("watcher: delete untracked", "path", e.Path)
				continue
			}

			h.logger.Info("watcher: delete", "path", e.Path)

			// Update state immediately
			h.state.RemoveFile(e.Path)

			// Write to outbox with checksum/namespace from state
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
		h.state.Save()
		h.onEvent("state.changed")
		h.pokeOutbox()
	}
}

// HandleDirectoryTrash emits delete events for all files that were
// in a directory that was trashed. Called when a watched directory
// disappears — kqueue doesn't fire per-file events in this case.
func (h *WatcherHandler) HandleDirectoryTrash(dirPath string) {
	prefix := dirPath + "/"
	var events []FileEvent
	for path := range h.state.Files {
		if len(path) > len(prefix) && path[:len(prefix)] == prefix {
			events = append(events, FileEvent{Path: path, Type: FileDeleted})
		}
	}
	if len(events) > 0 {
		h.HandleEvents(events)
	}
}
