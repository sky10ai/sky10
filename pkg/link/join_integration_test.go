package link

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

// TestP2PJoinHandshake verifies the full P2P join flow between two libp2p
// nodes: inviter generates invite, joiner connects, inviter auto-approves
// and sends identity + namespace keys.
func TestP2PJoinHandshake(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- Inviter (Device A) ---
	inviterBundle := generateBundle(t, "inviter")
	inviterNode, err := New(inviterBundle, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go inviterNode.Run(ctx)
	waitForHost(t, inviterNode)

	// Generate a test namespace key the inviter will share.
	testNSKey, _ := skykey.GenerateSymmetricKey()

	joinHandler := NewJoinHandler(inviterBundle, nil, nil) // auto-approve
	joinHandler.SetNSKeyProvider(func() []NSKey {
		return []NSKey{{Namespace: "default", Key: testNSKey}}
	})
	inviterNode.Host().SetStreamHandler(JoinProtocol, joinHandler.HandleStream)

	// --- Joiner (Device B) ---
	joinerKey, _ := skykey.Generate()
	joinerBundle := generateBundle(t, "joiner")
	// Use the joiner's own key as device key.
	joinerBundle.Device = joinerKey
	joinerNode, err := New(joinerBundle, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go joinerNode.Run(ctx)
	waitForHost(t, joinerNode)

	// Create invite pointing directly at inviter's addrs (skip Nostr for test).
	invite := &P2PInvite{
		Address:  inviterBundle.Address(),
		InviteID: "test-invite-123",
	}

	// Connect joiner directly to inviter (simulate Nostr discovery).
	inviterAddrs := inviterNode.Host().Addrs()
	inviterInfo := inviterNode.Host().Peerstore().PeerInfo(inviterNode.PeerID())
	if err := joinerNode.Host().Connect(ctx, inviterInfo); err != nil {
		// Direct connect with full addr info.
		t.Logf("direct connect attempt: %v (addrs: %v)", err, inviterAddrs)
	}

	// Request join.
	resp, err := RequestJoin(ctx, joinerNode, nil, invite,
		joinerKey.Address(), "test-joiner")
	if err != nil {
		t.Fatalf("RequestJoin: %v", err)
	}

	if !resp.Approved {
		t.Fatalf("expected approval, got error: %s", resp.Error)
	}

	// Verify identity key was sent.
	var wrappedIdentity []byte
	if err := json.Unmarshal(resp.IdentityKey, &wrappedIdentity); err != nil {
		t.Fatalf("unmarshal identity key: %v", err)
	}
	identityPriv, err := skykey.UnwrapKey(wrappedIdentity, joinerKey.PrivateKey)
	if err != nil {
		t.Fatalf("unwrap identity key: %v", err)
	}
	// Joiner should now have the inviter's identity private key.
	if !bytes.Equal(inviterBundle.Identity.PrivateKey, identityPriv) {
		t.Errorf("identity key mismatch: got %d bytes, want %d bytes",
			len(identityPriv), len(inviterBundle.Identity.PrivateKey))
	}

	// Verify manifest contains both devices.
	var manifest id.DeviceManifest
	if err := json.Unmarshal(resp.Manifest, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !manifest.HasDevice(joinerKey.PublicKey) {
		t.Error("manifest should contain joiner's device")
	}

	// Verify namespace keys were sent.
	if len(resp.NSKeys) != 1 {
		t.Fatalf("expected 1 namespace key, got %d", len(resp.NSKeys))
	}
	if resp.NSKeys[0].Namespace != "default" {
		t.Errorf("namespace = %q, want default", resp.NSKeys[0].Namespace)
	}
	// Unwrap and verify.
	nsKey, err := skykey.UnwrapKey(resp.NSKeys[0].Wrapped, inviterBundle.Identity.PrivateKey)
	if err != nil {
		t.Fatalf("unwrap ns key: %v", err)
	}
	if string(nsKey) != string(testNSKey) {
		t.Error("namespace key mismatch")
	}
}

func generateBundle(t *testing.T, name string) *id.Bundle {
	t.Helper()
	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	device, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	manifest := id.NewManifest(identity)
	manifest.AddDevice(device.PublicKey, name)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
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
			t.Fatal("host did not start in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
