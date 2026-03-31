package link

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// Gater implements libp2p's ConnectionGater interface. In private mode,
// it rejects all connections except from peers in the allowlist (own
// devices). In network mode, it allows authorized external peers.
type Gater struct {
	mu      sync.RWMutex
	allowed map[peer.ID]bool // explicitly allowed peer IDs
	mode    Mode
}

// NewGater creates a connection gater.
func NewGater(mode Mode) *Gater {
	return &Gater{
		allowed: make(map[peer.ID]bool),
		mode:    mode,
	}
}

// Allow adds a peer to the allowlist.
func (g *Gater) Allow(id peer.ID) {
	g.mu.Lock()
	g.allowed[id] = true
	g.mu.Unlock()
}

// Revoke removes a peer from the allowlist.
func (g *Gater) Revoke(id peer.ID) {
	g.mu.Lock()
	delete(g.allowed, id)
	g.mu.Unlock()
}

// IsAllowed returns whether a peer is in the allowlist.
func (g *Gater) IsAllowed(id peer.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allowed[id]
}

// AllowedPeers returns all allowed peer IDs.
func (g *Gater) AllowedPeers() []peer.ID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]peer.ID, 0, len(g.allowed))
	for id := range g.allowed {
		out = append(out, id)
	}
	return out
}

// InterceptPeerDial is called before dialing a peer.
func (g *Gater) InterceptPeerDial(p peer.ID) bool {
	if g.mode == Network {
		return true // network mode: allow all outbound dials
	}
	return g.IsAllowed(p)
}

// InterceptAddrDial is called before dialing a specific address.
func (g *Gater) InterceptAddrDial(id peer.ID, addr ma.Multiaddr) bool {
	return g.InterceptPeerDial(id)
}

// InterceptAccept is called when accepting an inbound connection.
func (g *Gater) InterceptAccept(addrs network.ConnMultiaddrs) bool {
	return true // can't know peer ID yet, defer to InterceptSecured
}

// InterceptSecured is called after the security handshake, when we
// know the remote peer's identity.
func (g *Gater) InterceptSecured(dir network.Direction, id peer.ID, addrs network.ConnMultiaddrs) bool {
	if g.mode == Network {
		return true // network mode: accept all authenticated peers
	}
	// Private mode: only allow peers in the allowlist.
	return g.IsAllowed(id)
}

// InterceptUpgraded is called after the connection is fully upgraded.
func (g *Gater) InterceptUpgraded(conn network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}
