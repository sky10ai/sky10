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
	heartbeat      func() // watchdog heartbeat
	onEvent        func(string, map[string]any)
	driveName      string
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
		heartbeat:      func() {},
		onEvent:        func(string, map[string]any) {},
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
	p.heartbeat()
	log, err := p.store.getOpsLog(ctx)
	if err != nil {
		p.logger.Warn("poll: getting ops log failed", "error", err)
		return
	}

	cursor := p.localLog.LastRemoteOp()

	// Read ops in batches of 200. Each batch is appended to the local
	// log and the reconciler is poked so downloads start while the
	// poller is still fetching remaining ops from S3.
	totalAppended := 0
	totalS3 := 0

	_, err = log.ReadBatched(ctx, cursor, 200, func(entries []opslog.Entry) {
		p.heartbeat()
		batchAppended := 0
		maxTs := cursor

		totalS3 += len(entries)

		// Snapshot before this batch for dedup checks. Cached, so O(1).
		snap, _ := p.localLog.Snapshot()
		var snapDirs map[string]opslog.DirInfo
		var snapDeleted map[string]bool
		if snap != nil {
			snapDirs = snap.Dirs()
			snapDeleted = snap.DeletedFiles()
		}

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
				if e.Timestamp > maxTs {
					maxTs = e.Timestamp
				}
				continue
			}

			// Skip ops already reflected in the local snapshot.
			switch e.Type {
			case opslog.Put, opslog.Symlink:
				if snap != nil {
					if existing, ok := snap.Lookup(e.Path); ok {
						match := existing.Checksum == e.Checksum
						if !match && len(existing.Chunks) == 1 && len(e.Chunks) == 1 && existing.Chunks[0] == e.Chunks[0] {
							match = true
						}
						if match {
							if e.Timestamp > maxTs {
								maxTs = e.Timestamp
							}
							continue
						}
					}
				}
			case opslog.CreateDir:
				if _, ok := snapDirs[e.Path]; ok {
					if e.Timestamp > maxTs {
						maxTs = e.Timestamp
					}
					continue
				}
			case opslog.Delete:
				if snapDeleted[e.Path] {
					if e.Timestamp > maxTs {
						maxTs = e.Timestamp
					}
					continue
				}
			}

			p.localLog.Append(e)
			batchAppended++
			p.logger.Info("poll: appended", "path", e.Path, "op", string(e.Type), "device", e.Device)

			if e.Timestamp > maxTs {
				maxTs = e.Timestamp
			}
		}

		if maxTs > cursor {
			p.localLog.SetLastRemoteOp(maxTs)
		}

		totalAppended += batchAppended
		if batchAppended > 0 {
			p.onEvent("sync.active", nil)
			p.onEvent("poll.progress", map[string]any{
				"drive":   p.driveName,
				"fetched": batchAppended,
			})
			p.logger.Info("poll: batch imported", "appended", batchAppended, "batch_size", len(entries))
			p.pokeReconciler()
		}
	})

	if err != nil {
		p.logger.Warn("poll: reading entries failed", "error", err)
		return
	}

	if totalAppended > 0 {
		p.logger.Info("poll: done", "appended", totalAppended, "total_s3", totalS3)
	} else {
		p.logger.Info("poll", "ops", totalS3, "since", cursor)
	}
}
