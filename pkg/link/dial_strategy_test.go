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
