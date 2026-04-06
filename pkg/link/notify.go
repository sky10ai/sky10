package link

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// SyncNotifyProtocol is the libp2p protocol ID for private sync notifications.
// These are direct point-to-point streams to own devices — never GossipSub.
const SyncNotifyProtocol = protocol.ID("/sky10/sync-notify/1.0.0")

// NotifyOwn sends a sync notification to all connected own-device peers
// via direct streams. This is the private sync path — it never touches
// GossipSub and is invisible to network peers.
func (n *Node) NotifyOwn(ctx context.Context, topic string) error {
	if n.host == nil {
		return fmt.Errorf("node not running")
	}

	peers := n.ConnectedPrivateNetworkPeers()
	if len(peers) == 0 {
		return nil
	}

	for _, pid := range peers {
		go n.sendPoke(ctx, pid, topic)
	}
	return nil
}

// sendPoke sends a sync notification to a single peer via direct stream.
func (n *Node) sendPoke(ctx context.Context, target peer.ID, topic string) {
	s, err := n.host.NewStream(ctx, target, SyncNotifyProtocol)
	if err != nil {
		n.logger.Debug("sync notify failed",
			"peer", target.String(),
			"topic", topic,
			"error", err,
		)
		return
	}
	defer s.Close()

	_, err = s.Write([]byte(topic))
	if err != nil {
		n.logger.Debug("sync notify write failed",
			"peer", target.String(),
			"topic", topic,
			"error", err,
		)
	}
}
