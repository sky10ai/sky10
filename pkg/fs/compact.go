package fs

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/pkg/adapter"
)

// CompactResult contains stats from a compaction run.
type CompactResult struct {
	OpsCompacted     int
	OpsDeleted       int
	SnapshotsKept    int
	SnapshotsDeleted int
}

// Compact writes a new snapshot from the current state and cleans up
// old ops and snapshots. Keeps the last maxSnapshots snapshots.
//
// Compaction is idempotent: two devices compacting simultaneously read
// the same ops, replay in the same order, and produce logically identical
// snapshots.
func Compact(ctx context.Context, backend adapter.Backend, identity *DeviceKey, maxSnapshots int) (*CompactResult, error) {
	store := New(backend, identity)
	log, err := store.getOpsLog(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops log: %w", err)
	}

	r, err := log.Compact(ctx, maxSnapshots)
	if err != nil {
		return nil, fmt.Errorf("compact: %w", err)
	}

	return &CompactResult{
		OpsCompacted:     r.OpsCompacted,
		OpsDeleted:       r.OpsDeleted,
		SnapshotsKept:    r.SnapshotsKept,
		SnapshotsDeleted: r.SnapshotsDeleted,
	}, nil
}
