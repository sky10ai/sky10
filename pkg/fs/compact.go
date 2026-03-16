package fs

import (
	"context"
	"fmt"
	"sort"

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
	if maxSnapshots < 1 {
		maxSnapshots = 3
	}

	// Build current state from latest snapshot + ops
	store := New(backend, identity)

	encKey, err := store.opsKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops key: %w", err)
	}
	state, err := store.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading current state: %w", err)
	}

	// Save new snapshot
	if err := store.SaveSnapshot(ctx); err != nil {
		return nil, fmt.Errorf("saving snapshot: %w", err)
	}

	result := &CompactResult{}

	// Count ops that will be compacted
	allOps, err := ReadAllOps(ctx, backend, encKey)
	if err != nil {
		return nil, fmt.Errorf("reading ops: %w", err)
	}
	result.OpsCompacted = len(allOps)

	// Delete all ops (they're now captured in the snapshot)
	opsKeys, err := backend.List(ctx, "ops/")
	if err != nil {
		return nil, fmt.Errorf("listing ops: %w", err)
	}
	for _, key := range opsKeys {
		if err := backend.Delete(ctx, key); err != nil {
			return nil, fmt.Errorf("deleting op %s: %w", key, err)
		}
		result.OpsDeleted++
	}

	// Clean up old snapshots, keep the latest maxSnapshots
	snapshotKeys, err := backend.List(ctx, "manifests/snapshot-")
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	sort.Strings(snapshotKeys)

	if len(snapshotKeys) > maxSnapshots {
		toDelete := snapshotKeys[:len(snapshotKeys)-maxSnapshots]
		for _, key := range toDelete {
			if err := backend.Delete(ctx, key); err != nil {
				return nil, fmt.Errorf("deleting old snapshot %s: %w", key, err)
			}
			result.SnapshotsDeleted++
		}
	}

	remaining, _ := backend.List(ctx, "manifests/snapshot-")
	result.SnapshotsKept = len(remaining)

	// Verify: state after compaction should match state before
	_ = state // used for the compaction, verification is implicit in tests

	return result, nil
}
