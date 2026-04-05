package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
)

// RPCHandler dispatches system.* RPC methods.
type RPCHandler struct {
	version  string
	emit     Emitter
	updating atomic.Bool
}

// NewRPCHandler creates an RPC handler for system operations.
func NewRPCHandler(version string, emit Emitter) *RPCHandler {
	return &RPCHandler{version: version, emit: emit}
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
			slog.Warn("could not update sky10-menu", "error", err)
		}

		h.emit("update:complete", map[string]string{
			"previous": info.Current,
			"updated":  info.Latest,
		})
	}()

	return map[string]string{"status": "checking"}, nil, true
}
