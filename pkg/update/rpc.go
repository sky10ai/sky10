package update

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// RPCHandler dispatches system.* RPC methods.
type RPCHandler struct {
	version  string
	emit     Emitter
	updating atomic.Bool

	restartHandler RestartHandler
	restartDelay   time.Duration
}

// RestartHandler restarts the daemon after the RPC response has been sent.
type RestartHandler func() error

// NewRPCHandler creates an RPC handler for system operations.
func NewRPCHandler(version string, emit Emitter) *RPCHandler {
	return &RPCHandler{
		version:      version,
		emit:         emit,
		restartDelay: 500 * time.Millisecond,
	}
}

// SetRestartHandler configures system.restart.
func (h *RPCHandler) SetRestartHandler(fn RestartHandler) {
	h.restartHandler = fn
}

// Dispatch handles system.* methods.
func (h *RPCHandler) Dispatch(_ context.Context, method string, _ json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "system.") {
		return nil, nil, false
	}

	switch method {
	case "system.checkUpdate":
		return h.rpcCheckUpdate()
	case "system.update":
		return h.rpcUpdate()
	case "system.restart":
		return h.rpcRestart()
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

func (h *RPCHandler) rpcCheckUpdate() (interface{}, error, bool) {
	info, err := Check(h.version)
	if err != nil {
		return nil, err, true
	}
	return info, nil, true
}

func (h *RPCHandler) rpcUpdate() (interface{}, error, bool) {
	if !h.updating.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("update already in progress"), true
	}

	go func() {
		defer h.updating.Store(false)

		info, err := Check(h.version)
		if err != nil {
			h.emit("update:error", map[string]string{"message": err.Error()})
			return
		}
		if !info.Available {
			return
		}

		err = Apply(info, func(downloaded, total int64) {
			h.emit("update:progress", map[string]int64{
				"downloaded": downloaded,
				"total":      total,
			})
		})
		if err != nil {
			h.emit("update:error", map[string]string{"message": err.Error()})
			return
		}

		if _, err := ApplyMenu(info); err != nil {
			logger().Warn("could not update sky10-menu", "error", err)
		}

		h.emit("update:complete", map[string]string{
			"previous": info.Current,
			"updated":  info.Latest,
		})
	}()

	return map[string]string{"status": "checking"}, nil, true
}

func (h *RPCHandler) rpcRestart() (interface{}, error, bool) {
	if h.restartHandler == nil {
		return nil, fmt.Errorf("system.restart not available"), true
	}

	go func() {
		if h.restartDelay > 0 {
			time.Sleep(h.restartDelay)
		}
		if err := h.restartHandler(); err != nil {
			logger().Warn("system.restart failed", "error", err)
		}
	}()

	return map[string]string{"status": "restarting"}, nil, true
}
