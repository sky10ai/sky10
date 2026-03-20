package fs

import (
	"context"
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// PollerV2 fetches remote ops from S3 and appends them to the local ops
// log. Pokes the Reconciler when new ops arrive so it can apply changes
// to the local filesystem.
type PollerV2 struct {
	store          *Store
	localLog       *opslog.LocalOpsLog
	interval       time.Duration
	namespace      string
	logger         *slog.Logger
	pokeReconciler func()
}

// NewPollerV2 creates a poller that appends remote ops to the local log.
func NewPollerV2(store *Store, localLog *opslog.LocalOpsLog, interval time.Duration, namespace string, logger *slog.Logger) *PollerV2 {
	if logger == nil {
		logger = slog.Default()
	}
	return &PollerV2{
		store:          store,
		localLog:       localLog,
		interval:       interval,
		namespace:      namespace,
		logger:         logger,
		pokeReconciler: func() {},
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

	cursor := p.localLog.LastRemoteOp()
	entries, err := log.ReadSince(ctx, cursor)
	if err != nil {
		p.logger.Warn("poll: reading entries failed", "error", err)
		return
	}

	p.logger.Info("poll", "ops", len(entries), "since", cursor)

	wrote := false
	maxTs := cursor

	for _, e := range entries {
		// Skip our own ops — they're already in the local log.
		// Exception: on a fresh log (cursor=0) we must import everything,
		// including our own ops, to recover state after ops.jsonl deletion.
		if cursor > 0 && e.Device == p.store.deviceID {
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

		// Skip duplicate puts (avoid unnecessary JSONL writes)
		if e.Type == opslog.Put {
			if existing, ok := p.localLog.Lookup(e.Path); ok {
				match := existing.Checksum == e.Checksum
				// Backwards compat: compare chunk hashes across checksum schemes
				if !match && len(existing.Chunks) == 1 && len(e.Chunks) == 1 && existing.Chunks[0] == e.Chunks[0] {
					match = true
				}
				if match {
					p.logger.Info("poll: already have", "path", e.Path)
					if e.Timestamp > maxTs {
						maxTs = e.Timestamp
					}
					continue
				}
			}
		}

		// Append remote op to local log (CRDT state)
		p.localLog.Append(e)
		wrote = true
		p.logger.Info("poll: appended", "path", e.Path, "op", string(e.Type), "device", e.Device)

		if e.Timestamp > maxTs {
			maxTs = e.Timestamp
		}
	}

	if maxTs > cursor {
		p.localLog.SetLastRemoteOp(maxTs)
	}

	if wrote {
		p.pokeReconciler()
	}
}
