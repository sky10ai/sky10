package link

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestPrioritizeAddrInfoPrefersQUICWhenUDPHealthy(t *testing.T) {
	t.Parallel()

	info := testPeerAddrInfo(t, []string{
		"/ip4/203.0.113.10/tcp/4101",
		"/ip4/203.0.113.10/udp/4101/quic-v1",
	})

	got := PrioritizeAddrInfo(info, NetcheckResult{
		UDP:        true,
		PublicAddr: "203.0.113.99:55000",
	})
	if !isQUICAddr(got.Addrs[0]) {
		t.Fatalf("first addr = %s, want QUIC", got.Addrs[0])
	}
}

func TestPrioritizeAddrInfoPrefersLastSuccessfulAddr(t *testing.T) {
	t.Parallel()

	info := testPeerAddrInfo(t, []string{
		"/ip4/203.0.113.10/udp/4101/quic-v1",
		"/ip4/203.0.113.10/tcp/4101",
	})

	got, scores := PrioritizeAddrInfoWithHint(info, NetcheckResult{
		UDP:        true,
		PublicAddr: "203.0.113.99:55000",
	}, PathHint{
		LastSuccessAt:        time.Now().UTC(),
		LastSuccessTransport: "direct_tcp",
		LastSuccessAddr:      "/ip4/203.0.113.10/tcp/4101",
	})
	if !isTCPAddr(got.Addrs[0]) {
		t.Fatalf("first addr = %s, want TCP last-success path", got.Addrs[0])
	}
	if len(scores) == 0 || scores[0].Transport != "direct_tcp" {
		t.Fatalf("top score = %+v, want direct_tcp", scores)
	}
}

func TestPrioritizeAddrInfoPrefersTCPWhenUDPFlaky(t *testing.T) {
	t.Parallel()

	info := testPeerAddrInfo(t, []string{
		"/ip4/203.0.113.10/udp/4101/quic-v1",
		"/ip4/203.0.113.10/tcp/4101",
	})

	got := PrioritizeAddrInfo(info, NetcheckResult{
		UDP:                   true,
		PublicAddr:            "203.0.113.99:55000",
		MappingVariesByServer: true,
	})
	if !isTCPAddr(got.Addrs[0]) {
		t.Fatalf("first addr = %s, want TCP", got.Addrs[0])
	}
}

func TestPrioritizeAddrInfoPenalizesRecentFailures(t *testing.T) {
	t.Parallel()

	quicAddr := "/ip4/203.0.113.10/udp/4101/quic-v1"
	info := testPeerAddrInfo(t, []string{
		quicAddr,
		"/ip4/203.0.113.10/tcp/4101",
	})

	got, scores := PrioritizeAddrInfoWithHint(info, NetcheckResult{
		UDP:        true,
		PublicAddr: "203.0.113.99:55000",
	}, PathHint{
		AddrFailures: map[string]PathFailure{
			quicAddr: {Count: 2, LastAt: time.Now().UTC()},
		},
		TransportFailures: map[string]PathFailure{
			"direct_quic": {Count: 1, LastAt: time.Now().UTC()},
		},
	})
	if !isTCPAddr(got.Addrs[0]) {
		t.Fatalf("first addr = %s, want TCP after recent QUIC failures", got.Addrs[0])
	}
	if len(scores) == 0 || scores[0].Transport != "direct_tcp" {
		t.Fatalf("top score = %+v, want direct_tcp", scores)
	}
	if len(scores) < 2 || scores[1].FailureCount == 0 {
		t.Fatalf("scores = %+v, want failure count on penalized QUIC addr", scores)
	}
}

func TestPrioritizeAddrInfoAllowsRelayToWinAfterDirectFailures(t *testing.T) {
	t.Parallel()

	relayAddr := "/ip4/127.0.0.1/tcp/4101/p2p/12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M/p2p-circuit"
	info := testPeerAddrInfo(t, []string{
		"/ip4/203.0.113.10/tcp/4101",
		relayAddr,
	})

	got, scores := PrioritizeAddrInfoWithHint(info, NetcheckResult{
		UDP:                   false,
		PublicAddr:            "203.0.113.99:55000",
		MappingVariesByServer: true,
	}, PathHint{
		TransportFailures: map[string]PathFailure{
			"direct_tcp": {Count: 3, LastAt: time.Now().UTC()},
		},
	})
	if !isRelayAddr(got.Addrs[0]) {
		t.Fatalf("first addr = %s, want relay after direct failures", got.Addrs[0])
	}
	if len(scores) == 0 || scores[0].Transport != "libp2p_relay" {
		t.Fatalf("top score = %+v, want libp2p_relay", scores)
	}
}

func TestPrioritizeAddrInfoPrefersCurrentHomeRelay(t *testing.T) {
	t.Parallel()

	const preferredID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8M"
	const fallbackID = "12D3KooWQJ9m1x5v6Lq3J1s4mP4h9j9bpt5yN4B8pJxWf1dP6W8N"
	preferredRelay := "/ip4/127.0.0.1/tcp/4101/p2p/" + preferredID + "/p2p-circuit"
	otherRelay := "/ip4/127.0.0.1/tcp/4102/p2p/" + fallbackID + "/p2p-circuit"
	info := testPeerAddrInfo(t, []string{
		otherRelay,
		preferredRelay,
	})

	got, scores := PrioritizeAddrInfoWithRelayPreference(info, NetcheckResult{
		UDP:                   false,
		PublicAddr:            "203.0.113.99:55000",
		MappingVariesByServer: true,
	}, PathHint{}, LiveRelayPreference{
		CurrentPeerID: preferredID,
	})
	if got.Addrs[0].String() != preferredRelay {
		t.Fatalf("first relay addr = %s, want preferred relay", got.Addrs[0])
	}
	if len(scores) == 0 || scores[0].Multiaddr != preferredRelay {
		t.Fatalf("top score = %+v, want preferred relay first", scores)
	}
}

func TestResolverResolveAllPrioritizesPeerAddrs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bundleA := generateTestBundle(t, "nodeA")
	nodeA, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeA.Run(ctx)
	waitForHost(t, nodeA)

	membershipA, err := nodeA.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record A: %v", err)
	}
	presenceA, err := nodeA.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record A: %v", err)
	}
	pid := nodeA.PeerID().String()
	presenceA.Multiaddrs = []string{
		"/ip4/203.0.113.10/tcp/4101/p2p/" + pid,
		"/ip4/203.0.113.10/udp/4101/quic-v1/p2p/" + pid,
	}

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	resolver := NewResolver(nodeB)
	resolver.nostr = &staticDiscovery{
		membership: membershipA,
		presences:  []*PresenceRecord{presenceA},
	}
	resolver.netcheck = func(context.Context, []string) NetcheckResult {
		return NetcheckResult{
			UDP:        true,
			PublicAddr: "203.0.113.99:55000",
		}
	}

	resolution, err := resolver.ResolveAll(ctx, bundleA.Address())
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	if len(resolution.Peers) == 0 || len(resolution.Peers[0].Info.Addrs) == 0 {
		t.Fatalf("resolution = %+v, want peer addrs", resolution)
	}
	if !isQUICAddr(resolution.Peers[0].Info.Addrs[0]) {
		t.Fatalf("first resolved addr = %s, want QUIC", resolution.Peers[0].Info.Addrs[0])
	}
	if resolution.Peers[0].PreferredTransport != "direct_quic" {
		t.Fatalf("preferred transport = %q, want direct_quic", resolution.Peers[0].PreferredTransport)
	}
	if len(resolution.Peers[0].AddrScores) == 0 {
		t.Fatal("expected addr score explanations")
	}
}

func testPeerAddrInfo(t *testing.T, addrs []string) *peer.AddrInfo {
	t.Helper()

	info := &peer.AddrInfo{ID: "12D3KooWQp6KSGY7N4r7VJx4nVQ2zVQKp8S7gJ3N4Pp7vQ9QmR4A"}
	for _, raw := range addrs {
		addr, err := ma.NewMultiaddr(raw)
		if err != nil {
			t.Fatalf("NewMultiaddr(%q): %v", raw, err)
		}
		info.Addrs = append(info.Addrs, addr)
	}
	return info
}
