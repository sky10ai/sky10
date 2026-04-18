package mailbox

import (
	"context"
	"testing"
	"time"
)

func TestStoreCreateAppliesDefaultTTL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind: ItemKindMessage,
		From: Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := now.Add(DefaultLifecyclePolicy(ItemKindMessage).DefaultTTL)
	if !record.Item.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", record.Item.ExpiresAt, want)
	}
}

func TestStoreSweepExpiresRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind:      ItemKindMessage,
		From:      Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:        &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		ExpiresAt: now.Add(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)
	now = time.Now().UTC()
	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Expired != 1 {
		t.Fatalf("expired = %d, want 1", swept.Expired)
	}

	updated, ok := store.Get(record.Item.ID)
	if !ok {
		t.Fatal("record missing after sweep")
	}
	if updated.State != StateExpired {
		t.Fatalf("state = %s, want %s", updated.State, StateExpired)
	}
}

func TestStoreSweepLeaseExpiryReturnsQueueToQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind: ItemKindTaskRequest,
		From: Principal{ID: "agent:dispatcher", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "queue:work", Kind: PrincipalKindCapabilityQueue, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, ok, err := store.Claim(ctx, record.Item.ID, Principal{
		ID:    "agent:worker",
		Kind:  PrincipalKindLocalAgent,
		Scope: ScopePrivateNetwork,
	}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("claim should succeed")
	}

	time.Sleep(30 * time.Millisecond)
	now = time.Now().UTC()
	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.LeaseExpired != 1 {
		t.Fatalf("lease_expired = %d, want 1", swept.LeaseExpired)
	}

	updated, ok := store.Get(record.Item.ID)
	if !ok {
		t.Fatal("record missing after sweep")
	}
	if updated.Claim != nil {
		t.Fatalf("claim = %+v, want nil after lease expiry", updated.Claim)
	}
	if updated.State != StateQueued {
		t.Fatalf("state = %s, want %s", updated.State, StateQueued)
	}
	if !hasRecordEvent(updated, EventTypeLeaseExpired, "lease_id", record.Claim.Token) {
		t.Fatal("expected lease_expired event")
	}
}

func TestStoreSweepDeadLettersExhaustedFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind: ItemKindMessage,
		From: Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}

	maxAttempts := DefaultLifecyclePolicy(ItemKindMessage).Retry.MaxAttempts
	for i := 0; i < maxAttempts; i++ {
		now = now.Add(time.Second)
		if _, err := store.AppendEvent(ctx, Event{
			ItemID: record.Item.ID,
			Type:   EventTypeDeliveryFailed,
			Actor:  Principal{ID: "system:router", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
			Error:  "device not connected",
		}); err != nil {
			t.Fatal(err)
		}
	}

	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.DeadLettered != 1 {
		t.Fatalf("dead_lettered = %d, want 1", swept.DeadLettered)
	}

	updated, ok := store.Get(record.Item.ID)
	if !ok {
		t.Fatal("record missing after sweep")
	}
	if updated.State != StateDeadLettered {
		t.Fatalf("state = %s, want %s", updated.State, StateDeadLettered)
	}
}

func TestStoreSweepCompactsOldTerminalRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind: ItemKindMessage,
		From: Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{
		ItemID: record.Item.ID,
		Type:   EventTypeCompleted,
		Actor:  Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
	}); err != nil {
		t.Fatal(err)
	}

	now = time.Now().UTC().Add(DefaultLifecyclePolicy(ItemKindMessage).TerminalRetention + time.Hour)
	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Compacted != 1 {
		t.Fatalf("compacted = %d, want 1", swept.Compacted)
	}
	if _, ok := store.Get(record.Item.ID); ok {
		t.Fatal("record should be removed after compaction")
	}
	if backend.ContainsItem(record.Item.ID) {
		t.Fatal("backend should not contain compacted item")
	}
}

func TestStoreSweepCompactsDeliveredMessagePastExpiry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind:      ItemKindMessage,
		From:      Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:        &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{
		ItemID: record.Item.ID,
		Type:   EventTypeDelivered,
		Actor:  Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(2 * time.Minute)
	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Compacted != 1 {
		t.Fatalf("compacted = %d, want 1", swept.Compacted)
	}
	if _, ok := store.Get(record.Item.ID); ok {
		t.Fatal("delivered message should be removed after sweep once expired")
	}
	if backend.ContainsItem(record.Item.ID) {
		t.Fatal("backend should not contain compacted delivered message")
	}
}

func TestStoreSweepCompactsDeliveredMessageAfterRetention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, backend := newTestMailboxStore(t)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }

	record, err := store.Create(ctx, Item{
		Kind:      ItemKindMessage,
		From:      Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:        &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{
		ItemID:    record.Item.ID,
		Type:      EventTypeDelivered,
		Actor:     Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(DefaultLifecyclePolicy(ItemKindMessage).DeliveredRetention + time.Minute)
	swept, err := store.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Compacted != 1 {
		t.Fatalf("compacted = %d, want 1", swept.Compacted)
	}
	if _, ok := store.Get(record.Item.ID); ok {
		t.Fatal("delivered message should be removed after delivered retention elapses")
	}
	if backend.ContainsItem(record.Item.ID) {
		t.Fatal("backend should not contain compacted delivered message")
	}
}
