package fs

import (
	"context"
	"log/slog"
	"time"
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
	opsKey, err := p.store.opsKey(ctx)
	if err != nil {
		p.logger.Warn("poll: getting ops key failed", "error", err)
		return
	}

	ops, err := ReadOps(ctx, p.store.backend, p.state.LastRemoteOp, opsKey)
	if err != nil {
		p.logger.Warn("poll: reading ops failed", "error", err)
		return
	}

	p.logger.Info("poll", "ops", len(ops), "since", p.state.LastRemoteOp)

	wrote := false
	maxTs := p.state.LastRemoteOp

	for _, op := range ops {
		// Skip our own ops
		if op.Device == p.store.deviceID {
			if op.Timestamp > maxTs {
				maxTs = op.Timestamp
			}
			continue
		}

		// Filter by namespace
		if p.namespace != "" && op.Namespace != p.namespace {
			if op.Timestamp > maxTs {
				maxTs = op.Timestamp
			}
			continue
		}

		switch op.Type {
		case OpPut:
			// Skip if we already have this version
			if existing, ok := p.state.GetFile(op.Path); ok && existing.Checksum == op.Checksum {
				break
			}
			p.inbox.Append(NewInboxPut(op.Path, op.Checksum, op.Namespace, op.Device))
			wrote = true

		case OpDelete:
			// Only add to inbox if we have the file
			if _, ok := p.state.GetFile(op.Path); ok {
				p.inbox.Append(NewInboxDelete(op.Path, op.Device))
				wrote = true
			}
		}

		if op.Timestamp > maxTs {
			maxTs = op.Timestamp
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
