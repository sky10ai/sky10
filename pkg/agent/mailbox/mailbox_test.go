package mailbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/kv/collections"
)

func TestStoreCreateListAndComplete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)

	record, err := store.Create(ctx, Item{
		Kind:          ItemKindMessage,
		From:          Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:            &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		PayloadInline: []byte(`{"text":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateQueued {
		t.Fatalf("state = %s, want %s", record.State, StateQueued)
	}

	if got := store.ListOutbox("agent:sender"); len(got) != 1 {
		t.Fatalf("outbox len = %d, want 1", len(got))
	}
	if got := store.ListInbox("human:alice"); len(got) != 1 {
		t.Fatalf("inbox len = %d, want 1", len(got))
	}

	record, err = store.AppendEvent(ctx, Event{
		ItemID: record.Item.ID,
		Type:   EventTypeCompleted,
		Actor:  Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.State != StateCompleted {
		t.Fatalf("state = %s, want %s", record.State, StateCompleted)
	}
	if got := store.ListOutbox("agent:sender"); len(got) != 0 {
		t.Fatalf("outbox len = %d, want 0 after completion", len(got))
	}
	if got := store.ListSent("agent:sender"); len(got) != 1 {
		t.Fatalf("sent len = %d, want 1", len(got))
	}
	if got := store.ListInbox("human:alice"); len(got) != 0 {
		t.Fatalf("inbox len = %d, want 0 after completion", len(got))
	}
}

func TestStoreQueueClaimReleaseAndRebuild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)

	record, err := store.Create(ctx, Item{
		Kind:        ItemKindTaskRequest,
		From:        Principal{ID: "agent:dispatcher", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:          &Principal{ID: "queue:payments", Kind: PrincipalKindCapabilityQueue, Scope: ScopePrivateNetwork},
		TargetSkill: "payments",
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := record.Item.QueueName()
	if queue == "" {
		t.Fatal("expected queue item")
	}
	if got := store.ListQueue(queue); len(got) != 1 {
		t.Fatalf("queue len = %d, want 1", len(got))
	}

	record, ok, err := store.Claim(ctx, record.Item.ID, Principal{
		ID:    "agent:worker-a",
		Kind:  PrincipalKindLocalAgent,
		Scope: ScopePrivateNetwork,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claim not acquired")
	}
	if record.Claim == nil || record.Claim.Holder != "agent:worker-a" {
		t.Fatalf("claim = %+v, want holder agent:worker-a", record.Claim)
	}
	if record.State != StateClaimed {
		t.Fatalf("state = %s, want %s", record.State, StateClaimed)
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(record.Item.ID)
	if !ok {
		t.Fatal("reloaded item not found")
	}
	if got.Claim == nil || got.Claim.Holder != "agent:worker-a" {
		t.Fatalf("reloaded claim = %+v, want holder agent:worker-a", got.Claim)
	}

	got, ok, err = reloaded.Release(ctx, record.Item.ID, "agent:worker-a", record.Claim.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("release should succeed")
	}
	if got.Claim != nil {
		t.Fatalf("claim = %+v, want nil after release", got.Claim)
	}
	if got.State != StateQueued {
		t.Fatalf("state = %s, want %s after release", got.State, StateQueued)
	}
}

func TestStoreFailedProjectionAndPayloadRefRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)
	payloadRef, err := collections.NewPayloadRef(collections.PayloadKindChunkedKV, "payloads/abc", 9000, collections.SHA256Digest([]byte("payload")))
	if err != nil {
		t.Fatal(err)
	}

	record, err := store.Create(ctx, Item{
		Kind:       ItemKindPaymentRequired,
		From:       Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:         &Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		PayloadRef: &payloadRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.AppendEvent(ctx, Event{
		ItemID: record.Item.ID,
		Type:   EventTypeDeliveryFailed,
		Actor:  Principal{ID: "system", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		Error:  "device not connected",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !record.Failed() {
		t.Fatalf("record should be failed, got state %s", record.State)
	}
	if got := store.ListFailed("agent:provider"); len(got) != 1 {
		t.Fatalf("failed len = %d, want 1", len(got))
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(record.Item.ID)
	if !ok {
		t.Fatal("reloaded item not found")
	}
	if got.Item.PayloadRef == nil || got.Item.PayloadRef.Key != "payloads/abc" {
		t.Fatalf("payload ref = %+v, want payloads/abc", got.Item.PayloadRef)
	}
}

func newTestMailboxStore(t *testing.T) (*Store, *PrivateKVBackend) {
	t.Helper()

	backend := NewPrivateKVBackend(newMemoryKVStore(), "")
	store, err := NewStore(context.Background(), backend)
	if err != nil {
		t.Fatal(err)
	}
	return store, backend
}

type memoryKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryKVStore() *memoryKVStore {
	return &memoryKVStore{data: make(map[string][]byte)}
}

func (s *memoryKVStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return append([]byte(nil), value...), ok
}

func (s *memoryKVStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *memoryKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0)
	for key := range s.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}
	return out
}
