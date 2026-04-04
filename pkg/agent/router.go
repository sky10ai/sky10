package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/link"
)

const peerQueryTimeout = 3 * time.Second

// Router dispatches agent calls locally or to remote devices via skylink.
type Router struct {
	registry *Registry
	caller   *Caller
	node     *link.Node
	deviceID string
	logger   *slog.Logger

	// mu protects peerDevices.
	mu          sync.RWMutex
	peerDevices map[string]peer.ID // device_id -> peer.ID
}

// NewRouter creates an agent router.
func NewRouter(registry *Registry, caller *Caller, node *link.Node, deviceID string, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		registry:    registry,
		caller:      caller,
		node:        node,
		deviceID:    deviceID,
		logger:      logger,
		peerDevices: make(map[string]peer.ID),
	}
}

// Call dispatches to a local or remote agent. If deviceID is empty or
// matches self, the call goes to the local registry. Otherwise it routes
// through skylink to the remote device's daemon.
func (r *Router) Call(ctx context.Context, p CallParams) (*CallResult, error) {
	if p.DeviceID == "" || p.DeviceID == r.deviceID {
		return r.callLocal(ctx, p)
	}
	return r.callRemote(ctx, p)
}

func (r *Router) callLocal(ctx context.Context, p CallParams) (*CallResult, error) {
	info := r.registry.Resolve(p.Agent)
	if info == nil {
		return nil, ErrAgentNotFound
	}
	result, err := r.caller.Call(ctx, info.Endpoint, p.Method, p.Params)
	if err != nil {
		return &CallResult{Error: err.Error()}, nil
	}
	return &CallResult{Result: result}, nil
}

func (r *Router) callRemote(ctx context.Context, p CallParams) (*CallResult, error) {
	pid, ok := r.lookupPeer(p.DeviceID)
	if !ok {
		return nil, fmt.Errorf("device %s not connected", p.DeviceID)
	}

	callCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
	defer cancel()

	raw, err := r.node.Call(callCtx, pid, "agent.call", p)
	if err != nil {
		return nil, fmt.Errorf("remote call to %s: %w", p.DeviceID, err)
	}

	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing remote result: %w", err)
	}
	return &result, nil
}

// List returns agents from the local registry and all connected peers.
func (r *Router) List(ctx context.Context) []AgentInfo {
	local := r.registry.List()

	peers := r.node.ConnectedPeers()
	if len(peers) == 0 {
		return local
	}

	type peerResult struct {
		agents []AgentInfo
		peerID peer.ID
	}

	results := make(chan peerResult, len(peers))
	var wg sync.WaitGroup

	for _, pid := range peers {
		wg.Add(1)
		go func(pid peer.ID) {
			defer wg.Done()
			queryCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
			defer cancel()

			raw, err := r.node.Call(queryCtx, pid, "agent.list", nil)
			if err != nil {
				r.logger.Debug("agent.list from peer failed", "peer", pid, "error", err)
				return
			}

			var resp struct {
				Agents []AgentInfo `json:"agents"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				r.logger.Debug("parsing agent.list response", "peer", pid, "error", err)
				return
			}

			// Cache device_id -> peer.ID mapping.
			for _, a := range resp.Agents {
				r.cachePeer(a.DeviceID, pid)
			}

			results <- peerResult{agents: resp.Agents, peerID: pid}
		}(pid)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	all := make([]AgentInfo, len(local))
	copy(all, local)
	for pr := range results {
		all = append(all, pr.agents...)
	}
	return all
}

// Discover returns agents matching a capability from local + remote.
func (r *Router) Discover(ctx context.Context, capability string) []AgentInfo {
	all := r.List(ctx)
	var matched []AgentInfo
	for _, a := range all {
		if a.HasCapability(capability) {
			matched = append(matched, a)
		}
	}
	return matched
}

func (r *Router) cachePeer(deviceID string, pid peer.ID) {
	r.mu.Lock()
	r.peerDevices[deviceID] = pid
	r.mu.Unlock()
}

func (r *Router) lookupPeer(deviceID string) (peer.ID, bool) {
	r.mu.RLock()
	pid, ok := r.peerDevices[deviceID]
	r.mu.RUnlock()
	return pid, ok
}
