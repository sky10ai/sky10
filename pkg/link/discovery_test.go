package link

import (
	"context"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

// TestResolverNostrFallback verifies that when there's no S3 backend and
// no DHT, the resolver falls back to Nostr relay discovery.
//
// Two nodes publish their multiaddrs to Nostr, then resolve each other
// using the Nostr layer. This is a live test against public relays.
func TestResolverNostrFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Nostr integration test in short mode")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	relays := []string{"wss://nos.lol", "wss://relay.nostr.band"}

	// --- Node A ---
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
	t.Logf("published A: %s", bundleA.Address())

	// Give relays time to index.
	time.Sleep(2 * time.Second)

	// --- Node B tries to resolve A via Nostr (no S3, no DHT) ---
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

	// Verify we got a valid peer ID that matches A.
	if info.ID != nodeA.PeerID() {
		t.Errorf("resolved peer ID = %s, want %s", info.ID, nodeA.PeerID())
	}

	// Actually connect to verify the resolved addrs work.
	if err := nodeB.Host().Connect(ctx, *info); err != nil {
		t.Fatalf("connect B→A: %v", err)
	}
	t.Logf("connected B→A via Nostr-resolved addrs")
}

// TestResolverS3ThenNostr verifies the resolution order:
// S3 is tried first, then Nostr when S3 doesn't have the device.
func TestResolverS3ThenNostr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Nostr integration test in short mode")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	relays := []string{"wss://nos.lol", "wss://relay.nostr.band"}

	bundleA := generateTestBundle(t, "nodeA")
	nodeA, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeA.Run(ctx)
	waitForHost(t, nodeA)

	nostr := NewNostrDiscovery(relays, nil)
	membershipA, err := nodeA.CurrentMembershipRecord()
	if err != nil {
		t.Fatalf("membership record A: %v", err)
	}
	presenceA, err := nodeA.CurrentPresenceRecord(0)
	if err != nil {
		t.Fatalf("presence record A: %v", err)
	}
	if err := nostr.PublishMembership(ctx, bundleA.Identity, membershipA); err != nil {
		t.Fatalf("publish membership A: %v", err)
	}
	if err := nostr.PublishPresence(ctx, bundleA.Device, presenceA); err != nil {
		t.Fatalf("publish presence A: %v", err)
	}
	time.Sleep(2 * time.Second)

	// Resolver with no backend (S3 layer skipped) + Nostr.
	bundleB := generateTestBundle(t, "nodeB")
	nodeB, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeB.Run(ctx)
	waitForHost(t, nodeB)

	// No WithBackend — S3 layer is nil. Should fall through to Nostr.
	resolver := NewResolver(nodeB, WithNostr(relays))

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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	deadline := time.Now().Add(5 * time.Second)
	for {
		err = nodeA.PublishRecord(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publish DHT providers: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	resolver := NewResolver(nodeB)

	var resolution *Resolution
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resolution, err = resolver.ResolveAll(ctx, bundleA.Address())
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("resolve via DHT provider discovery: %v", err)
	}
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
