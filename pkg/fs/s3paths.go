package fs

import "fmt"

// S3 path helpers for the snapshot-exchange architecture.

func snapshotLatestKey(nsID, deviceID string) string {
	return fmt.Sprintf("fs/%s/snapshots/%s/latest.enc", nsID, deviceID)
}

func snapshotHistoryKey(nsID, deviceID string, ts int64) string {
	return fmt.Sprintf("fs/%s/snapshots/%s/%d.enc", nsID, deviceID, ts)
}

// namespacedBlobKey returns the S3 key for a chunk blob scoped to a namespace.
// Example: "fs/abc123/blobs/ab/cd/abcdef1234...enc"
func namespacedBlobKey(nsID, hash string) string {
	return fmt.Sprintf("fs/%s/blobs/%s/%s/%s.enc", nsID, hash[:2], hash[2:4], hash)
}

// namespacedPackKey returns the S3 key for a pack file scoped to a namespace.
func namespacedPackKey(nsID string, seq int) string {
	return fmt.Sprintf("fs/%s/packs/pack_%04d.enc", nsID, seq)
}

// namespacedPackIndexKey returns the S3 key for the pack index.
func namespacedPackIndexKey(nsID string) string {
	return fmt.Sprintf("fs/%s/pack-index.enc", nsID)
}

// fsSchemaKey returns the S3 key for the skyfs schema file.
func fsSchemaKey() string {
	return "fs/schema"
}
