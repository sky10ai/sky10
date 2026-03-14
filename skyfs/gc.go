package skyfs

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/skyadapter"
)

// GCResult contains stats from a garbage collection run.
type GCResult struct {
	BlobsReferenced int
	BlobsFound      int
	BlobsDeleted    int
	BytesReclaimed  int64
}

// GC removes orphaned blobs that are no longer referenced by any file in
// the current manifest state. If dryRun is true, it reports what would be
// deleted without deleting anything.
func GC(ctx context.Context, backend skyadapter.Backend, identity *Identity, dryRun bool) (*GCResult, error) {
	store := New(backend, identity)
	state, err := store.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}

	// Collect all referenced chunk hashes
	referenced := make(map[string]bool)
	for _, entry := range state.Tree {
		for _, hash := range entry.Chunks {
			referenced[hash] = true
		}
	}

	// List all blobs in storage
	blobKeys, err := backend.List(ctx, "blobs/")
	if err != nil {
		return nil, fmt.Errorf("listing blobs: %w", err)
	}

	result := &GCResult{
		BlobsReferenced: len(referenced),
		BlobsFound:      len(blobKeys),
	}

	for _, blobKey := range blobKeys {
		hash := extractHashFromBlobKey(blobKey)
		if referenced[hash] {
			continue
		}

		// Orphaned blob
		if !dryRun {
			meta, err := backend.Head(ctx, blobKey)
			if err == nil {
				result.BytesReclaimed += meta.Size
			}

			if err := backend.Delete(ctx, blobKey); err != nil {
				return nil, fmt.Errorf("deleting orphaned blob %s: %w", blobKey, err)
			}
		} else {
			meta, err := backend.Head(ctx, blobKey)
			if err == nil {
				result.BytesReclaimed += meta.Size
			}
		}
		result.BlobsDeleted++
	}

	return result, nil
}

// extractHashFromBlobKey extracts the chunk hash from a blob S3 key.
// Key format: blobs/ab/cd/abcdef1234....enc
func extractHashFromBlobKey(key string) string {
	// Strip prefix "blobs/ab/cd/" and suffix ".enc"
	if len(key) < 16 {
		return key
	}
	// Find last "/" and strip ".enc"
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
