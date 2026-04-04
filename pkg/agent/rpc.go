package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Emitter sends SSE events to connected subscribers.
type Emitter func(event string, data interface{})

// RPCHandler dispatches agent.* RPC methods.
type RPCHandler struct {
	registry *Registry
	caller   *Caller
	router   *Router // nil until cross-device wiring (Phase 2)
	emit     Emitter
}

// NewRPCHandler creates an agent RPC handler.
func NewRPCHandler(registry *Registry, caller *Caller, emit Emitter) *RPCHandler {
	return &RPCHandler{registry: registry, caller: caller, emit: emit}
}

// SetRouter attaches a cross-device router. Once set, agent.call and
// agent.list use the router for remote dispatch and aggregation.
func (h *RPCHandler) SetRouter(r *Router) {
	h.router = r
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
	case "agent.call":
		result, err = h.rpcCall(ctx, params)
	case "agent.discover":
		result, err = h.rpcDiscover(ctx, params)
	case "agent.status":
		result, err = h.rpcStatus(ctx)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

func (h *RPCHandler) rpcRegister(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p RegisterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if p.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	// Verify agent is reachable before accepting registration.
	if err := h.caller.Ping(ctx, p.Endpoint); err != nil {
		return nil, fmt.Errorf("agent not reachable at %s: %w", p.Endpoint, err)
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

func (h *RPCHandler) rpcCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p CallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Agent == "" {
		return nil, fmt.Errorf("agent is required")
	}
	if p.Method == "" {
		return nil, fmt.Errorf("method is required")
	}

	if h.router != nil {
		return h.router.Call(ctx, p)
	}

	// Local-only fallback (no router wired).
	info := h.registry.Resolve(p.Agent)
	if info == nil {
		return nil, ErrAgentNotFound
	}
	result, err := h.caller.Call(ctx, info.Endpoint, p.Method, p.Params)
	if err != nil {
		return CallResult{Error: err.Error()}, nil
	}
	return &CallResult{Result: result}, nil
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
