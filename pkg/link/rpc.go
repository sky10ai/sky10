package link

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches skylink.* RPC methods.
type RPCHandler struct {
	node     *Node
	resolver *Resolver
}

// NewRPCHandler creates an RPC handler for the skylink node.
func NewRPCHandler(node *Node, resolver *Resolver) *RPCHandler {
	return &RPCHandler{node: node, resolver: resolver}
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
	case "skylink.connect":
		return h.rpcConnect(ctx, params)
	case "skylink.call":
		return h.rpcCall(ctx, params)
	case "skylink.resolve":
		return h.rpcResolve(ctx, params)
	case "skylink.publish":
		return h.rpcPublish(ctx)
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

type connectParams struct {
	Address string `json:"address"`
}

func (h *RPCHandler) rpcConnect(ctx context.Context, params json.RawMessage) (interface{}, error, bool) {
	var p connectParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}
	if h.resolver == nil {
		return nil, fmt.Errorf("resolver not configured"), true
	}
	if err := h.resolver.Connect(ctx, p.Address); err != nil {
		return nil, err, true
	}
	return map[string]bool{"connected": true}, nil, true
}

type callParams struct {
	Address string          `json:"address"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (h *RPCHandler) rpcCall(ctx context.Context, params json.RawMessage) (interface{}, error, bool) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}
	pid, err := PeerIDFromAddress(p.Address)
	if err != nil {
		return nil, err, true
	}
	result, err := h.node.Call(ctx, pid, p.Method, p.Params)
	if err != nil {
		return nil, err, true
	}
	return json.RawMessage(result), nil, true
}

type resolveParams struct {
	Address string `json:"address"`
}

func (h *RPCHandler) rpcResolve(ctx context.Context, params json.RawMessage) (interface{}, error, bool) {
	var p resolveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}

	// Try agent record from DHT first.
	if h.node.dht != nil {
		rec, err := h.node.ResolveRecord(ctx, p.Address)
		if err == nil {
			return rec, nil, true
		}
	}

	// Fall back to resolver for address info.
	if h.resolver != nil {
		info, err := h.resolver.Resolve(ctx, p.Address)
		if err == nil {
			addrs := make([]string, 0, len(info.Addrs))
			for _, a := range info.Addrs {
				addrs = append(addrs, a.String())
			}
			return map[string]interface{}{
				"peer_id":    info.ID.String(),
				"multiaddrs": addrs,
			}, nil, true
		}
	}

	return nil, fmt.Errorf("could not resolve %s", p.Address), true
}

func (h *RPCHandler) rpcPublish(ctx context.Context) (interface{}, error, bool) {
	if err := h.node.PublishRecord(ctx); err != nil {
		return nil, err, true
	}
	return map[string]bool{"published": true}, nil, true
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
