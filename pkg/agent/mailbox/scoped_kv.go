package mailbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/kv/collections"
)

const (
	defaultScopedRoot      = "mailbox"
	defaultPrivateRootName = "private"
	defaultSky10RootName   = "sky10"
)

// ScopedKVBackend routes mailbox items to separate KV roots based on their
// transport scope while preserving one unified mailbox store API.
type ScopedKVBackend struct {
	private Backend
	network Backend
}

// NewScopedKVBackend creates a scope-aware mailbox backend.
func NewScopedKVBackend(store collections.KVStore, root string) *ScopedKVBackend {
	root = strings.TrimSuffix(strings.TrimSpace(root), "/")
	if root == "" {
		root = defaultScopedRoot
	}
	return &ScopedKVBackend{
		private: NewPrivateKVBackend(store, root+"/"+defaultPrivateRootName),
		network: NewPrivateKVBackend(store, root+"/"+defaultSky10RootName),
	}
}

// Load merges snapshots from both scope-specific backends.
func (b *ScopedKVBackend) Load(ctx context.Context) (Snapshot, error) {
	privateSnapshot, err := b.private.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	networkSnapshot, err := b.network.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return mergeSnapshots(privateSnapshot, networkSnapshot), nil
}

// CreateItem persists an item in the backend selected by its scope.
func (b *ScopedKVBackend) CreateItem(ctx context.Context, item Item) error {
	return b.backendForScope(item.Scope()).CreateItem(ctx, item)
}

// AppendEvent appends an event to the backend that owns the item.
func (b *ScopedKVBackend) AppendEvent(ctx context.Context, event Event) (Event, error) {
	backend, err := b.backendForItem(event.ItemID)
	if err != nil {
		return Event{}, err
	}
	return backend.AppendEvent(ctx, event)
}

// Claim acquires a lease in the backend that owns the queue item.
func (b *ScopedKVBackend) Claim(ctx context.Context, queue, itemID, holder string, ttl time.Duration) (Claim, bool, error) {
	backend, err := b.backendForItem(itemID)
	if err != nil {
		return Claim{}, false, err
	}
	return backend.Claim(ctx, queue, itemID, holder, ttl)
}

// Renew extends a lease in the backend that owns the queue item.
func (b *ScopedKVBackend) Renew(ctx context.Context, queue, itemID, holder, token string, ttl time.Duration) (Claim, bool, error) {
	backend, err := b.backendForItem(itemID)
	if err != nil {
		return Claim{}, false, err
	}
	return backend.Renew(ctx, queue, itemID, holder, token, ttl)
}

// Release drops a lease in the backend that owns the queue item.
func (b *ScopedKVBackend) Release(ctx context.Context, queue, itemID, holder, token string) (bool, error) {
	backend, err := b.backendForItem(itemID)
	if err != nil {
		return false, err
	}
	return backend.Release(ctx, queue, itemID, holder, token)
}

// DeleteItem removes an item from the backend that owns it.
func (b *ScopedKVBackend) DeleteItem(ctx context.Context, itemID string) error {
	backend, err := b.backendForItem(itemID)
	if err != nil {
		return err
	}
	return backend.DeleteItem(ctx, itemID)
}

// ContainsItem reports whether the item exists in either scope backend.
func (b *ScopedKVBackend) ContainsItem(itemID string) bool {
	return b.private.ContainsItem(itemID) || b.network.ContainsItem(itemID)
}

func (b *ScopedKVBackend) backendForScope(scope string) Backend {
	if scope == ScopeSky10Network {
		return b.network
	}
	return b.private
}

func (b *ScopedKVBackend) backendForItem(itemID string) (Backend, error) {
	switch {
	case b.network.ContainsItem(itemID):
		return b.network, nil
	case b.private.ContainsItem(itemID):
		return b.private, nil
	default:
		return nil, fmt.Errorf("mailbox item %s not found", itemID)
	}
}

func mergeSnapshots(snapshots ...Snapshot) Snapshot {
	out := Snapshot{
		Events: make(map[string][]Event),
	}
	for _, snapshot := range snapshots {
		out.Items = append(out.Items, snapshot.Items...)
		out.Claims = append(out.Claims, snapshot.Claims...)
		for itemID, events := range snapshot.Events {
			out.Events[itemID] = append(out.Events[itemID], events...)
		}
	}
	return out
}
