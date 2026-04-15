package link

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestNostrDiscoveryApplyPresenceRecordOnlyAdvancesOnNewerRecord(t *testing.T) {
	t.Parallel()

	node := generateTestNode(t)
	current, err := node.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record: %v", err)
	}
	older := clonePresenceRecord(current)
	older.PublishedAt = current.PublishedAt.Add(-time.Minute)
	older.ExpiresAt = current.ExpiresAt.Add(-time.Minute)

	discovery := NewNostrDiscovery(nil, nil)
	if !discovery.applyPresenceRecord(older) {
		t.Fatal("expected first presence record to populate cache")
	}
	if discovery.applyPresenceRecord(older) {
		t.Fatal("expected identical older presence not to change cache")
	}
	if !discovery.applyPresenceRecord(current) {
		t.Fatal("expected newer presence record to advance cache")
	}
}

func TestNostrDiscoveryApplyPresenceRecordIgnoresExpiredRecord(t *testing.T) {
	t.Parallel()

	node := generateTestNode(t)
	current, err := node.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record: %v", err)
	}
	expired := clonePresenceRecord(current)
	expired.PublishedAt = time.Now().UTC().Add(-2 * time.Minute)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)

	discovery := NewNostrDiscovery(nil, nil)
	if discovery.applyPresenceRecord(expired) {
		t.Fatal("expected expired presence record to be ignored")
	}
}

func TestNostrDiscoveryApplyMembershipRecordOnlyAdvancesOnNewerRevision(t *testing.T) {
	t.Parallel()

	node := generateTestNode(t)
	current, err := node.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record: %v", err)
	}
	older := cloneMembershipRecord(current)
	older.Revision = current.Revision - 1
	older.UpdatedAt = current.UpdatedAt.Add(-time.Minute)

	discovery := NewNostrDiscovery(nil, nil)
	if !discovery.applyMembershipRecord(older) {
		t.Fatal("expected first membership record to populate cache")
	}
	if discovery.applyMembershipRecord(older) {
		t.Fatal("expected identical older membership not to change cache")
	}
	if !discovery.applyMembershipRecord(current) {
		t.Fatal("expected newer membership record to advance cache")
	}
}

func TestNostrSubscriptionRelayLabelStaysShortAndStable(t *testing.T) {
	t.Parallel()

	label := "mailbox:sky10q44gdywv9g54xedu8nt6mz9qph3v2ncxlljc3ns8428x7cyjhtl6qvkj9zf"
	got := nostrSubscriptionRelayLabel(label)
	if got == "" {
		t.Fatal("expected non-empty relay label")
	}
	if len(got) > maxNostrSubscriptionRelayLabelLen {
		t.Fatalf("relay label length = %d, want <= %d", len(got), maxNostrSubscriptionRelayLabelLen)
	}
	if strings.Contains(got, ":") {
		t.Fatalf("relay label %q should not include ':'", got)
	}
	if got != nostrSubscriptionRelayLabel(label) {
		t.Fatal("expected stable relay label")
	}
	if got == nostrSubscriptionRelayLabel("sky10-private:sky10q44gdywv9g54xedu8nt6mz9qph3v2ncxlljc3ns8428x7cyjhtl6qvkj9z0") {
		t.Fatal("expected distinct relay labels for distinct subscriptions")
	}
}
