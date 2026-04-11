package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

const peerQueryTimeout = 3 * time.Second

// Router dispatches messages locally via SSE or to remote devices via
// skylink. It also aggregates agent lists across the swarm.
type Router struct {
	registry     *Registry
	node         *link.Node
	resolver     *link.Resolver
	emit         Emitter
	deviceID     string
	logger       *slog.Logger
	mailbox      *agentmailbox.Store
	relay        agentmailbox.NetworkRelay
	networkQueue agentmailbox.NetworkQueue

	// mu protects peerDevices, peerAddresses, and peerAgentCache.
	mu             sync.RWMutex
	peerDevices    map[string]peer.ID // device_id -> peer.ID
	peerAddresses  map[string]peer.ID // sky10 address -> peer.ID
	peerAgentCache map[peer.ID]cachedAgents
}

// cachedAgents holds the last successful agent list from a peer.
type cachedAgents struct {
	agents []AgentInfo
	at     time.Time
}

// NewRouter creates a message router.
func NewRouter(registry *Registry, node *link.Node, emit Emitter, deviceID string, logger *slog.Logger) *Router {
	logger = componentLogger(logger)
	return &Router{
		registry:       registry,
		node:           node,
		emit:           emit,
		deviceID:       deviceID,
		logger:         logger,
		peerDevices:    make(map[string]peer.ID),
		peerAddresses:  make(map[string]peer.ID),
		peerAgentCache: make(map[peer.ID]cachedAgents),
	}
}

// SetMailbox attaches durable mailbox storage used for fallback and drain.
func (r *Router) SetMailbox(store *agentmailbox.Store) {
	r.mailbox = store
}

// SetResolver attaches a sky10 network resolver for addressed delivery.
func (r *Router) SetResolver(resolver *link.Resolver) {
	r.resolver = resolver
}

// SetNetworkRelay attaches public-network store-and-forward transport.
func (r *Router) SetNetworkRelay(relay agentmailbox.NetworkRelay) {
	r.relay = relay
}

// SetNetworkQueue attaches public queue advertisement/discovery transport.
func (r *Router) SetNetworkQueue(queue agentmailbox.NetworkQueue) {
	r.networkQueue = queue
}

// Send routes a message to the target agent or identity. Local targets
// get an SSE event. Remote targets are forwarded via skylink.
func (r *Router) Send(ctx context.Context, msg Message) (interface{}, error) {
	// Local delivery — target is a local agent or the device itself.
	if msg.DeviceID == "" || msg.DeviceID == r.deviceID {
		return r.routeIncoming(ctx, msg)
	}

	// Remote — route via skylink.
	if err := r.sendRemoteLive(ctx, msg); err != nil {
		if r.mailbox == nil {
			return nil, err
		}
		record, queueErr := r.queueRemoteFailure(ctx, msg, err.Error())
		if queueErr != nil {
			return nil, queueErr
		}
		return r.queuedResult(msg.ID, record.Item.ID), nil
	}
	return r.sentResult(msg.ID), nil
}

// DeliverMailboxRecord retries a durable mailbox item through the direct
// delivery path appropriate for its scope.
func (r *Router) DeliverMailboxRecord(ctx context.Context, record agentmailbox.Record) (agentmailbox.Record, error) {
	switch record.Item.Scope() {
	case agentmailbox.ScopeSky10Network:
		if record.Item.QueueName() != "" && (record.Item.To == nil || record.Item.To.RouteAddress() == "") {
			return r.deliverNetworkQueueOffer(ctx, record)
		}
		return r.deliverNetworkMailbox(ctx, record)
	default:
		return r.deliverPrivateMailboxRecord(ctx, record)
	}
}

func (r *Router) deliverPrivateMailboxRecord(ctx context.Context, record agentmailbox.Record) (agentmailbox.Record, error) {
	if r.mailbox == nil {
		return agentmailbox.Record{}, fmt.Errorf("mailbox not configured")
	}
	if record.Item.To == nil {
		return record, fmt.Errorf("mailbox item %s has no recipient", record.Item.ID)
	}
	msg, err := mailboxMessage(record)
	if err != nil {
		return agentmailbox.Record{}, err
	}

	if record.Item.From.ID == r.deviceID && record.Item.To.DeviceHint != "" {
		attempted, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryAttempted,
			Actor:  routerPrincipal(r.deviceID, r.deviceID),
			Meta: map[string]string{
				"transport": "skylink",
				"device_id": msg.DeviceID,
			},
		})
		if err != nil {
			return agentmailbox.Record{}, err
		}
		r.emitMailboxUpdate("retrying", attempted)
		if err := r.sendRemoteLive(ctx, msg); err != nil {
			updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDeliveryFailed,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
				Error:  err.Error(),
				Meta: map[string]string{
					"transport": "skylink",
					"device_id": msg.DeviceID,
				},
			})
			if appendErr == nil {
				r.emitMailboxUpdate("queued", updated)
				return updated, err
			}
			return record, err
		}
		updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDelivered,
			Actor:  routerPrincipal(r.deviceID, r.deviceID),
			Meta: map[string]string{
				"transport": "skylink",
				"device_id": msg.DeviceID,
			},
		})
		if err != nil {
			return agentmailbox.Record{}, err
		}
		r.emitMailboxUpdate("delivered", updated)
		return updated, nil
	}

	if r.registry.Resolve(msg.To) == nil {
		return record, fmt.Errorf("agent %s is not registered", msg.To)
	}
	attempted, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryAttempted,
		Actor:  routerPrincipal(r.deviceID, r.deviceID),
		Meta: map[string]string{
			"transport": "local_registry",
			"recipient": msg.To,
		},
	})
	if err != nil {
		return agentmailbox.Record{}, err
	}
	r.emitMailboxUpdate("retrying", attempted)
	if r.emit != nil {
		r.emit("agent.message", msg)
	}
	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDelivered,
		Actor:  routerPrincipal(r.deviceID, r.deviceID),
		Meta: map[string]string{
			"transport": "local_registry",
			"recipient": msg.To,
		},
	})
	if err != nil {
		return agentmailbox.Record{}, err
	}
	r.emitMailboxUpdate("delivered", updated)
	return updated, nil
}

// DrainNetworkOutbox retries durable sky10-network outbound items.
func (r *Router) DrainNetworkOutbox(ctx context.Context, address string) error {
	if r.mailbox == nil {
		return nil
	}
	if err := r.mailbox.Reload(ctx); err != nil {
		return err
	}

	records := r.mailbox.ListOutbox("")
	var firstErr error
	for _, record := range records {
		if record.Item.Scope() != agentmailbox.ScopeSky10Network {
			continue
		}
		target := ""
		if record.Item.To != nil {
			target = record.Item.To.RouteAddress()
		}
		if address != "" && target != address {
			continue
		}
		if record.State != agentmailbox.StateQueued && record.State != agentmailbox.StateFailed {
			continue
		}
		if _, err := r.deliverNetworkMailbox(ctx, record); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// PollNetworkRelay ingests public-network relay handoffs and receipts.
func (r *Router) PollNetworkRelay(ctx context.Context) error {
	if r.relay == nil || r.mailbox == nil {
		return nil
	}
	inbound, err := r.relay.Poll(ctx)
	if err != nil {
		return err
	}

	var firstErr error
	for _, entry := range inbound {
		switch entry.RecordType {
		case "item":
			if err := r.ingestNetworkRelayItem(ctx, entry); err != nil && firstErr == nil {
				firstErr = err
			}
		case "delivery_receipt":
			if err := r.ingestNetworkRelayReceipt(ctx, entry); err != nil && firstErr == nil {
				firstErr = err
			}
		case "queue_claim":
			if err := r.ingestNetworkQueueClaim(ctx, entry); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// RunNetworkRelayPoller continuously ingests public-network relay traffic.
func (r *Router) RunNetworkRelayPoller(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = agentmailbox.DefaultRelayPollInterval()
	}
	if err := r.PollNetworkRelay(ctx); err != nil {
		r.logger.Debug("initial network relay poll failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.PollNetworkRelay(ctx); err != nil {
				r.logger.Debug("network relay poll failed", "error", err)
			}
		}
	}
}

// DrainOutbox retries durable outbound messages for this device. When
// deviceID is non-empty, only messages targeting that device are retried.
func (r *Router) DrainOutbox(ctx context.Context, deviceID string) error {
	if r.mailbox == nil {
		return nil
	}
	if err := r.mailbox.Reload(ctx); err != nil {
		return err
	}

	records := r.mailbox.ListOutbox(r.deviceID)
	var firstErr error
	for _, record := range records {
		if deviceID != "" && record.Item.To != nil && record.Item.To.DeviceHint != deviceID {
			continue
		}
		if record.State != agentmailbox.StateQueued && record.State != agentmailbox.StateFailed {
			continue
		}
		msg, err := mailboxMessage(record)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		attempted, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryAttempted,
			Actor:  routerPrincipal(r.deviceID, r.deviceID),
			Meta: map[string]string{
				"transport": "skylink",
				"device_id": msg.DeviceID,
			},
		})
		if appendErr != nil {
			if firstErr == nil {
				firstErr = appendErr
			}
			continue
		}
		r.emitMailboxUpdate("retrying", attempted)
		if err := r.sendRemoteLive(ctx, msg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if _, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDeliveryFailed,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
				Error:  err.Error(),
				Meta: map[string]string{
					"transport": "skylink",
					"device_id": msg.DeviceID,
				},
			}); appendErr != nil && firstErr == nil {
				firstErr = appendErr
			}
			continue
		}
		updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDelivered,
			Actor:  routerPrincipal(r.deviceID, r.deviceID),
			Meta: map[string]string{
				"transport": "skylink",
				"device_id": msg.DeviceID,
			},
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		r.emitMailboxUpdate("delivered", updated)
	}
	return firstErr
}

// DrainLocalPending retries queued local deliveries for one or more recipient
// identifiers such as an agent ID or name.
func (r *Router) DrainLocalPending(ctx context.Context, recipients ...string) error {
	if r.mailbox == nil {
		return nil
	}
	if err := r.mailbox.Reload(ctx); err != nil {
		return err
	}

	seen := make(map[string]struct{})
	var firstErr error
	for _, recipient := range recipients {
		if recipient == "" {
			continue
		}
		for _, record := range r.mailbox.ListInbox(recipient) {
			if _, ok := seen[record.Item.ID]; ok {
				continue
			}
			seen[record.Item.ID] = struct{}{}
			if record.State != agentmailbox.StateQueued && record.State != agentmailbox.StateFailed {
				continue
			}
			msg, err := mailboxMessage(record)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if r.registry.Resolve(msg.To) == nil {
				continue
			}
			attempted, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDeliveryAttempted,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
				Meta: map[string]string{
					"transport": "local_registry",
					"recipient": msg.To,
				},
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			r.emitMailboxUpdate("retrying", attempted)
			if r.emit != nil {
				r.emit("agent.message", msg)
			}
			updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDelivered,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
				Meta: map[string]string{
					"transport": "local_registry",
					"recipient": msg.To,
				},
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			r.emitMailboxUpdate("delivered", updated)
		}
	}
	return firstErr
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

// DiscoverPublicQueue returns currently visible public-network queue offers.
func (r *Router) DiscoverPublicQueue(ctx context.Context, skill, queue string) ([]agentmailbox.QueueOffer, error) {
	if r.networkQueue == nil {
		return nil, fmt.Errorf("public queue not configured")
	}
	return r.networkQueue.QueryOffers(ctx, agentmailbox.QueueOfferFilter{
		Skill: strings.TrimSpace(skill),
		Queue: strings.TrimSpace(queue),
	})
}

// ClaimPublicQueue sends a sealed claim request back to the queue sender.
func (r *Router) ClaimPublicQueue(ctx context.Context, offer agentmailbox.QueueOffer, actor agentmailbox.Principal, ttl time.Duration) (agentmailbox.QueueClaim, error) {
	if r.relay == nil {
		return agentmailbox.QueueClaim{}, fmt.Errorf("network relay not configured")
	}
	claim, err := agentmailbox.NewQueueClaim(offer, actor, ttl, time.Now().UTC())
	if err != nil {
		return agentmailbox.QueueClaim{}, err
	}
	if err := r.relay.PublishQueueClaim(ctx, offer.Sender, claim); err != nil {
		return agentmailbox.QueueClaim{}, err
	}
	return claim, nil
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

func (r *Router) cacheAddress(address string, pid peer.ID) {
	r.mu.Lock()
	r.peerAddresses[address] = pid
	r.mu.Unlock()
}

func (r *Router) lookupAddress(address string) (peer.ID, bool) {
	r.mu.RLock()
	pid, ok := r.peerAddresses[address]
	r.mu.RUnlock()
	return pid, ok
}

func (r *Router) routeIncoming(ctx context.Context, msg Message) (interface{}, error) {
	if r.emit != nil {
		r.emit("agent.message", msg)
	}
	if r.mailbox != nil && r.registry.Resolve(msg.To) == nil {
		record, err := r.queueLocalPending(ctx, msg)
		if err != nil {
			return nil, err
		}
		return r.queuedResult(msg.ID, record.Item.ID), nil
	}
	return r.sentResult(msg.ID), nil
}

func (r *Router) sendRemoteLive(ctx context.Context, msg Message) error {
	pid, ok := r.lookupPeer(msg.DeviceID)
	if !ok {
		return fmt.Errorf("device %s not connected", msg.DeviceID)
	}

	sendCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
	defer cancel()

	if _, err := r.node.Call(sendCtx, pid, "agent.send", msg); err != nil {
		return fmt.Errorf("remote send to %s: %w", msg.DeviceID, err)
	}
	return nil
}

func (r *Router) deliverNetworkQueueOffer(ctx context.Context, record agentmailbox.Record) (agentmailbox.Record, error) {
	if r.mailbox == nil {
		return agentmailbox.Record{}, fmt.Errorf("mailbox not configured")
	}
	if r.networkQueue == nil {
		err := fmt.Errorf("public queue not configured")
		updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryFailed,
			Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
			Error:  err.Error(),
		})
		if appendErr == nil {
			r.emitMailboxUpdate("queued", updated)
			return updated, err
		}
		return record, err
	}
	if record.Terminal() {
		return record, nil
	}
	if hasMailboxEvent(record, agentmailbox.EventTypeHandedOff, "transport", "nostr_queue") {
		return record, nil
	}
	attempted, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryAttempted,
		Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
		Meta: map[string]string{
			"transport": "nostr_queue",
			"queue":     record.Item.QueueName(),
			"skill":     strings.TrimSpace(record.Item.TargetSkill),
		},
	})
	if err != nil {
		return record, err
	}
	r.emitMailboxUpdate("retrying", attempted)
	if _, err := r.networkQueue.PublishOffer(ctx, record.Item); err != nil {
		updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryFailed,
			Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
			Error:  err.Error(),
			Meta: map[string]string{
				"transport": "nostr_queue",
				"queue":     record.Item.QueueName(),
				"skill":     strings.TrimSpace(record.Item.TargetSkill),
			},
		})
		if appendErr == nil {
			r.emitMailboxUpdate("queued", updated)
			return updated, err
		}
		return record, err
	}

	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeHandedOff,
		Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
		Meta: map[string]string{
			"transport": "nostr_queue",
			"queue":     record.Item.QueueName(),
			"skill":     strings.TrimSpace(record.Item.TargetSkill),
		},
	})
	if err != nil {
		return record, err
	}
	r.emitMailboxUpdate("handed_off", updated)
	return updated, nil
}

func (r *Router) deliverNetworkMailbox(ctx context.Context, record agentmailbox.Record) (agentmailbox.Record, error) {
	if r.mailbox == nil {
		return agentmailbox.Record{}, fmt.Errorf("mailbox not configured")
	}
	if record.Item.To == nil {
		return record, fmt.Errorf("mailbox item %s has no recipient", record.Item.ID)
	}
	if record.State == agentmailbox.StateDelivered || record.State == agentmailbox.StateCompleted {
		return record, nil
	}

	targetAddress := record.Item.To.RouteAddress()
	if targetAddress == "" {
		err := fmt.Errorf("mailbox item %s has no sky10 route address", record.Item.ID)
		updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryFailed,
			Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
			Error:  err.Error(),
		})
		if appendErr == nil {
			r.emitMailboxUpdate("queued", updated)
			return updated, err
		}
		return record, err
	}

	item := cloneMailboxItem(record.Item)
	item.To = cloneMailboxRecipient(item.To)
	if item.To != nil {
		item.To.DeviceHint = ""
	}
	attempted, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryAttempted,
		Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
		Meta: map[string]string{
			"transport":     "skylink",
			"route_address": targetAddress,
		},
	})
	if err != nil {
		return record, err
	}
	r.emitMailboxUpdate("retrying", attempted)
	if err := r.sendNetworkMailboxLive(ctx, targetAddress, item); err != nil {
		if r.relay != nil {
			if existing, ok := relayHandoffForRecord(record); ok {
				r.logger.Debug("network mailbox already handed off", "item_id", record.Item.ID, "handoff_id", existing)
				return record, nil
			}
			handoffAttempted, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDeliveryAttempted,
				Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
				Meta: map[string]string{
					"transport":     "nostr_dropbox",
					"route_address": targetAddress,
				},
			})
			if appendErr != nil {
				return record, appendErr
			}
			r.emitMailboxUpdate("retrying", handoffAttempted)
			handoff, handoffErr := r.relay.HandoffItem(ctx, item)
			if handoffErr == nil {
				updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
					ItemID: record.Item.ID,
					Type:   agentmailbox.EventTypeHandedOff,
					Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
					Meta: map[string]string{
						"route_address": targetAddress,
						"transport":     "nostr_dropbox",
						"handoff_id":    handoff.ID,
						"live_error":    err.Error(),
					},
				})
				if appendErr == nil {
					r.emitMailboxUpdate("handed_off", updated)
					return updated, nil
				}
				return record, appendErr
			}
			err = fmt.Errorf("%v; relay handoff failed: %w", err, handoffErr)
		}
		updated, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDeliveryFailed,
			Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
			Error:  err.Error(),
		})
		if appendErr == nil {
			r.emitMailboxUpdate("queued", updated)
			return updated, err
		}
		return record, err
	}

	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDelivered,
		Actor:  routerActor(agentmailbox.ScopeSky10Network, r.routerAddress(), r.deviceID),
		Meta: map[string]string{
			"transport":     "skylink",
			"route_address": targetAddress,
		},
	})
	if err != nil {
		return record, err
	}
	r.emitMailboxUpdate("delivered", updated)
	return updated, nil
}

func (r *Router) ingestNetworkRelayItem(ctx context.Context, entry agentmailbox.RelayInbound) error {
	if entry.Item == nil || r.mailbox == nil {
		return nil
	}
	if existing, ok := r.mailbox.Get(entry.Item.ID); ok {
		return r.publishNetworkDeliveryReceipt(ctx, entry, existing)
	}
	record, err := r.mailbox.Create(ctx, *entry.Item)
	if err != nil {
		return err
	}
	r.emitMailboxUpdate("received", record)
	return r.publishNetworkDeliveryReceipt(ctx, entry, record)
}

func (r *Router) publishNetworkDeliveryReceipt(ctx context.Context, entry agentmailbox.RelayInbound, record agentmailbox.Record) error {
	if r.relay == nil || entry.Sender == "" {
		return nil
	}
	if _, err := skyAddress(entry.Sender); err != nil {
		return nil
	}
	if entry.Item != nil && entry.Item.Kind == agentmailbox.ItemKindMessage && entry.Item.To != nil {
		if err := r.DrainLocalPending(ctx, entry.Item.To.ID); err != nil {
			return err
		}
	}
	return r.relay.PublishDeliveryReceipt(ctx, entry.Sender, agentmailbox.RelayDeliveryReceipt{
		ItemID:      record.Item.ID,
		HandoffID:   coalesce(entry.HandoffID, record.Item.ID),
		DeliveredBy: r.routerAddress(),
		DeliveredAt: time.Now().UTC(),
	})
}

func (r *Router) ingestNetworkRelayReceipt(ctx context.Context, entry agentmailbox.RelayInbound) error {
	if entry.Receipt == nil || r.mailbox == nil {
		return nil
	}
	record, ok := r.mailbox.Get(entry.Receipt.ItemID)
	if !ok {
		return nil
	}
	if hasMailboxEvent(record, agentmailbox.EventTypeDelivered, "handoff_id", coalesce(entry.Receipt.HandoffID, record.Item.ID)) {
		return nil
	}
	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDelivered,
		Actor: agentmailbox.Principal{
			ID:        entry.Receipt.DeliveredBy,
			Kind:      agentmailbox.PrincipalKindNetworkAgent,
			Scope:     agentmailbox.ScopeSky10Network,
			RouteHint: entry.Receipt.DeliveredBy,
		},
		Meta: map[string]string{
			"transport":  "nostr_dropbox",
			"handoff_id": coalesce(entry.Receipt.HandoffID, record.Item.ID),
			"event_id":   entry.EventID,
		},
	})
	if err != nil {
		return err
	}
	r.emitMailboxUpdate("delivered", updated)
	return nil
}

func (r *Router) ingestNetworkQueueClaim(ctx context.Context, entry agentmailbox.RelayInbound) error {
	if entry.Claim == nil || r.mailbox == nil {
		return nil
	}
	record, ok := r.mailbox.Get(entry.Claim.ItemID)
	if !ok {
		return nil
	}
	if record.Item.Scope() != agentmailbox.ScopeSky10Network || record.Item.QueueName() == "" || record.Terminal() {
		return nil
	}

	claimActor := entry.Claim.ActorPrincipal()
	claimed := record
	if record.Claim == nil || record.Claim.Holder != claimActor.ID {
		updated, ok, err := r.mailbox.Claim(ctx, record.Item.ID, claimActor, entry.Claim.TTL())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		claimed = updated
		r.emitMailboxUpdate("claim", claimed)
	}

	assignment, err := r.ensurePublicQueueAssignment(ctx, claimed, *entry.Claim)
	if err != nil {
		return err
	}
	deliveredAssignment, deliverErr := r.DeliverMailboxRecord(ctx, assignment)
	if deliverErr == nil && deliveredAssignment.Item.ID != "" {
		assignment = deliveredAssignment
	}
	if err := r.markPublicQueueAssigned(ctx, claimed, *entry.Claim, assignment); err != nil {
		return err
	}
	if deliverErr != nil {
		return deliverErr
	}
	return nil
}

func (r *Router) sendNetworkMailboxLive(ctx context.Context, address string, item agentmailbox.Item) error {
	if r.node == nil {
		return fmt.Errorf("node not configured")
	}
	pid, ok := r.lookupAddress(address)
	if !ok {
		if r.resolver == nil {
			return fmt.Errorf("sky10 address %s not connected", address)
		}
		resolveCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
		defer cancel()
		info, err := r.resolver.Resolve(resolveCtx, address)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", address, err)
		}
		if host := r.node.Host(); host != nil {
			if err := host.Connect(resolveCtx, *info); err != nil {
				return fmt.Errorf("connecting %s: %w", address, err)
			}
		}
		pid = info.ID
		r.cacheAddress(address, pid)
	}

	sendCtx, cancel := context.WithTimeout(ctx, peerQueryTimeout)
	defer cancel()
	if _, err := r.node.Call(sendCtx, pid, "agent.mailbox.deliver", mailboxDeliverParams{Item: item}); err != nil {
		return fmt.Errorf("network mailbox deliver to %s: %w", address, err)
	}
	return nil
}

func (r *Router) ensurePublicQueueAssignment(ctx context.Context, record agentmailbox.Record, claim agentmailbox.QueueClaim) (agentmailbox.Record, error) {
	for _, reply := range r.mailbox.ListReplies(record.Item.ID) {
		if reply.Item.To == nil {
			continue
		}
		if reply.Item.To.ID == claim.AgentID && reply.Item.To.RouteAddress() == claim.Claimant {
			return reply, nil
		}
	}

	item := cloneMailboxItem(record.Item)
	item.ID = ""
	item.To = &agentmailbox.Principal{
		ID:        claim.AgentID,
		Kind:      agentmailbox.PrincipalKindNetworkAgent,
		Scope:     agentmailbox.ScopeSky10Network,
		RouteHint: claim.Claimant,
	}
	item.TargetSkill = ""
	item.ReplyTo = record.Item.ID
	item.IdempotencyKey = publicQueueAssignmentKey(record.Item.ID, claim.AgentID)

	created, err := r.mailbox.Create(ctx, item)
	if err != nil {
		return agentmailbox.Record{}, err
	}
	r.emitMailboxUpdate("assigned", created)
	return created, nil
}

func (r *Router) markPublicQueueAssigned(ctx context.Context, record agentmailbox.Record, claim agentmailbox.QueueClaim, assignment agentmailbox.Record) error {
	if hasMailboxEvent(record, agentmailbox.EventTypeAssigned, "assignment_item_id", assignment.Item.ID) {
		return nil
	}
	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeAssigned,
		Actor:  claim.ActorPrincipal(),
		Meta: map[string]string{
			"queue":              record.Item.QueueName(),
			"claim_id":           claim.ClaimID,
			"claimant":           claim.Claimant,
			"assignment_item_id": assignment.Item.ID,
		},
	})
	if err != nil {
		return err
	}
	r.emitMailboxUpdate("assigned", updated)
	if r.networkQueue != nil {
		if _, err := r.networkQueue.PublishState(ctx, record.Item, "assigned"); err != nil {
			r.logger.Debug("queue assignment state publish failed", "item_id", record.Item.ID, "error", err)
		}
	}
	if updated.Claim != nil {
		_, released, err := r.mailbox.Release(ctx, updated.Item.ID, updated.Claim.Holder, updated.Claim.Token)
		if err != nil {
			return err
		}
		if released {
			if refreshed, ok := r.mailbox.Get(updated.Item.ID); ok {
				r.emitMailboxUpdate("assigned", refreshed)
			}
		}
	}
	return nil
}

func (r *Router) queueRemoteFailure(ctx context.Context, msg Message, reason string) (agentmailbox.Record, error) {
	record, err := r.createMailboxMessage(ctx, msg)
	if err != nil {
		return agentmailbox.Record{}, err
	}
	record, err = r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryAttempted,
		Actor:  routerPrincipal(r.deviceID, r.deviceID),
		Meta: map[string]string{
			"transport": "skylink",
			"device_id": msg.DeviceID,
		},
	})
	if err != nil {
		return agentmailbox.Record{}, err
	}
	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryFailed,
		Actor:  routerPrincipal(r.deviceID, r.deviceID),
		Error:  reason,
		Meta: map[string]string{
			"transport": "skylink",
			"device_id": msg.DeviceID,
		},
	})
	if err != nil {
		return agentmailbox.Record{}, err
	}
	r.emitMailboxUpdate("queued", updated)
	return updated, nil
}

func (r *Router) queueLocalPending(ctx context.Context, msg Message) (agentmailbox.Record, error) {
	record, err := r.createMailboxMessage(ctx, msg)
	if err != nil {
		return agentmailbox.Record{}, err
	}
	r.emitMailboxUpdate("queued", record)
	return record, nil
}

func (r *Router) createMailboxMessage(ctx context.Context, msg Message) (agentmailbox.Record, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return agentmailbox.Record{}, fmt.Errorf("marshal mailbox message %s: %w", msg.ID, err)
	}
	record, err := r.mailbox.Create(ctx, agentmailbox.Item{
		Kind:           agentmailbox.ItemKindMessage,
		From:           routerPrincipal(messageFromID(msg, r.deviceID), r.deviceID),
		To:             routerRecipient(msg),
		SessionID:      msg.SessionID,
		RequestID:      msg.ID,
		IdempotencyKey: msg.ID,
		PayloadInline:  payload,
		CreatedAt:      msg.Timestamp,
	})
	if err != nil {
		return agentmailbox.Record{}, err
	}
	return record, nil
}

func (r *Router) emitMailboxUpdate(action string, record agentmailbox.Record) {
	if r.emit == nil {
		return
	}
	payload := map[string]interface{}{
		"action":  action,
		"item_id": record.Item.ID,
		"state":   record.State,
		"from":    record.Item.From.ID,
	}
	if record.Item.To != nil {
		payload["to"] = record.Item.To.ID
		payload["device_id"] = record.Item.To.DeviceHint
	}
	r.emit("agent.mailbox.updated", payload)
}

func (r *Router) sentResult(id string) map[string]string {
	return map[string]string{"id": id, "status": "sent"}
}

func (r *Router) queuedResult(id, mailboxItemID string) map[string]string {
	return map[string]string{
		"id":              id,
		"status":          "queued",
		"mailbox_item_id": mailboxItemID,
	}
}

func messageFromID(msg Message, fallback string) string {
	if msg.From != "" {
		return msg.From
	}
	return fallback
}

func routerActor(scope, id, deviceHint string) agentmailbox.Principal {
	return agentmailbox.Principal{
		ID:         id,
		Kind:       agentmailbox.PrincipalKindLocalAgent,
		Scope:      scope,
		DeviceHint: deviceHint,
	}
}

func routerPrincipal(id, deviceHint string) agentmailbox.Principal {
	return routerActor(agentmailbox.ScopePrivateNetwork, id, deviceHint)
}

func routerRecipient(msg Message) *agentmailbox.Principal {
	return &agentmailbox.Principal{
		ID:         msg.To,
		Scope:      agentmailbox.ScopePrivateNetwork,
		DeviceHint: msg.DeviceID,
	}
}

func (r *Router) routerAddress() string {
	if r.node != nil {
		return r.node.Address()
	}
	return r.deviceID
}

func cloneMailboxItem(item agentmailbox.Item) agentmailbox.Item {
	cp := item
	if item.To != nil {
		to := *item.To
		cp.To = &to
	}
	if item.PayloadInline != nil {
		cp.PayloadInline = append(json.RawMessage(nil), item.PayloadInline...)
	}
	if item.PayloadRef != nil {
		ref := *item.PayloadRef
		cp.PayloadRef = &ref
	}
	return cp
}

func cloneMailboxRecipient(p *agentmailbox.Principal) *agentmailbox.Principal {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func relayHandoffForRecord(record agentmailbox.Record) (string, bool) {
	for i := len(record.Events) - 1; i >= 0; i-- {
		event := record.Events[i]
		if event.Type != agentmailbox.EventTypeHandedOff {
			continue
		}
		if handoffID := strings.TrimSpace(event.Meta["handoff_id"]); handoffID != "" {
			return handoffID, true
		}
		return record.Item.ID, true
	}
	return "", false
}

func publicQueueAssignmentKey(itemID, agentID string) string {
	return itemID + ":assignment:" + agentID
}

func hasMailboxEvent(record agentmailbox.Record, eventType, metaKey, metaValue string) bool {
	for _, event := range record.Events {
		if event.Type != eventType {
			continue
		}
		if metaKey == "" {
			return true
		}
		if event.Meta[metaKey] == metaValue {
			return true
		}
	}
	return false
}

func coalesce(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func skyAddress(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("empty sky10 address")
	}
	if _, err := skykey.ParseAddress(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

func mailboxMessage(record agentmailbox.Record) (Message, error) {
	var msg Message
	if len(record.Item.PayloadInline) == 0 {
		return Message{}, fmt.Errorf("mailbox item %s has no inline message payload", record.Item.ID)
	}
	if err := json.Unmarshal(record.Item.PayloadInline, &msg); err != nil {
		return Message{}, fmt.Errorf("parse mailbox message %s: %w", record.Item.ID, err)
	}
	return msg, nil
}
