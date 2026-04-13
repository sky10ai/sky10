package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches secrets.* RPC methods.
type RPCHandler struct {
	store *Store
}

// NewRPCHandler creates a new secrets RPC handler.
func NewRPCHandler(store *Store) *RPCHandler {
	return &RPCHandler{store: store}
}

// Dispatch implements rpc.Handler.
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "secrets.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "secrets.put":
		result, err = h.rpcPut(ctx, params)
	case "secrets.get":
		result, err = h.rpcGet(params)
	case "secrets.list":
		result, err = h.rpcList()
	case "secrets.devices":
		result, err = h.rpcDevices()
	case "secrets.rewrap":
		result, err = h.rpcRewrap(ctx, params)
	case "secrets.sync":
		result, err = h.rpcSync(ctx)
	case "secrets.status":
		result, err = h.rpcStatus()
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}

	return result, err, true
}

type putParams struct {
	ID               string   `json:"id,omitempty"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind,omitempty"`
	ContentType      string   `json:"content_type,omitempty"`
	PayloadBase64    string   `json:"payload"`
	RecipientDevices []string `json:"recipient_devices,omitempty"`
	AllowedAgents    []string `json:"allowed_agents,omitempty"`
	RequireApproval  bool     `json:"require_approval,omitempty"`
}

func (h *RPCHandler) rpcPut(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p putParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if p.PayloadBase64 == "" {
		return nil, fmt.Errorf("payload is required")
	}
	payload, err := base64.StdEncoding.DecodeString(p.PayloadBase64)
	if err != nil {
		return nil, fmt.Errorf("payload must be base64: %w", err)
	}

	summary, err := h.store.Put(ctx, PutParams{
		ID:                 p.ID,
		Name:               p.Name,
		Kind:               p.Kind,
		ContentType:        p.ContentType,
		Payload:            payload,
		RecipientDeviceIDs: p.RecipientDevices,
		Policy: AccessPolicy{
			AllowedAgents:   p.AllowedAgents,
			RequireApproval: p.RequireApproval,
		},
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

type getParams struct {
	IDOrName      string `json:"id_or_name"`
	RequesterType string `json:"requester_type,omitempty"`
	RequesterID   string `json:"requester_id,omitempty"`
}

func (h *RPCHandler) rpcGet(params json.RawMessage) (interface{}, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.IDOrName == "" {
		return nil, fmt.Errorf("id_or_name is required")
	}

	secret, err := h.store.Get(p.IDOrName, Requester{
		Type: p.RequesterType,
		ID:   p.RequesterID,
	})
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":                   secret.ID,
		"name":                 secret.Name,
		"kind":                 secret.Kind,
		"content_type":         secret.ContentType,
		"size":                 secret.Size,
		"sha256":               secret.SHA256,
		"created_at":           secret.CreatedAt,
		"updated_at":           secret.UpdatedAt,
		"recipient_device_ids": secret.RecipientDeviceIDs,
		"policy":               secret.Policy,
		"version_id":           secret.VersionID,
		"payload":              base64.StdEncoding.EncodeToString(secret.Payload),
	}, nil
}

func (h *RPCHandler) rpcList() (interface{}, error) {
	items, err := h.store.List()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"items": items,
		"count": len(items),
	}, nil
}

func (h *RPCHandler) rpcDevices() (interface{}, error) {
	devices := h.store.Devices()
	return map[string]interface{}{
		"devices": devices,
		"count":   len(devices),
	}, nil
}

type rewrapParams struct {
	IDOrName         string   `json:"id_or_name"`
	RecipientDevices []string `json:"recipient_devices,omitempty"`
	AllowedAgents    []string `json:"allowed_agents,omitempty"`
	RequireApproval  bool     `json:"require_approval,omitempty"`
}

func (h *RPCHandler) rpcRewrap(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p rewrapParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.IDOrName == "" {
		return nil, fmt.Errorf("id_or_name is required")
	}
	summary, err := h.store.Rewrap(ctx, RewrapParams{
		IDOrName:           p.IDOrName,
		RecipientDeviceIDs: p.RecipientDevices,
		Policy: AccessPolicy{
			AllowedAgents:   p.AllowedAgents,
			RequireApproval: p.RequireApproval,
		},
	})
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func (h *RPCHandler) rpcSync(ctx context.Context) (interface{}, error) {
	if err := h.store.SyncOnce(ctx); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func (h *RPCHandler) rpcStatus() (interface{}, error) {
	return h.store.Status()
}
