package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/link"
)

const peerQueryTimeout = 3 * time.Second

// Router dispatches messages locally via SSE or to remote devices via
// skylink. It also aggregates agent lists across the swarm.
type Router struct {
	registry *Registry
	node     *link.Node
	resolver *link.Resolver
	emit     Emitter
	deviceID string
	logger   *slog.Logger
	mailbox  *agentmailbox.Store

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
		return r.deliverNetworkMailbox(ctx, record)
	default:
		return record, fmt.Errorf("mailbox item %s is not a sky10-network item", record.Item.ID)
	}
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
		if err := r.sendRemoteLive(ctx, msg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if _, appendErr := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDeliveryFailed,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
				Error:  err.Error(),
			}); appendErr != nil && firstErr == nil {
				firstErr = appendErr
			}
			continue
		}
		updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
			ItemID: record.Item.ID,
			Type:   agentmailbox.EventTypeDelivered,
			Actor:  routerPrincipal(r.deviceID, r.deviceID),
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
			if r.emit != nil {
				r.emit("agent.message", msg)
			}
			updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
				ItemID: record.Item.ID,
				Type:   agentmailbox.EventTypeDelivered,
				Actor:  routerPrincipal(r.deviceID, r.deviceID),
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
	if err := r.sendNetworkMailboxLive(ctx, targetAddress, item); err != nil {
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
			"route_address": targetAddress,
		},
	})
	if err != nil {
		return record, err
	}
	r.emitMailboxUpdate("delivered", updated)
	return updated, nil
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

func (r *Router) queueRemoteFailure(ctx context.Context, msg Message, reason string) (agentmailbox.Record, error) {
	record, err := r.createMailboxMessage(ctx, msg)
	if err != nil {
		return agentmailbox.Record{}, err
	}
	updated, err := r.mailbox.AppendEvent(ctx, agentmailbox.Event{
		ItemID: record.Item.ID,
		Type:   agentmailbox.EventTypeDeliveryFailed,
		Actor:  routerPrincipal(r.deviceID, r.deviceID),
		Error:  reason,
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
