package link

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestNostrDiscoveryUsesCachedMembershipOnQueryFailure(t *testing.T) {
	t.Parallel()

	node := generateTestNode(t)
	membership, err := node.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record: %v", err)
	}
	content, err := json.Marshal(membership)
	if err != nil {
		t.Fatalf("marshal membership: %v", err)
	}

	discovery := NewNostrDiscovery(nil, nil)
	calls := 0
	discovery.queryFn = func(context.Context, nostr.Filter) ([]*nostr.Event, error) {
		calls++
		if calls == 1 {
			return []*nostr.Event{{Content: string(content)}}, nil
		}
		return nil, errors.New("relay unavailable")
	}

	got, err := discovery.QueryMembership(context.Background(), node.Address())
	if err != nil {
		t.Fatalf("first query membership: %v", err)
	}
	if got.Identity != membership.Identity {
		t.Fatalf("identity = %q, want %q", got.Identity, membership.Identity)
	}

	got, err = discovery.QueryMembership(context.Background(), node.Address())
	if err != nil {
		t.Fatalf("cached query membership: %v", err)
	}
	if got.Revision != membership.Revision {
		t.Fatalf("revision = %d, want %d", got.Revision, membership.Revision)
	}
}

func TestNostrDiscoveryUsesCachedPresenceOnQueryFailure(t *testing.T) {
	t.Parallel()

	node := generateTestNode(t)
	presence, err := node.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record: %v", err)
	}
	content, err := json.Marshal(presence)
	if err != nil {
		t.Fatalf("marshal presence: %v", err)
	}

	discovery := NewNostrDiscovery(nil, nil)
	calls := 0
	discovery.queryFn = func(context.Context, nostr.Filter) ([]*nostr.Event, error) {
		calls++
		if calls == 1 {
			return []*nostr.Event{{Content: string(content)}}, nil
		}
		return nil, errors.New("relay unavailable")
	}

	got, err := discovery.QueryPresenceAll(context.Background(), node.Address())
	if err != nil {
		t.Fatalf("first query presence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("presence count = %d, want 1", len(got))
	}

	got, err = discovery.QueryPresenceAll(context.Background(), node.Address())
	if err != nil {
		t.Fatalf("cached query presence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cached presence count = %d, want 1", len(got))
	}
	if got[0].DevicePubKey != presence.DevicePubKey {
		t.Fatalf("device pubkey = %q, want %q", got[0].DevicePubKey, presence.DevicePubKey)
	}
}
