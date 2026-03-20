package fs

import (
	"context"
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// PollerV2 fetches remote ops from S3 and writes them to the inbox.
// Only touches S3 to list/read ops. All local changes go through
// the inbox worker.
type PollerV2 struct {
	store     *Store
	inbox     *SyncLog[InboxEntry]
	state     *DriveState
	interval  time.Duration
	namespace string
	logger    *slog.Logger
	pokeInbox func()
}

// NewPollerV2 creates a poller that writes to the inbox.
func NewPollerV2(store *Store, inbox *SyncLog[InboxEntry], state *DriveState, interval time.Duration, namespace string, logger *slog.Logger) *PollerV2 {
	if logger == nil {
		logger = slog.Default()
	}
	return &PollerV2{
		store:     store,
		inbox:     inbox,
		state:     state,
		interval:  interval,
		namespace: namespace,
		logger:    logger,
		pokeInbox: func() {},
	}
}

// Run polls on a timer until context is cancelled.
// Polls once immediately on start.
func (p *PollerV2) Run(ctx context.Context) {
	p.pollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

func (p *PollerV2) pollOnce(ctx context.Context) {
	log, err := p.store.getOpsLog(ctx)
	if err != nil {
		p.logger.Warn("poll: getting ops log failed", "error", err)
		return
	}

	entries, err := log.ReadSince(ctx, p.state.LastRemoteOp)
	if err != nil {
		p.logger.Warn("poll: reading entries failed", "error", err)
		return
	}

	p.logger.Info("poll", "ops", len(entries), "since", p.state.LastRemoteOp)

	wrote := false
	maxTs := p.state.LastRemoteOp

	for _, e := range entries {
		// Skip our own ops
		if e.Device == p.store.deviceID {
			if e.Timestamp > maxTs {
				maxTs = e.Timestamp
			}
			continue
		}

		// Filter by namespace
		if p.namespace != "" && e.Namespace != p.namespace {
			p.logger.Info("poll: skip namespace", "path", e.Path, "ns", e.Namespace, "want", p.namespace)
			if e.Timestamp > maxTs {
				maxTs = e.Timestamp
			}
			continue
		}

		switch e.Type {
		case opslog.Put:
			// Skip if we already have this version
			if existing, ok := p.state.GetFile(e.Path); ok && existing.Checksum == e.Checksum {
				p.logger.Info("poll: already have", "path", e.Path)
				break
			}
			p.logger.Info("poll: inbox put", "path", e.Path, "device", e.Device, "chunks", len(e.Chunks))
			p.inbox.Append(NewInboxPut(e.Path, e.Checksum, e.Namespace, e.Device, e.Chunks))
			wrote = true

		case opslog.Delete:
			// Only add to inbox if we have the file
			if _, ok := p.state.GetFile(e.Path); ok {
				p.logger.Info("poll: inbox delete", "path", e.Path, "device", e.Device)
				p.inbox.Append(NewInboxDelete(e.Path, e.Device))
				wrote = true
			} else {
				p.logger.Info("poll: skip delete (not local)", "path", e.Path)
			}
		}

		if e.Timestamp > maxTs {
			maxTs = e.Timestamp
		}
	}

	if maxTs > p.state.LastRemoteOp {
		p.state.SetLastRemoteOp(maxTs)
		p.state.Save()
	}

	if wrote {
		p.pokeInbox()
	}
}
