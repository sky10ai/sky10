package link

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

type staticDiscovery struct {
	membership *MembershipRecord
	presences  []*PresenceRecord
}

func (d *staticDiscovery) QueryMembership(_ context.Context, identity string) (*MembershipRecord, error) {
	if d.membership == nil || d.membership.Identity != identity {
		return nil, fmt.Errorf("no membership record for %s", identity)
	}
	rec := *d.membership
	return &rec, nil
}

func (d *staticDiscovery) QueryPresenceAll(_ context.Context, identity string) ([]*PresenceRecord, error) {
	if d.membership == nil || d.membership.Identity != identity {
		return nil, fmt.Errorf("no presence records for %s", identity)
	}
	out := make([]*PresenceRecord, 0, len(d.presences))
	for _, rec := range d.presences {
		if rec == nil || rec.Identity != identity {
			continue
		}
		candidate := *rec
		out = append(out, &candidate)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no presence records for %s", identity)
	}
	return out, nil
}

func liveNostrRelays(t *testing.T) []string {
	t.Helper()

	if os.Getenv("SKY10_TEST_LIVE_NOSTR") == "" {
		t.Skip("skipping live Nostr relay test; set SKY10_TEST_LIVE_NOSTR=1 to enable")
	}
	if relays := strings.TrimSpace(os.Getenv("SKY10_TEST_LIVE_NOSTR_RELAYS")); relays != "" {
		var out []string
		for _, relay := range strings.Split(relays, ",") {
			relay = strings.TrimSpace(relay)
			if relay != "" {
				out = append(out, relay)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"wss://nos.lol", "wss://relay.nostr.band"}
}

func resolveWithin(resolver *Resolver, address string, timeout time.Duration) (*Resolution, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return resolver.ResolveAll(ctx, address)
}

func publishRecordWithin(node *Node, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return node.PublishRecord(ctx)
}

// TestResolverNostrFallback verifies that when there's no S3 backend and
// no DHT, the resolver falls back to Nostr discovery data.
func TestResolverNostrFallback(t *testing.T) {
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

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeB.Run(ctx)
	waitForHost(t, nodeB)

	resolver := NewResolver(nodeB)
	resolver.nostr = &staticDiscovery{
		membership: membershipA,
		presences:  []*PresenceRecord{presenceA},
	}

	info, err := resolver.Resolve(ctx, bundleA.Address())
	if err != nil {
		t.Fatalf("resolve A via Nostr: %v", err)
	}

	// Verify we got a valid peer ID that matches A.
	if info.ID != nodeA.PeerID() {
		t.Errorf("resolved peer ID = %s, want %s", info.ID, nodeA.PeerID())
	}

	// Actually connect to verify the resolved addrs work.
	if err := nodeB.Host().Connect(ctx, *info); err != nil {
		t.Fatalf("connect B→A: %v", err)
	}
}

// TestResolverS3ThenNostr verifies the resolution order:
// S3 is tried first, then Nostr when S3 doesn't have the device.
func TestResolverS3ThenNostr(t *testing.T) {
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

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeB.Run(ctx)
	waitForHost(t, nodeB)

	// No backend — DHT is unavailable in private mode, so this should resolve
	// entirely through the Nostr-style fallback discovery source.
	resolver := NewResolver(nodeB)
	resolver.nostr = &staticDiscovery{
		membership: membershipA,
		presences:  []*PresenceRecord{presenceA},
	}

	info, err := resolver.Resolve(ctx, bundleA.Address())
	if err != nil {
		t.Fatalf("resolve should succeed via Nostr fallback: %v", err)
	}
	if info.ID != nodeA.PeerID() {
		t.Errorf("peer ID mismatch")
	}
}

func TestResolverDHTProviderDiscovery(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bundleA := generateTestBundle(t, "nodeA")
	nodeA, err := New(bundleA, Config{Mode: Network}, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterPrivateNetworkHandlers(nodeA)
	startTestNode(t, nodeA)

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Network}, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterPrivateNetworkHandlers(nodeB)
	startTestNode(t, nodeB)

	if err := nodeB.Host().Connect(ctx, addrInfo(t, nodeA)); err != nil {
		t.Fatalf("seed DHT connectivity: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool {
		err = publishRecordWithin(nodeA, 2*time.Second)
		return err == nil
	}, func() string {
		return fmt.Sprintf("publish DHT providers: %v", err)
	})

	resolver := NewResolver(nodeB)

	var resolution *Resolution
	waitFor(t, 10*time.Second, func() bool {
		resolution, err = resolveWithin(resolver, bundleA.Address(), 2*time.Second)
		return err == nil
	}, func() string {
		return fmt.Sprintf("resolve via DHT provider discovery: %v", err)
	})
	if resolution.MembershipSource != "dht" {
		t.Fatalf("membership source = %q, want dht", resolution.MembershipSource)
	}
	if len(resolution.Peers) != 1 {
		t.Fatalf("peer count = %d, want 1", len(resolution.Peers))
	}
	if resolution.Peers[0].Info == nil || resolution.Peers[0].Info.ID != nodeA.PeerID() {
		t.Fatalf("resolved peer ID = %v, want %s", resolution.Peers[0].Info, nodeA.PeerID())
	}
	if resolution.Peers[0].Source != "dht" {
		t.Fatalf("peer source = %q, want dht", resolution.Peers[0].Source)
	}
}

func TestResolverNostrFallbackLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Nostr relay test in short mode")
	}
	relays := liveNostrRelays(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bundleA := generateTestBundle(t, "nodeA")
	nodeA, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeA.Run(ctx)
	waitForHost(t, nodeA)

	nostrA := NewNostrDiscovery(relays, nil)
	membershipA, err := nodeA.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record A: %v", err)
	}
	presenceA, err := nodeA.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record A: %v", err)
	}
	if err := nostrA.PublishMembership(ctx, bundleA.Identity, membershipA); err != nil {
		t.Fatalf("publish membership A: %v", err)
	}
	if err := nostrA.PublishPresence(ctx, bundleA.Device, presenceA); err != nil {
		t.Fatalf("publish presence A: %v", err)
	}

	time.Sleep(3 * time.Second)

	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeB.Run(ctx)
	waitForHost(t, nodeB)

	resolver := NewResolver(nodeB, WithNostr(relays))
	info, err := resolver.Resolve(ctx, bundleA.Address())
	if err != nil {
		t.Fatalf("resolve A via Nostr: %v", err)
	}
	if info.ID != nodeA.PeerID() {
		t.Fatalf("resolved peer ID = %s, want %s", info.ID, nodeA.PeerID())
	}
	if err := nodeB.Host().Connect(ctx, *info); err != nil {
		t.Fatalf("connect B→A: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool, failure func() string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			if failure != nil {
				t.Fatal(failure())
			}
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func generateTestBundle(t *testing.T, name string) *id.Bundle {
	t.Helper()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()
	manifest := id.NewManifest(identity)
	manifest.AddDevice(device.PublicKey, name)
	manifest.Sign(identity.PrivateKey)
	bundle, err := id.New(identity, device, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func waitForHost(t *testing.T, n *Node) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
