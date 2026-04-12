package link

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestLiveRelayTrackerKeepsPreferredRelayWithinHoldDown(t *testing.T) {
	t.Parallel()

	const preferredID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"
	const fallbackID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8N"
	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	tracker := NewLiveRelayTracker([]peer.AddrInfo{
		{ID: peer.ID(preferredID)},
		{ID: peer.ID(fallbackID)},
	}, RelayBootstrapSnapshot{
		PeerIDs:         []string{preferredID, fallbackID},
		PreferredPeerID: preferredID,
		PreferredAt:     now.Add(-10 * time.Minute),
		LastSwitchAt:    now.Add(-10 * time.Minute),
		UpdatedAt:       now.Add(-10 * time.Minute),
	})
	tracker.now = func() time.Time { return now }

	active, snapshot, changed := tracker.ObserveHostAddrs([]ma.Multiaddr{
		ma.StringCast("/ip4/127.0.0.1/tcp/4102/p2p/" + fallbackID + "/p2p-circuit"),
	})
	if !changed {
		t.Fatal("expected active relay ordering to refresh")
	}
	if len(active) != 1 || active[0].ID.String() != fallbackID {
		t.Fatalf("active relays = %v, want fallback only", peerIDsFromInfos(active))
	}
	if snapshot.PreferredPeerID != preferredID {
		t.Fatalf("preferred relay = %q, want preferred relay to remain sticky", snapshot.PreferredPeerID)
	}
	if snapshot.LastSwitchAt != now.Add(-10*time.Minute) {
		t.Fatalf("last switch at = %v, want unchanged", snapshot.LastSwitchAt)
	}
	if got := tracker.Preference([]ma.Multiaddr{
		ma.StringCast("/ip4/127.0.0.1/tcp/4102/p2p/" + fallbackID + "/p2p-circuit"),
	}); got.CurrentPeerID != fallbackID || got.PreferredPeerID != preferredID {
		t.Fatalf("preference = %+v", got)
	}
}

func TestLiveRelayTrackerPromotesFallbackAfterHoldDown(t *testing.T) {
	t.Parallel()

	const preferredID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"
	const fallbackID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8N"
	base := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	tracker := NewLiveRelayTracker([]peer.AddrInfo{
		{ID: peer.ID(preferredID)},
		{ID: peer.ID(fallbackID)},
	}, RelayBootstrapSnapshot{
		PeerIDs:         []string{preferredID, fallbackID},
		PreferredPeerID: preferredID,
		PreferredAt:     base.Add(-10 * time.Minute),
		LastSwitchAt:    base.Add(-10 * time.Minute),
		UpdatedAt:       base.Add(-10 * time.Minute),
	})

	now := base
	tracker.now = func() time.Time { return now }
	activeAddrs := []ma.Multiaddr{
		ma.StringCast("/ip4/127.0.0.1/tcp/4102/p2p/" + fallbackID + "/p2p-circuit"),
	}
	if _, snapshot, _ := tracker.ObserveHostAddrs(activeAddrs); snapshot.PreferredPeerID != preferredID {
		t.Fatalf("preferred relay after first miss = %q, want old preferred", snapshot.PreferredPeerID)
	}

	now = now.Add(defaultLiveRelaySwitchHold + time.Second)
	_, snapshot, changed := tracker.ObserveHostAddrs(activeAddrs)
	if !changed {
		t.Fatal("expected preferred relay switch to update snapshot")
	}
	if snapshot.PreferredPeerID != fallbackID {
		t.Fatalf("preferred relay = %q, want fallback after hold-down", snapshot.PreferredPeerID)
	}
	if snapshot.LastSwitchAt != now.UTC() {
		t.Fatalf("last switch at = %v, want %v", snapshot.LastSwitchAt, now.UTC())
	}
}
