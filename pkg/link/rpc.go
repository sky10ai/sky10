package link

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// RPCHandler dispatches skylink.* RPC methods.
type RPCHandler struct {
	node           *Node
	resolver       *Resolver
	stun           []string
	healthTracker  *RuntimeHealthTracker
	mailboxHealth  func() MailboxHealth
	netcheckMu     sync.Mutex
	lastNetcheckAt time.Time
	lastNetcheck   NetcheckResult
}

// RPCHandlerOption configures the skylink RPC handler.
type RPCHandlerOption func(*RPCHandler)

// WithSTUNServers overrides the STUN server list used by skylink.netcheck.
func WithSTUNServers(servers []string) RPCHandlerOption {
	return func(h *RPCHandler) {
		h.stun = append([]string(nil), servers...)
	}
}

// WithRuntimeHealthTracker attaches a runtime health tracker to skylink.status.
func WithRuntimeHealthTracker(tracker *RuntimeHealthTracker) RPCHandlerOption {
	return func(h *RPCHandler) {
		h.healthTracker = tracker
	}
}

// WithMailboxHealthProvider attaches mailbox summary data to skylink.status.
func WithMailboxHealthProvider(provider func() MailboxHealth) RPCHandlerOption {
	return func(h *RPCHandler) {
		h.mailboxHealth = provider
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
		return h.rpcStatus(ctx)
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
	PeerID       string        `json:"peer_id"`
	Address      string        `json:"address"`
	Mode         string        `json:"mode"`
	Addrs        []string      `json:"addrs"`
	Peers        int           `json:"peers"`
	PrivatePeers int           `json:"private_peers"`
	Health       NetworkHealth `json:"health"`
}

func (h *RPCHandler) rpcStatus(ctx context.Context) (interface{}, error, bool) {
	addrs := []string{}
	if host := h.node.Host(); host != nil {
		for _, a := range host.Addrs() {
			addrs = append(addrs, a.String())
		}
	}
	netcheck := h.cachedNetcheck(ctx)
	privatePeers := len(h.node.ConnectedPrivateNetworkPeers())
	runtime := RuntimeHealthSnapshot{}
	if h.healthTracker != nil {
		runtime = h.healthTracker.Snapshot()
	}
	mailbox := MailboxHealth{}
	if h.mailboxHealth != nil {
		mailbox = h.mailboxHealth()
	}
	return statusResult{
		PeerID:       h.node.PeerID().String(),
		Address:      h.node.Address(),
		Mode:         modeString(h.node.config.Mode),
		Addrs:        addrs,
		Peers:        len(h.node.ConnectedPeers()),
		PrivatePeers: privatePeers,
		Health: NetworkHealth{
			PreferredTransport:      preferredTransportFromNetcheck(netcheck),
			TransportDegradedReason: transportDegradedReason(netcheck),
			DeliveryDegradedReason:  deliveryDegradedReason(mailbox),
			Reachability:            runtime.Reachability,
			PublicAddr:              netcheck.PublicAddr,
			MappingVariesByServer:   netcheck.MappingVariesByServer,
			ConnectedPrivatePeers:   privatePeers,
			LastPublishedAt:         runtime.LastPublishedAt,
			LastAddressChangeAt:     runtime.LastAddressChangeAt,
			Netcheck:                netcheck,
			Mailbox:                 mailbox,
			Events:                  runtime.Events,
		},
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
		if h.healthTracker != nil {
			h.healthTracker.RecordConnect(p.Address, err)
		}
		return nil, err, true
	}
	if h.healthTracker != nil {
		h.healthTracker.RecordConnect(p.Address, nil)
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
					"peer_id":                resolved.Info.ID.String(),
					"device_pubkey":          resolved.DevicePubKey,
					"published_at":           resolved.PublishedAt,
					"expires_at":             resolved.ExpiresAt,
					"source":                 resolved.Source,
					"preferred_transport":    resolved.PreferredTransport,
					"last_success_at":        resolved.LastSuccessAt,
					"last_success_transport": resolved.LastSuccessTransport,
					"last_success_source":    resolved.LastSuccessSource,
					"last_success_addr":      resolved.LastSuccessAddr,
					"addr_scores":            resolved.AddrScores,
					"multiaddrs":             addrs,
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
		if h.healthTracker != nil {
			h.healthTracker.RecordPublish("dht", err)
		}
		return nil, err, true
	}
	if h.healthTracker != nil {
		h.healthTracker.RecordPublish("dht", nil)
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

const statusNetcheckTTL = 2 * time.Minute
const statusNetcheckTimeout = 4 * time.Second

func (h *RPCHandler) cachedNetcheck(ctx context.Context) NetcheckResult {
	h.netcheckMu.Lock()
	defer h.netcheckMu.Unlock()

	if !h.lastNetcheckAt.IsZero() && time.Since(h.lastNetcheckAt) < statusNetcheckTTL {
		return h.lastNetcheck
	}

	probeCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		probeCtx, cancel = context.WithTimeout(ctx, statusNetcheckTimeout)
	}
	defer cancel()

	h.lastNetcheck = Netcheck(probeCtx, h.stun)
	h.lastNetcheckAt = time.Now()
	return h.lastNetcheck
}
