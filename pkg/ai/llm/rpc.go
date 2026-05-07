package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const connectionsUpdatedEvent = "inference:connections:updated"

// Emitter publishes daemon events to connected UI clients.
type Emitter func(event string, data interface{})

// RPCHandler dispatches inference.* RPC methods.
type RPCHandler struct {
	store *Store
	emit  Emitter
}

// NewRPCHandler constructs an AI connection RPC handler.
func NewRPCHandler(store *Store, emit Emitter) *RPCHandler {
	return &RPCHandler{store: store, emit: emit}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "inference.") {
		return nil, nil, false
	}
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("AI connection store is not configured"), true
	}

	switch method {
	case "inference.providers":
		return ProvidersResult{Providers: Providers()}, nil, true
	case "inference.connections":
		connections, err := h.store.List()
		if err != nil {
			return nil, err, true
		}
		return ConnectionsResult{Connections: connections, Count: len(connections)}, nil, true
	case "inference.connectionSave":
		var parsed ConnectionSaveParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		connection, err := h.store.Upsert(ctx, parsed.Connection)
		if err != nil {
			return nil, err, true
		}
		h.emitUpdated(connection.ID)
		return ConnectionSaveResult{Connection: connection}, nil, true
	case "inference.connectionDelete":
		var parsed ConnectionDeleteParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		if strings.TrimSpace(parsed.ID) == "" {
			return nil, fmt.Errorf("id is required"), true
		}
		if err := h.store.Delete(ctx, strings.TrimSpace(parsed.ID)); err != nil {
			return nil, err, true
		}
		h.emitUpdated(strings.TrimSpace(parsed.ID))
		return map[string]string{"status": "ok"}, nil, true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

// ProvidersResult is the response shape for inference.providers.
type ProvidersResult struct {
	Providers []Provider `json:"providers"`
}

// ConnectionsResult is the response shape for inference.connections.
type ConnectionsResult struct {
	Connections []Connection `json:"connections"`
	Count       int          `json:"count"`
}

// ConnectionSaveParams is the request shape for inference.connectionSave.
type ConnectionSaveParams struct {
	Connection Connection `json:"connection"`
}

// ConnectionSaveResult is the response shape for inference.connectionSave.
type ConnectionSaveResult struct {
	Connection Connection `json:"connection"`
}

// ConnectionDeleteParams is the request shape for inference.connectionDelete.
type ConnectionDeleteParams struct {
	ID string `json:"id"`
}

func (h *RPCHandler) emitUpdated(id string) {
	if h.emit == nil {
		return
	}
	h.emit(connectionsUpdatedEvent, map[string]string{"id": id})
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
