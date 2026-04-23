package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
	messagingadapters "github.com/sky10/sky10/pkg/messengers/adapters"
)

// ProcessResolver resolves the process spec to supervise for one adapter id.
type ProcessResolver func(adapterID string) (messagingruntime.ProcessSpec, error)

// Config configures one messaging RPC handler.
type Config struct {
	Broker          *messagingbroker.Broker
	Store           *messagingstore.Store
	ProcessResolver ProcessResolver
}

// Handler dispatches messaging.* RPC methods.
type Handler struct {
	broker          *messagingbroker.Broker
	store           *messagingstore.Store
	processResolver ProcessResolver
}

// NewHandler creates a messaging RPC handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		broker:          cfg.Broker,
		store:           cfg.Store,
		processResolver: cfg.ProcessResolver,
	}
}

// Dispatch implements the repo RPC handler contract.
func (h *Handler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "messaging.") {
		return nil, nil, false
	}
	if h == nil || h.broker == nil || h.store == nil {
		return nil, fmt.Errorf("messaging rpc handler is not configured"), true
	}

	switch method {
	case "messaging.adapters":
		return h.rpcAdapters(), nil, true
	case "messaging.connections":
		return h.rpcConnections(), nil, true
	case "messaging.connectBuiltin":
		result, err := h.rpcConnectBuiltin(ctx, params)
		return result, err, true
	case "messaging.connectConnection":
		result, err := h.rpcConnectConnection(ctx, params)
		return result, err, true
	case "messaging.pollConnection":
		result, err := h.rpcPollConnection(ctx, params)
		return result, err, true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

type adapterInfo struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type connectBuiltinParams struct {
	Connection messaging.Connection `json:"connection"`
}

type connectionParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Limit        int                    `json:"limit,omitempty"`
}

func (h *Handler) rpcAdapters() map[string]interface{} {
	items := messagingadapters.Builtins()
	adapters := make([]adapterInfo, 0, len(items))
	for _, item := range items {
		adapters = append(adapters, adapterInfo{
			Name:    item.Name,
			Summary: item.Summary,
		})
	}
	return map[string]interface{}{
		"adapters": adapters,
		"count":    len(adapters),
	}
}

func (h *Handler) rpcConnections() map[string]interface{} {
	connections := h.store.ListConnections()
	return map[string]interface{}{
		"connections": connections,
		"count":       len(connections),
	}
}

func (h *Handler) rpcConnectBuiltin(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.processResolver == nil {
		return nil, fmt.Errorf("messaging builtin process resolver is not configured")
	}

	var p connectBuiltinParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := p.Connection.Validate(); err != nil {
		return nil, err
	}

	process, err := h.processResolver(string(p.Connection.AdapterID))
	if err != nil {
		return nil, err
	}
	if err := h.broker.UpsertConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: p.Connection,
		Process:    process,
	}); err != nil {
		return nil, err
	}
	return h.broker.ConnectConnection(ctx, p.Connection.ID)
}

func (h *Handler) rpcConnectConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.ConnectConnection(ctx, p.ConnectionID)
}

func (h *Handler) rpcPollConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.PollConnection(ctx, p.ConnectionID, p.Limit)
}

func parseConnectionParams(params json.RawMessage) (connectionParams, error) {
	var p connectionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return connectionParams{}, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return connectionParams{}, fmt.Errorf("connection_id is required")
	}
	return p, nil
}
