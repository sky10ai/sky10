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
	emit     Emitter
}

// NewRPCHandler creates an agent RPC handler.
func NewRPCHandler(registry *Registry, caller *Caller, emit Emitter) *RPCHandler {
	return &RPCHandler{registry: registry, caller: caller, emit: emit}
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

func (h *RPCHandler) rpcList(_ context.Context, _ json.RawMessage) (interface{}, error) {
	agents := h.registry.List()
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

	// For Phase 1, only local dispatch. Phase 2 adds cross-device routing.
	info := h.registry.Resolve(p.Agent)
	if info == nil {
		return nil, ErrAgentNotFound
	}

	result, err := h.caller.Call(ctx, info.Endpoint, p.Method, p.Params)
	if err != nil {
		return CallResult{Error: err.Error()}, nil
	}
	return CallResult{Result: result}, nil
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
