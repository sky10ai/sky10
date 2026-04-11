package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/kv/collections"
)

const defaultRootPrefix = "mailbox"

var markerValue = []byte("1")

type eventPayload struct {
	Type    string            `json:"type"`
	Actor   Principal         `json:"actor"`
	LeaseID string            `json:"lease_id,omitempty"`
	Error   string            `json:"error,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// PrivateKVBackend persists mailbox state in a dedicated private-network KV
// namespace.
type PrivateKVBackend struct {
	store collections.KVStore
	root  string
}

// NewPrivateKVBackend creates a mailbox backend rooted at root.
func NewPrivateKVBackend(store collections.KVStore, root string) *PrivateKVBackend {
	root = strings.TrimSuffix(strings.TrimSpace(root), "/")
	if root == "" {
		root = defaultRootPrefix
	}
	return &PrivateKVBackend{store: store, root: root}
}

// Load rebuilds the mailbox snapshot from persisted KV state.
func (b *PrivateKVBackend) Load(_ context.Context) (Snapshot, error) {
	if b == nil || b.store == nil {
		return Snapshot{}, fmt.Errorf("mailbox backend store is required")
	}

	items, err := b.listItems()
	if err != nil {
		return Snapshot{}, err
	}

	events := make(map[string][]Event, len(items))
	for _, item := range items {
		itemEvents, err := b.listEvents(item.ID)
		if err != nil {
			return Snapshot{}, err
		}
		if len(itemEvents) > 0 {
			events[item.ID] = itemEvents
		}
	}

	claims, err := b.listClaims("")
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Items:  items,
		Events: events,
		Claims: claims,
	}, nil
}

// CreateItem persists an immutable mailbox item and durable view markers.
func (b *PrivateKVBackend) CreateItem(ctx context.Context, item Item) error {
	if b == nil || b.store == nil {
		return fmt.Errorf("mailbox backend store is required")
	}
	data, err := json.Marshal(cloneItem(item))
	if err != nil {
		return fmt.Errorf("marshal mailbox item: %w", err)
	}
	if err := b.store.Set(ctx, b.itemKey(item.ID), data); err != nil {
		return fmt.Errorf("store mailbox item %s: %w", item.ID, err)
	}
	if err := b.store.Set(ctx, b.outboxIndexKey(item.From.ID, item.ID), markerValue); err != nil {
		return fmt.Errorf("store outbox index for %s: %w", item.ID, err)
	}
	if recipient := item.RecipientID(); recipient != "" {
		if err := b.store.Set(ctx, b.inboxIndexKey(recipient, item.ID), markerValue); err != nil {
			return fmt.Errorf("store inbox index for %s: %w", item.ID, err)
		}
	}
	if queue := item.QueueName(); queue != "" {
		if err := b.store.Set(ctx, b.queueIndexKey(queue, item.ID), markerValue); err != nil {
			return fmt.Errorf("store queue index for %s: %w", item.ID, err)
		}
	}
	return nil
}

// AppendEvent appends a durable timeline event to an item's append log.
func (b *PrivateKVBackend) AppendEvent(ctx context.Context, event Event) (Event, error) {
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	payload := eventPayload{
		Type:    event.Type,
		Actor:   clonePrincipal(event.Actor),
		LeaseID: event.LeaseID,
		Error:   event.Error,
		Meta:    cloneEvent(event).Meta,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal mailbox event: %w", err)
	}
	entry, err := collections.NewAppendLog(b.store, b.eventLogPrefix(event.ItemID)).Append(ctx, data)
	if err != nil {
		return Event{}, fmt.Errorf("append mailbox event for %s: %w", event.ItemID, err)
	}
	stored := cloneEvent(event)
	stored.EventID = entry.ID
	stored.Timestamp = entry.CreatedAt
	return stored, nil
}

// Claim acquires a queue lease for an item if the current lease is absent or
// expired.
func (b *PrivateKVBackend) Claim(ctx context.Context, queue, itemID, holder string, ttl time.Duration) (Claim, bool, error) {
	record, ok, err := collections.NewLease(b.store, b.queueLeasePrefix(queue)).Claim(ctx, encodeKeyPart(itemID), holder, ttl)
	if err != nil {
		return Claim{}, false, err
	}
	return b.claimFromLease(queue, itemID, record), ok, nil
}

// Renew extends an active queue lease.
func (b *PrivateKVBackend) Renew(ctx context.Context, queue, itemID, holder, token string, ttl time.Duration) (Claim, bool, error) {
	record, ok, err := collections.NewLease(b.store, b.queueLeasePrefix(queue)).Renew(ctx, encodeKeyPart(itemID), holder, token, ttl)
	if err != nil {
		return Claim{}, false, err
	}
	return b.claimFromLease(queue, itemID, record), ok, nil
}

// Release drops an active queue lease when holder and token still match.
func (b *PrivateKVBackend) Release(ctx context.Context, queue, itemID, holder, token string) (bool, error) {
	return collections.NewLease(b.store, b.queueLeasePrefix(queue)).Release(ctx, encodeKeyPart(itemID), holder, token)
}

// DeleteItem removes an item, all timeline events, indexes, and any active or
// stale lease key rooted under this backend.
func (b *PrivateKVBackend) DeleteItem(ctx context.Context, itemID string) error {
	if b == nil || b.store == nil {
		return fmt.Errorf("mailbox backend store is required")
	}
	data, ok := b.store.Get(b.itemKey(itemID))
	if !ok {
		return nil
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil {
		return fmt.Errorf("parse mailbox item %s for delete: %w", itemID, err)
	}

	if err := b.store.Delete(ctx, b.itemKey(itemID)); err != nil {
		return fmt.Errorf("delete mailbox item %s: %w", itemID, err)
	}
	for _, key := range b.store.List(b.eventLogPrefix(itemID) + "/") {
		if err := b.store.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete mailbox event %q: %w", key, err)
		}
	}
	if err := b.store.Delete(ctx, b.outboxIndexKey(item.From.ID, itemID)); err != nil {
		return fmt.Errorf("delete outbox index for %s: %w", itemID, err)
	}
	if recipient := item.RecipientID(); recipient != "" {
		if err := b.store.Delete(ctx, b.inboxIndexKey(recipient, itemID)); err != nil {
			return fmt.Errorf("delete inbox index for %s: %w", itemID, err)
		}
	}
	if queue := item.QueueName(); queue != "" {
		if err := b.store.Delete(ctx, b.queueIndexKey(queue, itemID)); err != nil {
			return fmt.Errorf("delete queue index for %s: %w", itemID, err)
		}
		if err := b.store.Delete(ctx, b.queueLeasePrefix(queue)+"/"+encodeKeyPart(itemID)); err != nil {
			return fmt.Errorf("delete queue lease for %s: %w", itemID, err)
		}
	}
	return nil
}

// ContainsItem reports whether the item exists in this backend.
func (b *PrivateKVBackend) ContainsItem(itemID string) bool {
	if b == nil || b.store == nil {
		return false
	}
	_, ok := b.store.Get(b.itemKey(itemID))
	return ok
}

func (b *PrivateKVBackend) listItems() ([]Item, error) {
	keys := b.store.List(b.itemsPrefix() + "/")
	items := make([]Item, 0, len(keys))
	for _, key := range keys {
		itemID, ok, err := b.itemIDFromKey(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		data, exists := b.store.Get(key)
		if !exists {
			continue
		}
		var item Item
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("parse mailbox item %s: %w", itemID, err)
		}
		items = append(items, cloneItem(item))
	}
	return items, nil
}

func (b *PrivateKVBackend) listEvents(itemID string) ([]Event, error) {
	entries, err := collections.NewAppendLog(b.store, b.eventLogPrefix(itemID)).List()
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(entries))
	for _, entry := range entries {
		var payload eventPayload
		if err := json.Unmarshal(entry.Value, &payload); err != nil {
			return nil, fmt.Errorf("parse mailbox event %s for %s: %w", entry.ID, itemID, err)
		}
		events = append(events, Event{
			ItemID:    itemID,
			EventID:   entry.ID,
			Type:      payload.Type,
			Actor:     clonePrincipal(payload.Actor),
			LeaseID:   payload.LeaseID,
			Error:     payload.Error,
			Timestamp: entry.CreatedAt,
			Meta:      payload.Meta,
		})
	}
	return events, nil
}

func (b *PrivateKVBackend) listClaims(queue string) ([]Claim, error) {
	prefix := b.leasesPrefix() + "/"
	if queue != "" {
		prefix = b.queueLeasePrefix(queue) + "/"
	}
	keys := b.store.List(prefix)
	claims := make([]Claim, 0, len(keys))
	for _, key := range keys {
		parsedQueue, itemID, ok, err := b.claimKeyParts(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		raw, exists := b.store.Get(key)
		if !exists {
			continue
		}
		var record collections.LeaseRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("parse mailbox claim %q: %w", key, err)
		}
		if record.Expired(time.Now().UTC()) {
			continue
		}
		claims = append(claims, Claim{
			Queue:      parsedQueue,
			ItemID:     itemID,
			Holder:     record.Holder,
			Token:      record.Token,
			AcquiredAt: record.AcquiredAt,
			ExpiresAt:  record.ExpiresAt,
		})
	}
	return claims, nil
}

func (b *PrivateKVBackend) claimFromLease(queue, itemID string, record collections.LeaseRecord) Claim {
	return Claim{
		Queue:      queue,
		ItemID:     itemID,
		Holder:     record.Holder,
		Token:      record.Token,
		AcquiredAt: record.AcquiredAt,
		ExpiresAt:  record.ExpiresAt,
	}
}

func (b *PrivateKVBackend) itemsPrefix() string {
	return b.root + "/items"
}

func (b *PrivateKVBackend) itemKey(itemID string) string {
	return b.itemsPrefix() + "/" + encodeKeyPart(itemID)
}

func (b *PrivateKVBackend) itemIDFromKey(key string) (string, bool, error) {
	suffix, ok := strings.CutPrefix(key, b.itemsPrefix()+"/")
	if !ok || suffix == "" {
		return "", false, nil
	}
	itemID, err := decodeKeyPart(suffix)
	if err != nil {
		return "", false, fmt.Errorf("decode mailbox item key %q: %w", key, err)
	}
	return itemID, true, nil
}

func (b *PrivateKVBackend) eventLogPrefix(itemID string) string {
	return b.root + "/events/" + encodeKeyPart(itemID)
}

func (b *PrivateKVBackend) leasesPrefix() string {
	return b.root + "/leases"
}

func (b *PrivateKVBackend) queueLeasePrefix(queue string) string {
	return b.leasesPrefix() + "/" + encodeKeyPart(queue)
}

func (b *PrivateKVBackend) outboxIndexKey(principalID, itemID string) string {
	return b.root + "/index/outbox/" + encodeKeyPart(principalID) + "/" + encodeKeyPart(itemID)
}

func (b *PrivateKVBackend) inboxIndexKey(principalID, itemID string) string {
	return b.root + "/index/inbox/" + encodeKeyPart(principalID) + "/" + encodeKeyPart(itemID)
}

func (b *PrivateKVBackend) queueIndexKey(queue, itemID string) string {
	return b.root + "/index/queue/" + encodeKeyPart(queue) + "/" + encodeKeyPart(itemID)
}

func (b *PrivateKVBackend) claimKeyParts(key string) (queue, itemID string, ok bool, err error) {
	suffix, ok := strings.CutPrefix(key, b.leasesPrefix()+"/")
	if !ok || suffix == "" {
		return "", "", false, nil
	}
	queuePart, itemPart, ok := strings.Cut(suffix, "/")
	if !ok || queuePart == "" || itemPart == "" {
		return "", "", false, nil
	}
	queue, err = decodeKeyPart(queuePart)
	if err != nil {
		return "", "", false, fmt.Errorf("decode claim queue %q: %w", queuePart, err)
	}
	itemID, err = decodeKeyPart(itemPart)
	if err != nil {
		return "", "", false, fmt.Errorf("decode claim item %q: %w", itemPart, err)
	}
	return queue, itemID, true, nil
}

func encodeKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeKeyPart(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
