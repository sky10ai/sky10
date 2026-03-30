package fs

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/pkg/adapter"
)

// CompactResult contains stats from a compaction run.
// Deprecated: compaction is no longer needed in the snapshot-exchange architecture.
type CompactResult struct {
	OpsCompacted     int
	OpsDeleted       int
	SnapshotsKept    int
	SnapshotsDeleted int
}

// Compact is a no-op stub. Compaction is no longer needed — the S3 ops log
// has been replaced by per-device snapshot exchange.
func Compact(ctx context.Context, backend adapter.Backend, identity *DeviceKey, maxSnapshots int) (*CompactResult, error) {
	return nil, fmt.Errorf("compaction removed: snapshot-exchange architecture has no S3 ops log")
}
