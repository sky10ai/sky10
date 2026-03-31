package link

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	skykey "github.com/sky10/sky10/pkg/key"
)

func TestLibp2pPrivKey(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := Libp2pPrivKey(k)
	if err != nil {
		t.Fatal(err)
	}
	if priv == nil {
		t.Fatal("expected non-nil private key")
	}
}

func TestLibp2pPrivKeyPublicOnly(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pubOnly := skykey.FromPublicKey(k.PublicKey)
	_, err = Libp2pPrivKey(pubOnly)
	if err == nil {
		t.Fatal("expected error for public-only key")
	}
}

func TestPeerIDRoundTrip(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	// key -> peer ID -> address -> peer ID
	id1, err := PeerIDFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := AddressFromPeerID(id1)
	if err != nil {
		t.Fatal(err)
	}
	if addr != k.Address() {
		t.Fatalf("address mismatch: got %s, want %s", addr, k.Address())
	}
	id2, err := PeerIDFromAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("peer ID mismatch: %s != %s", id1, id2)
	}
}

func TestPeerIDFromAddress(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	id, err := PeerIDFromAddress(k.Address())
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty peer ID")
	}
}

func TestPeerIDFromInvalidAddress(t *testing.T) {
	t.Parallel()
	_, err := PeerIDFromAddress("invalid")
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestDifferentKeysProduceDifferentPeerIDs(t *testing.T) {
	t.Parallel()
	k1, _ := skykey.Generate()
	k2, _ := skykey.Generate()
	id1, _ := PeerIDFromKey(k1)
	id2, _ := PeerIDFromKey(k2)
	if id1 == id2 {
		t.Fatal("expected different peer IDs for different keys")
	}
}

func TestPeerIDMatchesPublicKey(t *testing.T) {
	t.Parallel()
	k, _ := skykey.Generate()
	id, _ := PeerIDFromKey(k)
	pub, _ := Libp2pPubKey(k.PublicKey)
	if !id.MatchesPublicKey(pub) {
		t.Fatal("peer ID does not match public key")
	}
}

func TestAddressFromPeerIDInvalidID(t *testing.T) {
	t.Parallel()
	_, err := AddressFromPeerID(peer.ID("garbage"))
	if err == nil {
		t.Fatal("expected error for invalid peer ID")
	}
}
