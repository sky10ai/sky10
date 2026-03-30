package fs

import "fmt"

// S3 path helpers for the snapshot-exchange architecture.
// All paths are scoped per namespace ID.

func snapshotLatestKey(nsID, deviceID string) string {
	return fmt.Sprintf("fs/%s/snapshots/%s/latest.enc", nsID, deviceID)
}

func snapshotHistoryKey(nsID, deviceID string, ts int64) string {
	return fmt.Sprintf("fs/%s/snapshots/%s/%d.enc", nsID, deviceID, ts)
}
