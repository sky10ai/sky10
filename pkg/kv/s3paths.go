package kv

import "fmt"

// S3 path helpers for the KV snapshot-exchange architecture.

func snapshotLatestKey(nsID, deviceID string) string {
	return fmt.Sprintf("kv/%s/snapshots/%s/latest.enc", nsID, deviceID)
}

func snapshotHistoryKey(nsID, deviceID string, ts int64) string {
	return fmt.Sprintf("kv/%s/snapshots/%s/%d.enc", nsID, deviceID, ts)
}
