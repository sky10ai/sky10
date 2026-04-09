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
	stun     []string
}

// RPCHandlerOption configures the skylink RPC handler.
type RPCHandlerOption func(*RPCHandler)

// WithSTUNServers overrides the STUN server list used by skylink.netcheck.
func WithSTUNServers(servers []string) RPCHandlerOption {
	return func(h *RPCHandler) {
		h.stun = append([]string(nil), servers...)
	}
}

// NewRPCHandler creates an RPC handler for the skylink node.
func NewRPCHandler(node *Node, resolver *Resolver, opts ...RPCHandlerOption) *RPCHandler {
	h := &RPCHandler{
		node:     node,
		resolver: resolver,
		stun:     append([]string(nil), DefaultSTUNServers...),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
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
	case "skylink.netcheck":
		return h.rpcNetcheck(ctx)
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
	// Resolve address to peer info, then call.
	if h.resolver == nil {
		return nil, fmt.Errorf("resolver not configured"), true
	}
	info, err := h.resolver.Resolve(ctx, p.Address)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", p.Address, err), true
	}
	result, err := h.node.Call(ctx, info.ID, p.Method, p.Params)
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

	if h.resolver != nil {
		resolution, err := h.resolver.ResolveAll(ctx, p.Address)
		if err == nil {
			peers := make([]map[string]interface{}, 0, len(resolution.Peers))
			for _, resolved := range resolution.Peers {
				if resolved == nil || resolved.Info == nil {
					continue
				}
				addrs := make([]string, 0, len(resolved.Info.Addrs))
				for _, addr := range resolved.Info.Addrs {
					addrs = append(addrs, addr.String())
				}
				peers = append(peers, map[string]interface{}{
					"peer_id":       resolved.Info.ID.String(),
					"device_pubkey": resolved.DevicePubKey,
					"published_at":  resolved.PublishedAt,
					"expires_at":    resolved.ExpiresAt,
					"source":        resolved.Source,
					"multiaddrs":    addrs,
				})
			}
			return map[string]interface{}{
				"identity":          resolution.Identity,
				"membership_source": resolution.MembershipSource,
				"membership":        resolution.Membership,
				"peers":             peers,
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

func (h *RPCHandler) rpcNetcheck(ctx context.Context) (interface{}, error, bool) {
	return Netcheck(ctx, h.stun), nil, true
}

func (h *RPCHandler) rpcPeers() (interface{}, error, bool) {
	connected := h.node.ConnectedPeers()
	peers := make([]peerInfo, 0, len(connected))
	for _, pid := range connected {
		peers = append(peers, peerInfo{PeerID: pid.String()})
	}
	return peersResult{
		Peers: peers,
		Count: len(peers),
	}, nil, true
}
