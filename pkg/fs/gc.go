package fs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// GCResult contains stats from a garbage collection run.
type GCResult struct {
	BlobsReferenced   int
	BlobsFound        int
	BlobsDeleted      int
	BytesReclaimed    int64
	SnapshotsDeleted  int
	SnapshotsRetained int
}

// GCConfig configures garbage collection behavior.
type GCConfig struct {
	NSIDs         []string // namespace IDs to scan
	RetentionDays int      // history snapshot retention (default 30)
	DryRun        bool     // if true, report but don't delete
}

// GC removes orphaned blobs and expired history snapshots for the given
// namespaces. A blob is orphaned if no current or retained historical
// snapshot references it.
func GC(ctx context.Context, backend adapter.Backend, encKey []byte, cfg GCConfig) (*GCResult, error) {
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays).Unix()
	result := &GCResult{}

	for _, nsID := range cfg.NSIDs {
		if err := gcNamespace(ctx, backend, encKey, nsID, cutoff, cfg.DryRun, result); err != nil {
			return result, fmt.Errorf("GC namespace %s: %w", nsID, err)
		}
	}
	return result, nil
}

func gcNamespace(ctx context.Context, backend adapter.Backend, encKey []byte, nsID string, cutoff int64, dryRun bool, result *GCResult) error {
	// Collect all referenced blob hashes from current + retained snapshots.
	referenced := make(map[string]bool)
	snapshotPrefix := fmt.Sprintf("fs/%s/snapshots/", nsID)
	allKeys, err := backend.List(ctx, snapshotPrefix)
	if err != nil {
		return fmt.Errorf("listing snapshots: %w", err)
	}

	for _, key := range allKeys {
		// Parse: fs/{nsID}/snapshots/{deviceID}/{ts_or_latest}.enc
		isLatest := strings.HasSuffix(key, "/latest.enc")
		isHistory := !isLatest && strings.HasSuffix(key, ".enc")

		if isHistory {
			ts := parseSnapshotTS(key)
			if ts > 0 && ts < cutoff {
				result.SnapshotsDeleted++
				if !dryRun {
					backend.Delete(ctx, key)
				}
				continue // don't count references from expired snapshots
			}
		}

		result.SnapshotsRetained++
		// Download and collect blob references
		if err := collectBlobRefs(ctx, backend, encKey, key, referenced); err != nil {
			// Non-fatal — skip this snapshot
			continue
		}
	}

	result.BlobsReferenced = len(referenced)

	// List all blobs and delete unreferenced ones.
	blobPrefix := fmt.Sprintf("fs/%s/blobs/", nsID)
	blobKeys, err := backend.List(ctx, blobPrefix)
	if err != nil {
		return fmt.Errorf("listing blobs: %w", err)
	}
	result.BlobsFound = len(blobKeys)

	for _, blobKey := range blobKeys {
		hash := extractHashFromBlobKey(blobKey)
		if referenced[hash] {
			continue
		}
		result.BlobsDeleted++
		if !dryRun {
			if meta, err := backend.Head(ctx, blobKey); err == nil {
				result.BytesReclaimed += meta.Size
			}
			backend.Delete(ctx, blobKey)
		}
	}

	return nil
}

func collectBlobRefs(ctx context.Context, backend adapter.Backend, encKey []byte, key string, refs map[string]bool) error {
	rc, err := backend.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()

	data, err := readAllBytes(rc)
	if err != nil {
		return err
	}

	plain, err := Decrypt(data, encKey)
	if err != nil {
		return err
	}

	snap, err := opslog.UnmarshalSnapshot(plain)
	if err != nil {
		return err
	}

	for _, fi := range snap.Files() {
		for _, chunk := range fi.Chunks {
			refs[chunk] = true
		}
	}
	return nil
}

// parseSnapshotTS extracts the unix timestamp from a history snapshot key.
// Key format: fs/{nsID}/snapshots/{deviceID}/{timestamp}.enc
func parseSnapshotTS(key string) int64 {
	// Find the last path segment before .enc
	key = strings.TrimSuffix(key, ".enc")
	idx := strings.LastIndex(key, "/")
	if idx < 0 {
		return 0
	}
	var ts int64
	fmt.Sscanf(key[idx+1:], "%d", &ts)
	return ts
}

// extractHashFromBlobKey extracts the chunk hash from a blob S3 key.
// Key format: fs/{nsID}/blobs/ab/cd/abcdef1234....enc
func extractHashFromBlobKey(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		hash := key[idx+1:]
		return strings.TrimSuffix(hash, ".enc")
	}
	return key
}

func readAllBytes(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 32768)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
