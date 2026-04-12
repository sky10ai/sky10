package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Emitter sends SSE events to connected subscribers.
type Emitter func(event string, data interface{})

// PeerNotifier broadcasts agent events to connected devices.
type PeerNotifier func(ctx context.Context, topic string)

// RPCHandler dispatches agent.* RPC methods.
type RPCHandler struct {
	registry *Registry
	owner    *skykey.Key
	router   *Router // nil until cross-device wiring
	mailbox  *agentmailbox.Store
	emit     Emitter
	notify   PeerNotifier
}

// NewRPCHandler creates an agent RPC handler.
func NewRPCHandler(registry *Registry, owner *skykey.Key, emit Emitter) *RPCHandler {
	return &RPCHandler{registry: registry, owner: owner, emit: emit}
}

// SetRouter attaches a cross-device router.
func (h *RPCHandler) SetRouter(r *Router) {
	h.router = r
}

// SetMailbox attaches durable mailbox storage.
func (h *RPCHandler) SetMailbox(store *agentmailbox.Store) {
	h.mailbox = store
}

// SetPeerNotifier attaches a function that broadcasts agent events to
// connected devices (e.g. linkNode.NotifyOwn).
func (h *RPCHandler) SetPeerNotifier(fn PeerNotifier) {
	h.notify = fn
}

// Dispatch handles agent.* methods.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "agent.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "agent.register":
		result, err = h.rpcRegister(ctx, params)
	case "agent.deregister":
		result, err = h.rpcDeregister(ctx, params)
	case "agent.list":
		result, err = h.rpcList(ctx, params)
	case "agent.send":
		result, err = h.rpcSend(ctx, params)
	case "agent.heartbeat":
		result, err = h.rpcHeartbeat(ctx, params)
	case "agent.discover":
		result, err = h.rpcDiscover(ctx, params)
	case "agent.status":
		result, err = h.rpcStatus(ctx)
	case "agent.mailbox.send":
		result, err = h.rpcMailboxSend(ctx, params)
	case "agent.mailbox.views":
		result, err = h.rpcMailboxViews(ctx, params)
	case "agent.mailbox.listInbox":
		result, err = h.rpcMailboxListInbox(ctx, params)
	case "agent.mailbox.listOutbox":
		result, err = h.rpcMailboxListOutbox(ctx, params)
	case "agent.mailbox.listQueue":
		result, err = h.rpcMailboxListQueue(ctx, params)
	case "agent.mailbox.listFailed":
		result, err = h.rpcMailboxListFailed(ctx, params)
	case "agent.mailbox.listSent":
		result, err = h.rpcMailboxListSent(ctx, params)
	case "agent.mailbox.get":
		result, err = h.rpcMailboxGet(ctx, params)
	case "agent.mailbox.claim":
		result, err = h.rpcMailboxClaim(ctx, params)
	case "agent.mailbox.release":
		result, err = h.rpcMailboxRelease(ctx, params)
	case "agent.mailbox.ack":
		result, err = h.rpcMailboxAck(ctx, params)
	case "agent.mailbox.approve":
		result, err = h.rpcMailboxApprove(ctx, params)
	case "agent.mailbox.reject":
		result, err = h.rpcMailboxReject(ctx, params)
	case "agent.mailbox.complete":
		result, err = h.rpcMailboxComplete(ctx, params)
	case "agent.mailbox.retry":
		result, err = h.rpcMailboxRetry(ctx, params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

func (h *RPCHandler) rpcRegister(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p RegisterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	agentID, _, err := GenerateAgentID(h.owner, p.EffectiveKeyName())
	if err != nil {
		return nil, fmt.Errorf("generating agent ID: %w", err)
	}

	info, err := h.registry.Register(p, agentID)
	if err != nil {
		return nil, err
	}

	if h.emit != nil {
		h.emit("agent.connected", map[string]interface{}{
			"id":        info.ID,
			"name":      info.Name,
			"device_id": info.DeviceID,
			"skills":    info.Skills,
		})
	}
	if h.notify != nil {
		go h.notify(context.Background(), "agent:connected:"+info.DeviceID)
	}
	if h.router != nil {
		go h.router.DrainLocalPending(context.Background(), info.ID, info.Name, info.KeyName)
	}

	return RegisterResult{AgentID: info.ID, Status: "registered"}, nil
}

func (h *RPCHandler) rpcDeregister(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p DeregisterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	info := h.registry.Get(p.AgentID)
	h.registry.Deregister(p.AgentID)

	if h.emit != nil && info != nil {
		h.emit("agent.disconnected", map[string]interface{}{
			"id":        info.ID,
			"name":      info.Name,
			"device_id": info.DeviceID,
		})
	}
	if h.notify != nil {
		deviceID := h.registry.DeviceID()
		if info != nil && info.DeviceID != "" {
			deviceID = info.DeviceID
		}
		go h.notify(context.Background(), "agent:disconnected:"+deviceID)
	}

	return map[string]string{"status": "ok"}, nil
}

func (h *RPCHandler) rpcList(ctx context.Context, _ json.RawMessage) (interface{}, error) {
	var agents []AgentInfo
	if h.router != nil {
		agents = h.router.List(ctx)
	} else {
		agents = h.registry.List()
	}
	return map[string]interface{}{
		"agents": agents,
		"count":  len(agents),
	}, nil
}

func (h *RPCHandler) rpcSend(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p SendParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.To == "" {
		return nil, fmt.Errorf("to is required")
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	msg := Message{
		ID:        uuid.NewString(),
		SessionID: p.SessionID,
		From:      h.registry.DeviceID(),
		To:        p.To,
		DeviceID:  p.DeviceID,
		Type:      p.Type,
		Content:   p.Content,
		Timestamp: time.Now().UTC(),
	}

	// Route the message.
	if h.router != nil {
		return h.router.Send(ctx, msg)
	}

	// Local-only fallback: emit as SSE event.
	if h.emit != nil {
		h.emit("agent.message", msg)
	}
	return SendResult{
		ID:     msg.ID,
		Status: "sent",
		Delivery: DeliveryMetadata{
			Policy:        DeliveryPolicyLiveOnly,
			Scope:         agentmailbox.ScopePrivateNetwork,
			Status:        "sent",
			LiveTransport: "local_registry",
			LastTransport: "local_registry",
			LiveAttempted: true,
		},
	}, nil
}

func (h *RPCHandler) rpcHeartbeat(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if !h.registry.Heartbeat(p.AgentID) {
		return nil, ErrAgentNotFound
	}
	return map[string]string{"status": "ok"}, nil
}

func (h *RPCHandler) rpcDiscover(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Skill == "" {
		return nil, fmt.Errorf("skill is required")
	}

	var agents []AgentInfo
	if h.router != nil {
		agents = h.router.Discover(ctx, p.Skill)
	} else {
		for _, a := range h.registry.List() {
			if a.HasSkill(p.Skill) {
				agents = append(agents, a)
			}
		}
	}
	return map[string]interface{}{
		"agents": agents,
		"count":  len(agents),
	}, nil
}

func (h *RPCHandler) rpcStatus(_ context.Context) (interface{}, error) {
	agents := h.registry.List()
	skills := make(map[string]bool)
	for _, a := range agents {
		for _, s := range a.Skills {
			skills[s] = true
		}
	}
	skillList := make([]string, 0, len(skills))
	for s := range skills {
		skillList = append(skillList, s)
	}
	return map[string]interface{}{
		"agents":            len(agents),
		"skills":            skillList,
		"delivery_policies": deliveryPolicies(h.router != nil && h.mailbox != nil),
	}, nil
}
