package join

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/link"
)

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
