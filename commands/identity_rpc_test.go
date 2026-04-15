package commands

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	skyid "github.com/sky10/sky10/pkg/id"
	skyjoin "github.com/sky10/sky10/pkg/join"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func TestPrivateNetworkDeviceMetadataUsesConnectedPrivatePeers(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := testSharedBundles(t)

	nodeA, err := link.New(bundleA, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodeA.SetVersion("v-test")

	nodeB, err := link.New(bundleB, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	startTestLinkNode(t, nodeA)
	startTestLinkNode(t, nodeB)
	connectTestNodes(t, nodeA, nodeB)

	waitForCondition(t, 5*time.Second, func() bool {
		return len(nodeA.ConnectedPrivateNetworkPeers()) == 1
	})

	metadata, err := privateNetworkDeviceMetadata(context.Background(), bundleA, nil, nodeA)
	if err != nil {
		t.Fatalf("privateNetworkDeviceMetadata: %v", err)
	}

	currentMeta, ok := metadata[bundleA.DevicePubKeyHex()]
	if !ok {
		t.Fatalf("missing current device metadata for %s", bundleA.DevicePubKeyHex())
	}
	if currentMeta.Version != "v-test" {
		t.Fatalf("current version = %q, want v-test", currentMeta.Version)
	}
	if len(currentMeta.Multiaddrs) == 0 {
		t.Fatal("expected current device multiaddrs")
	}

	remotePubHex := hex.EncodeToString(bundleB.Device.PublicKey)
	remoteMeta, ok := metadata[remotePubHex]
	if !ok {
		t.Fatalf("missing remote device metadata for %s", remotePubHex)
	}
	if len(remoteMeta.Multiaddrs) == 0 {
		t.Fatal("expected remote device multiaddrs from connected private peer")
	}
	if remoteMeta.LastSeen == "" {
		t.Fatal("expected remote device last_seen from connected private peer")
	}
}

func TestCreateIdentityInviteIncludesBootstrapHints(t *testing.T) {
	t.Parallel()

	bundleA, _ := testSharedBundles(t)
	nodeA, err := link.New(bundleA, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startTestLinkNode(t, nodeA)

	code, err := createIdentityInvite(context.Background(), nil, bundleA, nodeA, []string{
		"wss://relay.damus.io",
		"wss://nos.lol",
		"wss://relay.nostr.band",
	}, skyid.InviteOptions{})
	if err != nil {
		t.Fatalf("createIdentityInvite: %v", err)
	}

	invite, err := skyjoin.DecodeP2PInvite(code)
	if err != nil {
		t.Fatalf("DecodeP2PInvite: %v", err)
	}
	if invite.PeerID != nodeA.PeerID().String() {
		t.Fatalf("peer_id = %q, want %q", invite.PeerID, nodeA.PeerID())
	}
	if len(invite.Multiaddrs) == 0 {
		t.Fatal("expected invite multiaddrs")
	}
	if len(invite.NostrRelays) != 2 {
		t.Fatalf("invite relays = %v, want 2", invite.NostrRelays)
	}
}

func TestCreateIdentityInviteRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	bundleA, _ := testSharedBundles(t)
	if _, err := createIdentityInvite(context.Background(), nil, bundleA, nil, nil, skyid.InviteOptions{Mode: "unknown"}); err == nil {
		t.Fatal("createIdentityInvite() error = nil, want unsupported mode")
	}
}

func testSharedBundles(t *testing.T) (*skyid.Bundle, *skyid.Bundle) {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := skyid.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := skyid.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := skyid.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundleA, bundleB
}

func startTestLinkNode(t *testing.T, node *link.Node) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- node.Run(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for node.Host() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if node.Host() == nil {
		cancel()
		t.Fatal("node did not start")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

func connectTestNodes(t *testing.T, a, b *link.Node) {
	t.Helper()

	info := b.Host().Peerstore().PeerInfo(b.PeerID())
	info.Addrs = b.Host().Addrs()
	if err := a.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("connect nodes: %v", err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
