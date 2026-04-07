package join

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/sky10/sky10/pkg/link"
)

// InviteAddrInfo returns a direct libp2p address hint from the invite if one
// was included.
func InviteAddrInfo(invite *P2PInvite) (*peer.AddrInfo, error) {
	if invite == nil {
		return nil, fmt.Errorf("invite is nil")
	}
	if invite.PeerID == "" || len(invite.Multiaddrs) == 0 {
		return nil, fmt.Errorf("invite does not include direct dial hints")
	}

	pid, err := peer.Decode(invite.PeerID)
	if err != nil {
		return nil, fmt.Errorf("decoding invite peer ID: %w", err)
	}

	info := &peer.AddrInfo{ID: pid}
	for _, raw := range invite.Multiaddrs {
		addr, err := ma.NewMultiaddr(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing invite multiaddr: %w", err)
		}
		next, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("parsing invite peer multiaddr: %w", err)
		}
		if next.ID != pid {
			return nil, fmt.Errorf("invite multiaddr peer ID mismatch")
		}
		info.Addrs = append(info.Addrs, next.Addrs...)
	}
	return info, nil
}

// ConnectViaInvite seeds the host peerstore from the invite's direct dial
// hints and attempts to establish a direct libp2p connection.
func ConnectViaInvite(ctx context.Context, h host.Host, invite *P2PInvite) (*peer.AddrInfo, error) {
	info, err := InviteAddrInfo(invite)
	if err != nil {
		return nil, err
	}
	if err := h.Connect(ctx, *info); err != nil {
		return nil, fmt.Errorf("connecting via invite dial hints: %w", err)
	}
	return info, nil
}

// RequestP2PJoin opens a join stream to the target peer and requests to
// join. Blocks until the inviter responds (approved or denied).
//
// If targetPeer is empty, the first connected peer is used.
func RequestP2PJoin(ctx context.Context, h host.Host, targetPeer peer.ID, invite *P2PInvite, devicePubKey, deviceName string) (*Response, error) {
	if targetPeer == "" {
		peers := h.Network().Peers()
		if len(peers) == 0 {
			return nil, fmt.Errorf("no connected peers")
		}
		targetPeer = peers[0]
	}

	s, err := h.NewStream(ctx, targetPeer, Protocol)
	if err != nil {
		return nil, fmt.Errorf("opening join stream: %w", err)
	}
	defer s.Close()

	req := Request{
		InviteID:     invite.InviteID,
		DevicePubKey: devicePubKey,
		DeviceName:   deviceName,
	}
	params, _ := json.Marshal(req)

	if err := link.WriteMessage(s, &link.Message{
		ID:     invite.InviteID,
		Method: "join",
		Params: params,
	}); err != nil {
		return nil, fmt.Errorf("sending join request: %w", err)
	}
	s.CloseWrite()

	respMsg, err := link.ReadMessage(s)
	if err != nil {
		return nil, fmt.Errorf("reading join response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respMsg.Result, &resp); err != nil {
		return nil, fmt.Errorf("parsing join response: %w", err)
	}
	return &resp, nil
}
