package mailbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store materializes durable mailbox state into derived views.
type Store struct {
	backend Backend

	mu    sync.RWMutex
	index *recordIndex

	now   func() time.Time
	newID func() string
}

// NewStore creates a mailbox store and rebuilds state from the backend.
func NewStore(ctx context.Context, backend Backend) (*Store, error) {
	if backend == nil {
		return nil, fmt.Errorf("mailbox backend is required")
	}
	s := &Store{
		backend: backend,
		now:     func() time.Time { return time.Now().UTC() },
		newID:   func() string { return uuid.NewString() },
	}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload rebuilds the in-memory mailbox index from persisted state.
func (s *Store) Reload(ctx context.Context) error {
	snapshot, err := s.backend.Load(ctx)
	if err != nil {
		return fmt.Errorf("load mailbox snapshot: %w", err)
	}
	s.mu.Lock()
	s.index = newRecordIndex(snapshot)
	s.mu.Unlock()
	return nil
}

// Create persists a new item and emits the initial created event.
func (s *Store) Create(ctx context.Context, item Item) (Record, error) {
	if item.ID == "" {
		item.ID = s.newID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now()
	}
	if err := item.ValidateForCreate(); err != nil {
		return Record{}, err
	}
	if err := s.backend.CreateItem(ctx, item); err != nil {
		return Record{}, err
	}

	s.mu.Lock()
	if s.index == nil {
		s.index = newRecordIndex(Snapshot{})
	}
	s.index.upsertItem(item)
	s.mu.Unlock()

	event := Event{
		ItemID: item.ID,
		Type:   EventTypeCreated,
		Actor:  clonePrincipal(item.From),
	}
	if _, err := s.appendEvent(ctx, event); err != nil {
		_ = s.Reload(ctx)
		return Record{}, err
	}
	record, ok := s.Get(item.ID)
	if !ok {
		return Record{}, fmt.Errorf("mailbox item %s not found after create", item.ID)
	}
	return record, nil
}

// AppendEvent adds a durable event to an existing item timeline.
func (s *Store) AppendEvent(ctx context.Context, event Event) (Record, error) {
	if event.ItemID == "" {
		return Record{}, fmt.Errorf("event item_id is required")
	}
	if _, ok := s.Get(event.ItemID); !ok {
		return Record{}, fmt.Errorf("mailbox item %s not found", event.ItemID)
	}
	stored, err := s.appendEvent(ctx, event)
	if err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	s.index.appendEvent(stored.ItemID, stored)
	record, _ := s.index.get(stored.ItemID)
	s.mu.Unlock()
	return record, nil
}

// Get returns one fully materialized mailbox record.
func (s *Store) Get(itemID string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return Record{}, false
	}
	return s.index.get(itemID)
}

// ListInbox returns pending inbox items for a principal.
func (s *Store) ListInbox(principalID string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listInbox(principalID)
}

// ListOutbox returns non-terminal initiated items for a principal.
func (s *Store) ListOutbox(principalID string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listOutbox(principalID)
}

// ListQueue returns claimable queue items for a queue principal or capability.
func (s *Store) ListQueue(queue string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listQueue(queue)
}

// ListSent returns resolved outgoing items for a principal.
func (s *Store) ListSent(principalID string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listSent(principalID)
}

// ListFailed returns items that are currently in a failed or intervention
// state for a principal.
func (s *Store) ListFailed(principalID string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listFailed(principalID)
}

// Claim acquires a lease-backed claim for a queue item and records the claim
// event when successful.
func (s *Store) Claim(ctx context.Context, itemID string, actor Principal, ttl time.Duration) (Record, bool, error) {
	record, ok := s.Get(itemID)
	if !ok {
		return Record{}, false, fmt.Errorf("mailbox item %s not found", itemID)
	}
	queue := record.Item.QueueName()
	if queue == "" {
		return Record{}, false, fmt.Errorf("mailbox item %s is not claimable", itemID)
	}
	if err := actor.Validate(); err != nil {
		return Record{}, false, fmt.Errorf("claim actor: %w", err)
	}

	claim, ok, err := s.backend.Claim(ctx, queue, itemID, actor.ID, ttl)
	if err != nil {
		return Record{}, false, err
	}
	if !ok {
		current, _ := s.Get(itemID)
		return current, false, nil
	}
	s.mu.Lock()
	s.index.setClaim(claim)
	s.mu.Unlock()

	updated, err := s.AppendEvent(ctx, Event{
		ItemID:  itemID,
		Type:    EventTypeClaimed,
		Actor:   clonePrincipal(actor),
		LeaseID: claim.Token,
		Meta: map[string]string{
			"queue": queue,
		},
	})
	if err != nil {
		_ = s.Reload(ctx)
		return Record{}, false, err
	}
	return updated, true, nil
}

// Release clears a queue claim when holder and token still match.
func (s *Store) Release(ctx context.Context, itemID, holder, token string) (Record, bool, error) {
	record, ok := s.Get(itemID)
	if !ok {
		return Record{}, false, fmt.Errorf("mailbox item %s not found", itemID)
	}
	queue := record.Item.QueueName()
	if queue == "" {
		return Record{}, false, fmt.Errorf("mailbox item %s is not claimable", itemID)
	}
	released, err := s.backend.Release(ctx, queue, itemID, holder, token)
	if err != nil {
		return Record{}, false, err
	}
	if !released {
		current, _ := s.Get(itemID)
		return current, false, nil
	}
	s.mu.Lock()
	s.index.clearClaim(itemID)
	updated, _ := s.index.get(itemID)
	s.mu.Unlock()
	return updated, true, nil
}

// Renew extends an active queue claim when holder and token still match.
func (s *Store) Renew(ctx context.Context, itemID, holder, token string, ttl time.Duration) (Record, bool, error) {
	record, ok := s.Get(itemID)
	if !ok {
		return Record{}, false, fmt.Errorf("mailbox item %s not found", itemID)
	}
	queue := record.Item.QueueName()
	if queue == "" {
		return Record{}, false, fmt.Errorf("mailbox item %s is not claimable", itemID)
	}
	claim, ok, err := s.backend.Renew(ctx, queue, itemID, holder, token, ttl)
	if err != nil {
		return Record{}, false, err
	}
	if !ok {
		current, _ := s.Get(itemID)
		return current, false, nil
	}
	s.mu.Lock()
	s.index.setClaim(claim)
	updated, _ := s.index.get(itemID)
	s.mu.Unlock()
	return updated, true, nil
}

func (s *Store) appendEvent(ctx context.Context, event Event) (Event, error) {
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	stored, err := s.backend.AppendEvent(ctx, event)
	if err != nil {
		return Event{}, err
	}
	return stored, nil
}
