package rootassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler exposes daemon-backed RootAssistant UI state.
type RPCHandler struct {
	store *Store
}

// NewRPCHandler creates a RootAssistant RPC handler.
func NewRPCHandler(store *Store) *RPCHandler {
	return &RPCHandler{store: store}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(_ context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "rootAssistant.") {
		return nil, nil, false
	}
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("rootAssistant history store is not configured"), true
	}

	switch method {
	case "rootAssistant.historyList":
		var parsed HistoryListParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		result, err := h.store.List(parsed)
		return result, err, true
	case "rootAssistant.runSave":
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
