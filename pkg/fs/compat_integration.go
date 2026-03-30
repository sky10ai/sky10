//go:build integration

package fs

import (
	"context"
	"log/slog"
)

// NewDaemon is a deprecated stub for integration tests that haven't been
// rewritten for the snapshot-exchange architecture.
func NewDaemon(store *Store, _ interface{}, config DaemonConfig, logger *slog.Logger) (*DaemonV2_5, error) {
	return NewDaemonV2_5(store, config, logger)
}

// SyncResult is a deprecated type for old integration tests.
type SyncResult struct {
	Uploaded   int
	Downloaded int
	Deleted    int
	Errors     int
	Conflicts  int
}

// threeWaySync is a deprecated stub. Calls SyncOnce under the hood.
func (d *DaemonV2_5) threeWaySync(ctx context.Context) SyncResult {
	d.SyncOnce(ctx)
	return SyncResult{}
}
