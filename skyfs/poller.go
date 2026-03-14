package skyfs

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Poller periodically checks S3 for new ops from other devices.
type Poller struct {
	store    *Store
	index    *Index
	interval time.Duration
}

// NewPoller creates a poller that checks for remote changes.
func NewPoller(store *Store, index *Index, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Poller{
		store:    store,
		index:    index,
		interval: interval,
	}
}

// PollOnce checks for new ops since the last known timestamp.
// Returns the new ops found, or nil if nothing changed.
func (p *Poller) PollOnce(ctx context.Context) ([]Op, error) {
	encKey, err := deriveManifestKey(p.store.identity)
	if err != nil {
		return nil, fmt.Errorf("deriving manifest key: %w", err)
	}

	var since int64
	if p.index != nil {
		sinceStr, _ := p.index.GetState("last_op_timestamp")
		if sinceStr != "" {
			since, _ = strconv.ParseInt(sinceStr, 10, 64)
		}
	}

	ops, err := ReadOps(ctx, p.store.backend, since, encKey)
	if err != nil {
		return nil, fmt.Errorf("reading ops: %w", err)
	}

	if len(ops) == 0 {
		return nil, nil
	}

	// Update last_op_timestamp
	var maxTS int64
	for _, op := range ops {
		if op.Timestamp > maxTS {
			maxTS = op.Timestamp
		}
	}
	if p.index != nil {
		p.index.SetState("last_op_timestamp", strconv.FormatInt(maxTS, 10))
	}

	return ops, nil
}

// Start runs the poller in a loop until the context is cancelled.
// It calls onChange with new ops whenever they're detected.
func (p *Poller) Start(ctx context.Context, onChange func([]Op)) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ops, err := p.PollOnce(ctx)
			if err != nil {
				continue // retry next tick
			}
			if len(ops) > 0 && onChange != nil {
				onChange(ops)
			}
		}
	}
}
