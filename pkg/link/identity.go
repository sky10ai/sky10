// Package link provides P2P agent communication via libp2p.
package link

import (
	"crypto/ed25519"
	"fmt"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Libp2pPrivKey converts a sky10 private key to a libp2p PrivKey.
func Libp2pPrivKey(k *skykey.Key) (libcrypto.PrivKey, error) {
	if !k.IsPrivate() {
		return nil, fmt.Errorf("key has no private component")
	}
	priv, err := libcrypto.UnmarshalEd25519PrivateKey(k.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("converting ed25519 key: %w", err)
	}
	return priv, nil
}

// Libp2pPubKey converts a sky10 public key to a libp2p PubKey.
func Libp2pPubKey(pub ed25519.PublicKey) (libcrypto.PubKey, error) {
	key, err := libcrypto.UnmarshalEd25519PublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("converting ed25519 public key: %w", err)
	}
	return key, nil
}

// PeerIDFromKey derives a libp2p peer ID from a sky10 key.
func PeerIDFromKey(k *skykey.Key) (peer.ID, error) {
	pub, err := Libp2pPubKey(k.PublicKey)
	if err != nil {
		return "", err
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("deriving peer ID: %w", err)
	}
	return id, nil
}

// PeerIDFromAddress derives a libp2p peer ID from a sky10q... address.
func PeerIDFromAddress(address string) (peer.ID, error) {
	k, err := skykey.ParseAddress(address)
	if err != nil {
		return "", err
	}
	return PeerIDFromKey(k)
}

// AddressFromPeerID extracts the sky10q... address from a libp2p peer ID.
// Only works for Ed25519-based peer IDs.
func AddressFromPeerID(id peer.ID) (string, error) {
	pub, err := id.ExtractPublicKey()
	if err != nil {
		return "", fmt.Errorf("extracting public key from peer ID: %w", err)
	}
	raw, err := pub.Raw()
	if err != nil {
		return "", fmt.Errorf("extracting raw public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return "", fmt.Errorf("unexpected public key size: %d", len(raw))
	}
	k := skykey.FromPublicKey(ed25519.PublicKey(raw))
	return k.Address(), nil
}
