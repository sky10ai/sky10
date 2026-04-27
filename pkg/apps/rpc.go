package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

type Emitter func(event string, data interface{})

// RPCHandler dispatches apps.* RPC methods.
type RPCHandler struct {
	emit Emitter

	mu         sync.Mutex
	installing map[ID]bool

	list      func() []AppInfo
	lookup    func(string) (*AppInfo, error)
	status    func(ID) (*Status, error)
	check     func(ID) (*ReleaseInfo, error)
	upgrade   func(ID, ProgressFunc) (*ReleaseInfo, error)
	uninstall func(ID, UninstallAuditInfo) (*UninstallResult, error)
}

// NewRPCHandler creates an RPC handler for managed helper apps.
func NewRPCHandler(emit Emitter) *RPCHandler {
	return &RPCHandler{
		emit:       emit,
		installing: make(map[ID]bool),
		list:       List,
		lookup:     Lookup,
		status:     StatusFor,
		check:      CheckLatest,
		upgrade:    Upgrade,
		uninstall:  UninstallWithAudit,
	}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "apps.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "apps.list":
		result, err = h.rpcList()
	case "apps.status":
		result, err = h.rpcStatus(params)
	case "apps.install":
		result, err = h.rpcInstall(params)
	case "apps.uninstall":
		result, err = h.rpcUninstall(ctx, params)
	case "apps.checkUpdate":
		result, err = h.rpcCheckUpdate(params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

type rpcAppParams struct {
	ID string `json:"id"`
}

func (h *RPCHandler) rpcList() (interface{}, error) {
	return map[string]interface{}{
		"apps": h.list(),
	}, nil
}

func (h *RPCHandler) rpcStatus(params json.RawMessage) (interface{}, error) {
	app, err := h.parseApp(params)
	if err != nil {
		return nil, err
	}
	return h.status(app.ID)
}

func (h *RPCHandler) rpcInstall(params json.RawMessage) (interface{}, error) {
	app, err := h.parseApp(params)
	if err != nil {
		return nil, err
	}
	if !h.beginOperation(app.ID) {
		return nil, fmt.Errorf("%s operation already in progress", app.ID)
	}

	go func() {
		defer h.finishOperation(app.ID)

		info, err := h.upgrade(app.ID, func(downloaded, total int64) {
			h.emitEvent("apps:install:progress", map[string]interface{}{
				"id":         app.ID,
				"downloaded": downloaded,
				"total":      total,
			})
		})
		if err != nil {
			h.emitEvent("apps:install:error", map[string]string{
				"id":      string(app.ID),
				"message": err.Error(),
			})
			return
		}
		if !info.Available {
			h.emitEvent("apps:install:complete", map[string]string{
				"id":      string(app.ID),
				"version": info.Current,
				"status":  "already up to date",
			})
			return
		}
		h.emitEvent("apps:install:complete", map[string]string{
			"id":      string(app.ID),
			"version": info.Latest,
			"status":  "installed",
		})
	}()

	return map[string]string{
		"id":     string(app.ID),
		"status": "installing",
	}, nil
}

func (h *RPCHandler) rpcUninstall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	app, err := h.parseApp(params)
	if err != nil {
		return nil, err
	}
	if !h.beginOperation(app.ID) {
		return nil, fmt.Errorf("%s operation already in progress", app.ID)
	}
	defer h.finishOperation(app.ID)

	audit := UninstallAuditInfo{
		Source: "apps.rpc",
		Method: "apps.uninstall",
	}
	if info, ok := skyrpc.CallerInfoFromContext(ctx); ok {
		audit.Transport = info.Transport
		audit.Remote = info.Remote
	}
	return h.uninstall(app.ID, audit)
}

func (h *RPCHandler) rpcCheckUpdate(params json.RawMessage) (interface{}, error) {
	app, err := h.parseApp(params)
	if err != nil {
		return nil, err
	}
	return h.check(app.ID)
}

func (h *RPCHandler) parseApp(params json.RawMessage) (*AppInfo, error) {
	var p rpcAppParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return h.lookup(p.ID)
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
