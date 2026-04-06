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

// Router dispatches messages locally via SSE or to remote devices via
// skylink. It also aggregates agent lists across the swarm.
type Router struct {
	registry *Registry
	node     *link.Node
	emit     Emitter
	deviceID string
	logger   *slog.Logger

	// mu protects peerDevices and peerAgentCache.
	mu             sync.RWMutex
	peerDevices    map[string]peer.ID // device_id -> peer.ID
	peerAgentCache map[peer.ID]cachedAgents
}

// cachedAgents holds the last successful agent list from a peer.
type cachedAgents struct {
	agents []AgentInfo
	at     time.Time
}

// NewRouter creates a message router.
func NewRouter(registry *Registry, node *link.Node, emit Emitter, deviceID string, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		registry:       registry,
		node:           node,
		emit:           emit,
		deviceID:       deviceID,
		logger:         logger,
		peerDevices:    make(map[string]peer.ID),
		peerAgentCache: make(map[peer.ID]cachedAgents),
	}
}

// Send routes a message to the target agent or identity. Local targets
// get an SSE event. Remote targets are forwarded via skylink.
func (r *Router) Send(ctx context.Context, msg Message) (interface{}, error) {
	// Local delivery — target is a local agent or the device itself.
	if msg.DeviceID == "" || msg.DeviceID == r.deviceID {
		if r.emit != nil {
			r.emit("agent.message", msg)
		}
		return map[string]string{"id": msg.ID, "status": "sent"}, nil
	}

	// Remote — route via skylink.
	pid, ok := r.lookupPeer(msg.DeviceID)
	if !ok {
		return nil, fmt.Errorf("device %s not connected", msg.DeviceID)
	}

	sendCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
	defer cancel()

	_, err := r.node.Call(sendCtx, pid, "agent.send", msg)
	if err != nil {
		return nil, fmt.Errorf("remote send to %s: %w", msg.DeviceID, err)
	}
	return map[string]string{"id": msg.ID, "status": "sent"}, nil
}

// peerAgentCacheTTL is how long cached agent lists remain valid when a
// live query fails. Prevents the UI from flashing "No Agents" on transient
// P2P timeouts.
const peerAgentCacheTTL = 30 * time.Second

// List returns agents from the local registry and all connected peers.
// On peer query failure, returns cached results if still within TTL.
func (r *Router) List(ctx context.Context) []AgentInfo {
	local := r.registry.List()

	peers := r.node.ConnectedPrivateNetworkPeers()
	if len(peers) == 0 {
		return local
	}

	type peerResult struct {
		agents []AgentInfo
		peerID peer.ID
		ok     bool
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
				results <- peerResult{peerID: pid, ok: false}
				return
			}

			var resp struct {
				Agents []AgentInfo `json:"agents"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				r.logger.Debug("parsing agent.list response", "peer", pid, "error", err)
				results <- peerResult{peerID: pid, ok: false}
				return
			}

			// Cache device_id -> peer.ID mapping.
			for _, a := range resp.Agents {
				r.cachePeer(a.DeviceID, pid)
			}

			results <- peerResult{agents: resp.Agents, peerID: pid, ok: true}
		}(pid)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	now := time.Now()
	all := make([]AgentInfo, len(local))
	copy(all, local)

	for pr := range results {
		if pr.ok {
			// Fresh result — use it and update cache.
			all = append(all, pr.agents...)
			r.mu.Lock()
			r.peerAgentCache[pr.peerID] = cachedAgents{agents: pr.agents, at: now}
			r.mu.Unlock()
		} else {
			// Query failed — fall back to cached agents if fresh enough.
			r.mu.RLock()
			cached, hasCached := r.peerAgentCache[pr.peerID]
			r.mu.RUnlock()
			if hasCached && now.Sub(cached.at) < peerAgentCacheTTL {
				r.logger.Debug("using cached agent list", "peer", pr.peerID, "age", now.Sub(cached.at))
				all = append(all, cached.agents...)
			}
		}
	}

	return all
}

// Discover returns agents matching a skill from local + remote.
func (r *Router) Discover(ctx context.Context, skill string) []AgentInfo {
	all := r.List(ctx)
	var matched []AgentInfo
	for _, a := range all {
		if a.HasSkill(skill) {
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
