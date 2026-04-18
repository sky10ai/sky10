package mailbox

import (
	"context"
	"testing"
	"time"
)

func TestStoreStatsSummarizesFallbackState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := newTestMailboxStore(t)

	privateRecord, err := store.Create(ctx, Item{
		Kind: ItemKindMessage,
		From: Principal{ID: "agent:sender", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "agent:recipient", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}

	sky10Record, err := store.Create(ctx, Item{
		Kind: ItemKindMessage,
		From: Principal{ID: "sky10qsender", Kind: PrincipalKindNetworkAgent, Scope: ScopeSky10Network},
		To:   &Principal{ID: "sky10qtarget", Kind: PrincipalKindNetworkAgent, Scope: ScopeSky10Network, RouteHint: "sky10qtarget"},
	})
	if err != nil {
		t.Fatal(err)
	}

	deliveredRecord, err := store.Create(ctx, Item{
		Kind: ItemKindReceipt,
		From: Principal{ID: "agent:provider", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:   &Principal{ID: "agent:caller", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.AppendEvent(ctx, Event{
		ItemID:    sky10Record.Item.ID,
		Type:      EventTypeHandedOff,
		Actor:     Principal{ID: "router", Kind: PrincipalKindLocalAgent, Scope: ScopeSky10Network},
		Timestamp: time.Now().Add(-2 * time.Minute).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{
		ItemID:    privateRecord.Item.ID,
		Type:      EventTypeDeliveryFailed,
		Actor:     Principal{ID: "router", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		Timestamp: time.Now().Add(-time.Minute).UTC(),
		Error:     "device not connected",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, Event{
		ItemID:    deliveredRecord.Item.ID,
		Type:      EventTypeDelivered,
		Actor:     Principal{ID: "router", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	stats := store.Stats()
	if stats.Queued != 1 {
		t.Fatalf("queued = %d, want 1", stats.Queued)
	}
	if stats.Failed != 1 {
		t.Fatalf("failed = %d, want 1", stats.Failed)
	}
	if stats.HandedOff != 1 {
		t.Fatalf("handed_off = %d, want 1", stats.HandedOff)
	}
	if stats.PendingPrivate != 1 {
		t.Fatalf("pending_private = %d, want 1", stats.PendingPrivate)
	}
	if stats.PendingSky10Network != 1 {
		t.Fatalf("pending_sky10_network = %d, want 1", stats.PendingSky10Network)
	}
	if stats.LastHandoffAt.IsZero() {
		t.Fatal("expected last handoff timestamp")
	}
	if stats.LastFailureAt.IsZero() {
		t.Fatal("expected last failure timestamp")
	}
	if stats.LastDeliveredAt.IsZero() {
		t.Fatal("expected last delivered timestamp")
	}
}
