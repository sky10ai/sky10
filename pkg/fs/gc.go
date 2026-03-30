package fs

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/pkg/adapter"
)

// GCResult contains stats from a garbage collection run.
type GCResult struct {
	BlobsReferenced int
	BlobsFound      int
	BlobsDeleted    int
	BytesReclaimed  int64
}

// GC removes orphaned blobs that are no longer referenced by any snapshot.
// TODO(snapshot-exchange): rewrite to use per-device snapshots instead of
// the S3 ops log. Currently stubbed.
func GC(ctx context.Context, backend adapter.Backend, identity *DeviceKey, dryRun bool) (*GCResult, error) {
	return nil, fmt.Errorf("GC not yet implemented in snapshot-exchange architecture")
}

// extractHashFromBlobKey extracts the chunk hash from a blob S3 key.
// Key format: blobs/ab/cd/abcdef1234....enc
func extractHashFromBlobKey(key string) string {
	if len(key) < 16 {
		return key
	}
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			hash := key[i+1:]
			if len(hash) > 4 && hash[len(hash)-4:] == ".enc" {
				return hash[:len(hash)-4]
			}
			return hash
		}
	}
	return key
}
