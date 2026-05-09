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
	store                 *Store
	emit                  Emitter
	veniceBalanceProvider VeniceBalanceProvider
}

// NewRPCHandler constructs an AI connection RPC handler.
func NewRPCHandler(store *Store, emit Emitter) *RPCHandler {
	return &RPCHandler{store: store, emit: emit}
}

// SetVeniceBalanceProvider wires the host-side Venice account balance lookup
// used by the AI Connections settings surface.
func (h *RPCHandler) SetVeniceBalanceProvider(provider VeniceBalanceProvider) {
	if h == nil {
		return
	}
	h.veniceBalanceProvider = provider
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
	case "inference.veniceBalance":
		var parsed VeniceBalanceParams
		if err := decodeParams(params, &parsed); err != nil {
			return nil, err, true
		}
		result, err := h.veniceBalance(ctx, parsed)
		return result, err, true
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

// VeniceBalanceProvider resolves the Venice-side x402 balance for a configured
// Venice connection.
type VeniceBalanceProvider interface {
	VeniceBalance(context.Context, Connection) (*VeniceBalanceResult, error)
}

// VeniceBalanceProviderFunc adapts a function to VeniceBalanceProvider.
type VeniceBalanceProviderFunc func(context.Context, Connection) (*VeniceBalanceResult, error)

// VeniceBalance calls f.
func (f VeniceBalanceProviderFunc) VeniceBalance(ctx context.Context, connection Connection) (*VeniceBalanceResult, error) {
	return f(ctx, connection)
}

// VeniceBalanceParams is the request shape for inference.veniceBalance.
type VeniceBalanceParams struct {
	ConnectionID string `json:"connection_id"`
}

// VeniceBalanceResult is the response shape for inference.veniceBalance.
type VeniceBalanceResult struct {
	ConnectionID      string `json:"connection_id"`
	Wallet            string `json:"wallet"`
	Network           string `json:"network"`
	WalletAddress     string `json:"wallet_address"`
	BalanceUSD        string `json:"balance_usd"`
	CanConsume        bool   `json:"can_consume"`
	MinimumTopUpUSD   string `json:"minimum_top_up_usd,omitempty"`
	SuggestedTopUpUSD string `json:"suggested_top_up_usd,omitempty"`
	CheckedAt         string `json:"checked_at"`
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

func (h *RPCHandler) veniceBalance(ctx context.Context, params VeniceBalanceParams) (*VeniceBalanceResult, error) {
	connectionID := strings.TrimSpace(params.ConnectionID)
	if connectionID == "" {
		return nil, fmt.Errorf("connection_id is required")
	}
	if h.veniceBalanceProvider == nil {
		return nil, fmt.Errorf("Venice balance provider is not configured")
	}
	connection, ok, err := h.store.Get(connectionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("connection %q not found", connectionID)
	}
	if connection.Provider != ProviderVenice || connection.Auth.Method != AuthMethodX402 {
		return nil, fmt.Errorf("connection %q is not a Venice x402 connection", connectionID)
	}
	result, err := h.veniceBalanceProvider.VeniceBalance(ctx, connection)
	if err != nil {
		return nil, err
	}
	if result != nil && result.ConnectionID == "" {
		result.ConnectionID = connection.ID
	}
	return result, nil
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
