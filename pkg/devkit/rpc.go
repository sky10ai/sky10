package devkit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type Emitter func(event string, data interface{})

// RPCHandler dispatches devkit.* RPC methods.
type RPCHandler struct {
	emit Emitter

	mu         sync.Mutex
	installing map[ID]bool

	list      func() []ToolInfo
	lookup    func(string) (*ToolInfo, error)
	status    func(ID) (*Status, error)
	check     func(ID) (*ReleaseInfo, error)
	upgrade   func(ID, string, ProgressFunc) (*ReleaseInfo, error)
	uninstall func(ID) (*UninstallResult, error)
}

// NewRPCHandler creates an RPC handler for managed devkit tools.
func NewRPCHandler(emit Emitter) *RPCHandler {
	return &RPCHandler{
		emit:       emit,
		installing: make(map[ID]bool),
		list:       List,
		lookup:     Lookup,
		status:     StatusFor,
		check:      CheckLatest,
		upgrade:    Upgrade,
		uninstall:  Uninstall,
	}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(_ context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "devkit.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "devkit.list":
		result, err = h.rpcList()
	case "devkit.status":
		result, err = h.rpcStatus(params)
	case "devkit.install":
		result, err = h.rpcInstall(params)
	case "devkit.uninstall":
		result, err = h.rpcUninstall(params)
	case "devkit.checkUpdate":
		result, err = h.rpcCheckUpdate(params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

type rpcToolParams struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

func (h *RPCHandler) rpcList() (interface{}, error) {
	return map[string]interface{}{
		"tools": h.list(),
	}, nil
}

func (h *RPCHandler) rpcStatus(params json.RawMessage) (interface{}, error) {
	tool, err := h.parseTool(params)
	if err != nil {
		return nil, err
	}
	return h.status(tool.ID)
}

func (h *RPCHandler) rpcInstall(params json.RawMessage) (interface{}, error) {
	tool, parsed, err := h.parseInstall(params)
	if err != nil {
		return nil, err
	}
	if !h.beginOperation(tool.ID) {
		return nil, fmt.Errorf("%s operation already in progress", tool.ID)
	}

	go func() {
		defer h.finishOperation(tool.ID)

		info, err := h.upgrade(tool.ID, parsed.Version, func(downloaded, total int64) {
			h.emitEvent("devkit:install:progress", map[string]interface{}{
				"id":         tool.ID,
				"downloaded": downloaded,
				"total":      total,
			})
		})
		if err != nil {
			h.emitEvent("devkit:install:error", map[string]string{
				"id":      string(tool.ID),
				"message": err.Error(),
			})
			return
		}
		if !info.Available {
			h.emitEvent("devkit:install:complete", map[string]string{
				"id":      string(tool.ID),
				"version": info.Current,
				"status":  "already up to date",
			})
			return
		}
		h.emitEvent("devkit:install:complete", map[string]string{
			"id":      string(tool.ID),
			"version": info.Latest,
			"status":  "installed",
		})
	}()

	return map[string]string{
		"id":     string(tool.ID),
		"status": "installing",
	}, nil
}

func (h *RPCHandler) rpcUninstall(params json.RawMessage) (interface{}, error) {
	tool, err := h.parseTool(params)
	if err != nil {
		return nil, err
	}
	if !h.beginOperation(tool.ID) {
		return nil, fmt.Errorf("%s operation already in progress", tool.ID)
	}
	defer h.finishOperation(tool.ID)
	return h.uninstall(tool.ID)
}

func (h *RPCHandler) rpcCheckUpdate(params json.RawMessage) (interface{}, error) {
	tool, err := h.parseTool(params)
	if err != nil {
		return nil, err
	}
	return h.check(tool.ID)
}

func (h *RPCHandler) parseTool(params json.RawMessage) (*ToolInfo, error) {
	var p rpcToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return h.lookup(p.ID)
}

func (h *RPCHandler) parseInstall(params json.RawMessage) (*ToolInfo, rpcToolParams, error) {
	var p rpcToolParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, p, fmt.Errorf("invalid params: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	p.Version = strings.TrimSpace(p.Version)
	if p.ID == "" {
		return nil, p, fmt.Errorf("id is required")
	}
	tool, err := h.lookup(p.ID)
	if err != nil {
		return nil, p, err
	}
	return tool, p, nil
}

func (h *RPCHandler) beginOperation(id ID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.installing[id] {
		return false
	}
	h.installing[id] = true
	return true
}

func (h *RPCHandler) finishOperation(id ID) {
	h.mu.Lock()
	delete(h.installing, id)
	h.mu.Unlock()
}

func (h *RPCHandler) emitEvent(event string, data interface{}) {
	if h.emit == nil {
		return
	}
	h.emit(event, data)
}
