package link

import (
	"testing"

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

func TestPeerIDFromKey(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	id, err := PeerIDFromKey(k)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty peer ID")
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
