package link

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const defaultLiveRelaySwitchHold = 2 * time.Minute

// LiveRelayPreference is the current local relay-selection hint used when
// publishing relayed multiaddrs.
type LiveRelayPreference struct {
	CurrentPeerID   string
	PreferredPeerID string
}

// LiveRelayTracker keeps lightweight sticky relay selection state on top of
// libp2p's autorelay machinery.
type LiveRelayTracker struct {
	mu                    sync.Mutex
	now                   func() time.Time
	holdDown              time.Duration
	configured            []peer.AddrInfo
	snapshot              RelayBootstrapSnapshot
	preferredMissingSince time.Time
}

// NewLiveRelayTracker creates a sticky relay tracker seeded from persisted
// cache state.
func NewLiveRelayTracker(configured []peer.AddrInfo, snapshot RelayBootstrapSnapshot) *LiveRelayTracker {
	return &LiveRelayTracker{
		now:        time.Now,
		holdDown:   defaultLiveRelaySwitchHold,
		configured: append([]peer.AddrInfo(nil), configured...),
		snapshot:   snapshot,
	}
}

// Snapshot returns the persisted sticky relay state.
func (t *LiveRelayTracker) Snapshot() RelayBootstrapSnapshot {
	if t == nil {
		return RelayBootstrapSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshot
}

// ObserveHostAddrs updates sticky relay state from the node's currently active
// relay addresses. It returns the ordered active relay set, the updated
// snapshot, and whether the persisted snapshot changed.
func (t *LiveRelayTracker) ObserveHostAddrs(addrs []ma.Multiaddr) ([]peer.AddrInfo, RelayBootstrapSnapshot, bool) {
	if t == nil {
		active := RelayBootstrapPeersFromHostAddrs(addrs)
		return active, RelayBootstrapSnapshot{}, false
	}

	active := RelayBootstrapPeersFromHostAddrs(addrs)

	t.mu.Lock()
	defer t.mu.Unlock()

	before := t.snapshot
	now := t.now().UTC()
	t.reconcileLocked(active, now)
	return t.orderedActiveLocked(active), t.snapshot, !reflect.DeepEqual(before, t.snapshot)
}

// Preference returns the current active relay preference derived from the
// latest active relay set and sticky home-relay state.
func (t *LiveRelayTracker) Preference(addrs []ma.Multiaddr) LiveRelayPreference {
	if t == nil {
		return LiveRelayPreference{}
	}
	active := RelayBootstrapPeersFromHostAddrs(addrs)

	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.currentPeerIDLocked(active)
	return LiveRelayPreference{
		CurrentPeerID:   current,
		PreferredPeerID: strings.TrimSpace(t.snapshot.PreferredPeerID),
	}
}

// Health returns operator-facing live relay state using the tracker's sticky
// relay preference.
func (t *LiveRelayTracker) Health(addrs []ma.Multiaddr) LiveRelayHealth {
	if t == nil {
		return LiveRelayHealth{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return LiveRelayHealthFromHost(addrs, t.configured, t.snapshot)
}

func (t *LiveRelayTracker) reconcileLocked(active []peer.AddrInfo, now time.Time) {
	if len(active) == 0 {
		if t.snapshot.PreferredPeerID != "" && t.preferredMissingSince.IsZero() {
			t.preferredMissingSince = now
		}
		return
	}

	current := t.currentPeerIDLocked(active)
	if current == "" {
		return
	}

	if t.snapshot.PreferredPeerID == "" {
		t.snapshot.PreferredPeerID = current
		t.snapshot.PreferredAt = now
		t.snapshot.LastSwitchAt = now
		t.preferredMissingSince = time.Time{}
	} else if relayPeerActive(active, t.snapshot.PreferredPeerID) {
		t.preferredMissingSince = time.Time{}
	} else {
		if t.preferredMissingSince.IsZero() {
			t.preferredMissingSince = now
		}
		if now.Sub(t.preferredMissingSince) >= t.holdDown && current != t.snapshot.PreferredPeerID {
			t.snapshot.PreferredPeerID = current
			t.snapshot.PreferredAt = now
			t.snapshot.LastSwitchAt = now
			t.preferredMissingSince = time.Time{}
		}
	}

	t.snapshot.PeerIDs = peerIDsFromInfos(t.orderedActiveLocked(active))
	t.snapshot.UpdatedAt = now
}

func (t *LiveRelayTracker) orderedActiveLocked(active []peer.AddrInfo) []peer.AddrInfo {
	current := t.currentPeerIDLocked(active)
	return orderRelayInfos(active, current, t.snapshot.PeerIDs, peerIDsFromInfos(t.configured))
}

func (t *LiveRelayTracker) currentPeerIDLocked(active []peer.AddrInfo) string {
	if len(active) == 0 {
		return ""
	}
	if relayPeerActive(active, t.snapshot.PreferredPeerID) {
		return t.snapshot.PreferredPeerID
	}
	ordered := orderRelayInfos(active, "", t.snapshot.PeerIDs, peerIDsFromInfos(t.configured))
	if len(ordered) == 0 {
		return ""
	}
	return ordered[0].ID.String()
}
