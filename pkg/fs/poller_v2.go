package fs

import (
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// PollerV2 is a deprecated stub. The S3 ops poller has been replaced
// by the snapshot poller in the snapshot-exchange architecture.
type PollerV2 struct {
	pokeReconciler func()
	heartbeat      func()
	onEvent        func(string, map[string]any)
	driveName      string
}

// NewPollerV2 returns a no-op poller stub.
func NewPollerV2(_ *Store, _ *opslog.LocalOpsLog, _ time.Duration, _ string, _ *slog.Logger) *PollerV2 {
	return &PollerV2{
		pokeReconciler: func() {},
		heartbeat:      func() {},
		onEvent:        func(string, map[string]any) {},
	}
}

func (p *PollerV2) pollOnce(_ interface{}) {}
func (p *PollerV2) Run(_ interface{})      {}
