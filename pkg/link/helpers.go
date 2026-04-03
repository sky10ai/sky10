package link

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	skykey "github.com/sky10/sky10/pkg/key"
)

// HostMultiaddrs returns the node's listen addresses with the /p2p/<peerID>
// suffix appended, ready for publishing to discovery services.
func HostMultiaddrs(n *Node) []string {
	if n.host == nil {
		return nil
	}
	var addrs []string
	pid := n.PeerID().String()
	for _, a := range n.host.Addrs() {
		addrs = append(addrs, a.String()+"/p2p/"+pid)
	}
	return addrs
}

// NostrSecretKey derives a Nostr (secp256k1) secret key from a sky10 key.
// The derivation is deterministic: same device key always produces the same
// Nostr key. Returns a hex-encoded 32-byte string.
func NostrSecretKey(k *skykey.Key) string {
	mac := hmac.New(sha256.New, k.PrivateKey[:32])
	mac.Write([]byte("sky10-nostr"))
	return hex.EncodeToString(mac.Sum(nil))
}
