package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type RPCHandler struct {
	manager *Manager
}

func NewRPCHandler(manager *Manager) *RPCHandler {
	return &RPCHandler{manager: manager}
}

func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "sandbox.") {
		return nil, nil, false
	}
	if h.manager == nil {
		return nil, fmt.Errorf("sandbox manager unavailable"), true
	}

	switch method {
	case "sandbox.list":
		result, err := h.manager.List(ctx)
		return result, err, true
	case "sandbox.get":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.get params: %w", err), true
		}
		result, err := h.manager.Get(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	case "sandbox.logs":
		var p LogsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.logs params: %w", err), true
		}
		result, err := h.manager.Logs(coalesceSandboxKey(p.Name, p.Slug), p.Limit)
		return result, err, true
	case "sandbox.runtime.status":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.runtime.status params: %w", err), true
		}
		result, err := h.manager.RuntimeStatus(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	case "sandbox.runtime.upgrade":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.runtime.upgrade params: %w", err), true
		}
		result, err := h.manager.RuntimeUpgrade(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	case "sandbox.create":
		var p CreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.create params: %w", err), true
		}
		result, err := h.manager.Create(ctx, p)
		return result, err, true
	case "sandbox.ensure":
		var p CreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.ensure params: %w", err), true
		}
		result, err := h.manager.Ensure(ctx, p)
		return result, err, true
	case "sandbox.start":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.start params: %w", err), true
		}
		result, err := h.manager.Start(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	case "sandbox.stop":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.stop params: %w", err), true
		}
		result, err := h.manager.Stop(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	case "sandbox.delete":
		var p NamedParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("parse sandbox.delete params: %w", err), true
		}
		result, err := h.manager.Delete(ctx, coalesceSandboxKey(p.Name, p.Slug))
		return result, err, true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

func coalesceSandboxKey(name, slug string) string {
	if strings.TrimSpace(slug) != "" {
		return slug
	}
	return name
}
