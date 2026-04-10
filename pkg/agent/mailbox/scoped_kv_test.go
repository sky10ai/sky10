package mailbox

import (
	"context"
	"testing"
	"time"
)

func TestScopedKVBackendSeparatesPrivateAndSky10Items(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewScopedKVBackend(newMemoryKVStore(), "")
	store, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}

	privateRecord, err := store.Create(ctx, Item{
		Kind:           ItemKindMessage,
		From:           Principal{ID: "agent:local", Kind: PrincipalKindLocalAgent, Scope: ScopePrivateNetwork},
		To:             &Principal{ID: "human:alice", Kind: PrincipalKindHuman, Scope: ScopePrivateNetwork},
		IdempotencyKey: "private-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	networkRecord, err := store.Create(ctx, Item{
		Kind:           ItemKindMessage,
		From:           Principal{ID: "sky10qsender", Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: "sky10qsender"},
		To:             &Principal{ID: "agent:remote", Kind: PrincipalKindNetworkAgent, Scope: ScopeSky10Network, RouteHint: "sky10qremote"},
		IdempotencyKey: "network-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !backend.ContainsItem(privateRecord.Item.ID) || !backend.ContainsItem(networkRecord.Item.ID) {
		t.Fatal("scoped backend should contain both private and network items")
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(privateRecord.Item.ID); !ok {
		t.Fatal("private item missing after reload")
	}
	got, ok := reloaded.Get(networkRecord.Item.ID)
	if !ok {
		t.Fatal("network item missing after reload")
	}
	if got.Item.Scope() != ScopeSky10Network {
		t.Fatalf("network item scope = %s, want %s", got.Item.Scope(), ScopeSky10Network)
	}
}

func TestScopedKVBackendClaimsNetworkQueueItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewScopedKVBackend(newMemoryKVStore(), "")
	store, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}

	record, err := store.Create(ctx, Item{
		Kind:           ItemKindTaskRequest,
		From:           Principal{ID: "sky10qplanner", Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: "sky10qplanner"},
		To:             &Principal{ID: "queue:research", Kind: PrincipalKindCapabilityQueue, Scope: ScopeSky10Network, RouteHint: "sky10qworker"},
		IdempotencyKey: "network-queue-1",
		TargetSkill:    "research",
	})
	if err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := store.Claim(ctx, record.Item.ID, Principal{
		ID:        "agent:worker",
		Kind:      PrincipalKindNetworkAgent,
		Scope:     ScopeSky10Network,
		RouteHint: "sky10qworker",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected network queue item to be claimable")
	}
	if claimed.Claim == nil || claimed.Claim.Holder != "agent:worker" {
		t.Fatalf("claim = %+v, want holder agent:worker", claimed.Claim)
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(record.Item.ID)
	if !ok {
		t.Fatal("claimed item missing after reload")
	}
	if got.Claim == nil || got.Claim.Holder != "agent:worker" {
		t.Fatalf("reloaded claim = %+v, want holder agent:worker", got.Claim)
	}
}
