package link

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

func TestMembershipRecordRoundTrip(t *testing.T) {
	t.Parallel()

	bundle := generateTestBundle(t, "laptop")
	phone, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Manifest.AddDevice(phone.PublicKey, "phone")
	if err := bundle.Manifest.Sign(bundle.Identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	record := membershipRecordFromManifest(bundle.Manifest)
	if record == nil {
		t.Fatal("expected membership record")
	}
	if err := record.Sign(bundle.Identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	if err := record.Validate(membershipDHTKey(bundle.Address())); err != nil {
		t.Fatalf("validate membership record: %v", err)
	}

	manifest, err := record.ToManifest(bundle.Identity)
	if err != nil {
		t.Fatalf("membership record to manifest: %v", err)
	}
	if len(manifest.Devices) != 2 {
		t.Fatalf("device count = %d, want 2", len(manifest.Devices))
	}
	if !manifest.HasDevice(bundle.Device.PublicKey) {
		t.Fatal("round-tripped manifest missing original device")
	}
	if !manifest.HasDevice(phone.PublicKey) {
		t.Fatal("round-tripped manifest missing phone device")
	}
}

func TestPresenceRecordValidate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bundle := generateTestBundle(t, "node")
	node, err := New(bundle, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go node.Run(ctx)
	waitForHost(t, node)

	record, err := node.CurrentPresenceRecord(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.Validate(presenceDHTKey(bundle.Address(), bundle.DevicePubKeyHex())); err != nil {
		t.Fatalf("validate presence record: %v", err)
	}

	record.PeerID = "12D3KooWbogus"
	if err := record.Validate(presenceDHTKey(bundle.Address(), bundle.DevicePubKeyHex())); err == nil {
		t.Fatal("expected invalid presence record when peer ID does not match device key")
	}
}

func TestSelectBestMembershipPrefersNewerRecord(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	older := &MembershipRecord{
		Schema:    membershipSchema,
		Identity:  identity.Address(),
		Revision:  1,
		UpdatedAt: time.Unix(100, 0).UTC(),
		Devices:   []MembershipDevice{{PublicKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "old"}},
	}
	if err := older.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	newer := &MembershipRecord{
		Schema:    membershipSchema,
		Identity:  identity.Address(),
		Revision:  2,
		UpdatedAt: time.Unix(200, 0).UTC(),
		Devices:   []MembershipDevice{{PublicKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "new"}},
	}
	if err := newer.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	best := selectBestMembership(older, newer)
	if best != newer {
		t.Fatal("expected newer membership record to win")
	}
}

func TestResolverResolveMembershipUsesLocalCandidate(t *testing.T) {
	t.Parallel()

	bundle := generateTestBundle(t, "node")
	node, err := New(bundle, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(node)

	record, source, err := resolver.ResolveMembership(context.Background(), bundle.Address())
	if err != nil {
		t.Fatal(err)
	}
	if source != "local" {
		t.Fatalf("source = %q, want local", source)
	}
	if err := record.Validate(membershipDHTKey(bundle.Address())); err != nil {
		t.Fatalf("validate local membership candidate: %v", err)
	}
}

func TestMembershipRecordCanBeAppliedToBundle(t *testing.T) {
	t.Parallel()

	bundle := generateTestBundle(t, "node")
	other, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bundle.Manifest.AddDevice(other.PublicKey, "other")
	if err := bundle.Manifest.Sign(bundle.Identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	record := membershipRecordFromManifest(bundle.Manifest)
	if err := record.Sign(bundle.Identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	manifest, err := record.ToManifest(bundle.Identity)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Manifest = manifest
	if _, err := id.New(bundle.Identity, bundle.Device, bundle.Manifest); err != nil {
		t.Fatalf("expected bundle rebuilt from membership cache to validate: %v", err)
	}
}

func TestFetchMembershipRecordFromPeer(t *testing.T) {
	t.Parallel()

	nodeA, err := New(generateTestBundle(t, "nodeA"), Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterPrivateNetworkHandlers(nodeA)
	startTestNode(t, nodeA)

	nodeB, err := New(generateTestBundle(t, "nodeB"), Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startTestNode(t, nodeB)

	rec, err := nodeB.FetchMembershipRecord(context.Background(), addrInfo(t, nodeA), nodeA.Address())
	if err != nil {
		t.Fatalf("fetch membership record: %v", err)
	}
	if err := rec.Validate(membershipDHTKey(nodeA.Address())); err != nil {
		t.Fatalf("validate fetched membership record: %v", err)
	}
	if !slices.ContainsFunc(rec.Devices, func(device MembershipDevice) bool {
		return device.PublicKey == nodeA.Bundle().DevicePubKeyHex()
	}) {
		t.Fatal("fetched membership record missing nodeA device")
	}
}
