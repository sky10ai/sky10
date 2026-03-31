package link

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches skylink.* RPC methods.
type RPCHandler struct {
	node *Node
}

// NewRPCHandler creates an RPC handler for the skylink node.
func NewRPCHandler(node *Node) *RPCHandler {
	return &RPCHandler{node: node}
}

// Dispatch handles skylink.* methods.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "skylink.") {
		return nil, nil, false
	}

	switch method {
	case "skylink.status":
		return h.rpcStatus()
	case "skylink.peers":
		return h.rpcPeers()
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

type statusResult struct {
	PeerID  string   `json:"peer_id"`
	Address string   `json:"address"`
	Mode    string   `json:"mode"`
	Addrs   []string `json:"addrs"`
	Peers   int      `json:"peers"`
}

func (h *RPCHandler) rpcStatus() (interface{}, error, bool) {
	addrs := []string{}
	if host := h.node.Host(); host != nil {
		for _, a := range host.Addrs() {
			addrs = append(addrs, a.String())
		}
	}
	return statusResult{
		PeerID:  h.node.PeerID().String(),
		Address: h.node.Address(),
		Mode:    modeString(h.node.config.Mode),
		Addrs:   addrs,
		Peers:   len(h.node.ConnectedPeers()),
	}, nil, true
}

type peerInfo struct {
	PeerID  string `json:"peer_id"`
	Address string `json:"address,omitempty"`
}

type peersResult struct {
	Peers []peerInfo `json:"peers"`
	Count int        `json:"count"`
}

func (h *RPCHandler) rpcPeers() (interface{}, error, bool) {
	connected := h.node.ConnectedPeers()
	peers := make([]peerInfo, 0, len(connected))
	for _, pid := range connected {
		info := peerInfo{PeerID: pid.String()}
		if addr, err := AddressFromPeerID(pid); err == nil {
			info.Address = addr
		}
		peers = append(peers, info)
	}
	return peersResult{
		Peers: peers,
		Count: len(peers),
	}, nil, true
}
