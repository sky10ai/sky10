package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Emitter sends SSE events to connected subscribers.
type Emitter func(event string, data interface{})

// PeerNotifier broadcasts agent events to connected devices.
type PeerNotifier func(ctx context.Context, topic string)

// RPCHandler dispatches agent.* RPC methods.
type RPCHandler struct {
	registry *Registry
	router   *Router // nil until cross-device wiring
	emit     Emitter
	notify   PeerNotifier
}

// NewRPCHandler creates an agent RPC handler.
func NewRPCHandler(registry *Registry, emit Emitter) *RPCHandler {
	return &RPCHandler{registry: registry, emit: emit}
}

// SetRouter attaches a cross-device router.
func (h *RPCHandler) SetRouter(r *Router) {
	h.router = r
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

	agentID, _, err := GenerateAgentID()
	if err != nil {
		return nil, fmt.Errorf("generating agent ID: %w", err)
	}

	info, err := h.registry.Register(p, agentID)
	if err != nil {
		return nil, err
	}

	if h.emit != nil {
		h.emit("agent.connected", map[string]interface{}{
			"id":           info.ID,
			"name":         info.Name,
			"device_id":    info.DeviceID,
			"capabilities": info.Capabilities,
		})
	}
	if h.notify != nil {
		go h.notify(context.Background(), "agent:connected")
	}

	return RegisterResult{AgentID: agentID, Status: "registered"}, nil
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
		go h.notify(context.Background(), "agent:disconnected")
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
	return map[string]string{"id": msg.ID, "status": "sent"}, nil
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
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Capability == "" {
		return nil, fmt.Errorf("capability is required")
	}

	var agents []AgentInfo
	if h.router != nil {
		agents = h.router.Discover(ctx, p.Capability)
	} else {
		for _, a := range h.registry.List() {
			if a.HasCapability(p.Capability) {
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
	caps := make(map[string]bool)
	for _, a := range agents {
		for _, c := range a.Capabilities {
			caps[c] = true
		}
	}
	capList := make([]string, 0, len(caps))
	for c := range caps {
		capList = append(capList, c)
	}
	return map[string]interface{}{
		"agents":       len(agents),
		"capabilities": capList,
	}, nil
}
