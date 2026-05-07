package rootagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler exposes daemon-backed RootAgent UI state.
type RPCHandler struct {
	store *Store
}

// NewRPCHandler creates a RootAgent RPC handler.
func NewRPCHandler(store *Store) *RPCHandler {
	return &RPCHandler{store: store}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(_ context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "rootAgent.") {
		return nil, nil, false
	}
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("rootAgent history store is not configured"), true
	}

	switch method {
	case "rootAgent.historyList":
		var parsed HistoryListParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		result, err := h.store.List(parsed)
		return result, err, true
	case "rootAgent.runSave":
		var parsed RunSaveParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		result, err := h.store.Save(parsed)
		return result, err, true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

func decodeParams(params json.RawMessage, target interface{}) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, target); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}
