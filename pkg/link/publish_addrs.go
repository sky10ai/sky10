package link

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// PublishedHostMultiaddrs returns the node's current host addresses with the
// /p2p/<peerID> suffix appended, reordered using a fresh STUN probe so direct
// dial hints prefer the transport that is most likely to work.
func PublishedHostMultiaddrs(ctx context.Context, n *Node) []string {
	if n == nil || n.host == nil {
		return nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, defaultSTUNProbeTimeout)
	defer cancel()
	return hostMultiaddrsForResult(n, Netcheck(probeCtx, DefaultSTUNServers))
}

func hostMultiaddrsForResult(n *Node, result NetcheckResult) []string {
	if n == nil || n.host == nil {
		return nil
	}

	info, _ := PrioritizeAddrInfoWithRelayPreference(&peer.AddrInfo{
		ID:    n.PeerID(),
		Addrs: append([]ma.Multiaddr(nil), n.host.Addrs()...),
	}, result, PathHint{}, n.liveRelayPreference())
	if info == nil {
		return nil
	}

	out := make([]string, 0, len(info.Addrs))
	pid := info.ID.String()
	for _, addr := range info.Addrs {
		out = append(out, addr.String()+"/p2p/"+pid)
	}
	return out
}
