package mailbox

import (
	"context"
	"time"
)

// Snapshot is the durable mailbox state loaded from a backend.
type Snapshot struct {
	Items  []Item
	Events map[string][]Event
	Claims []Claim
}

// Backend persists mailbox state.
type Backend interface {
	Load(ctx context.Context) (Snapshot, error)
	CreateItem(ctx context.Context, item Item) error
	AppendEvent(ctx context.Context, event Event) (Event, error)
	Claim(ctx context.Context, queue, itemID, holder string, ttl time.Duration) (Claim, bool, error)
	Renew(ctx context.Context, queue, itemID, holder, token string, ttl time.Duration) (Claim, bool, error)
	Release(ctx context.Context, queue, itemID, holder, token string) (bool, error)
}
