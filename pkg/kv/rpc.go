package kv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RPCHandler dispatches skykv.* RPC methods. Implements the ExternalHandler
// interface from pkg/fs/rpc.go.
type RPCHandler struct {
	store *Store
}

// NewRPCHandler creates a KV RPC handler for the given store.
func NewRPCHandler(store *Store) *RPCHandler {
	return &RPCHandler{store: store}
}

// Dispatch handles a skykv.* method. Returns (result, error, handled).
// If the method is not recognized, returns (nil, nil, false).
func (h *RPCHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "skykv.") {
		return nil, nil, false
	}

	var result interface{}
	var err error

	switch method {
	case "skykv.set":
		result, err = h.rpcSet(ctx, params)
	case "skykv.get":
		result, err = h.rpcGet(ctx, params)
	case "skykv.delete":
		result, err = h.rpcDelete(ctx, params)
	case "skykv.list":
		result, err = h.rpcList(ctx, params)
	case "skykv.getAll":
		result, err = h.rpcGetAll(ctx, params)
	case "skykv.sync":
		result, err = h.rpcSync(ctx)
	case "skykv.status":
		result, err = h.rpcStatus(ctx)
	default:
		return nil, nil, false
	}

	return result, err, true
}

type setParams struct {
	Key   string `json:"key"`
	Value string `json:"value"` // base64 or plain text
}

func (h *RPCHandler) rpcSet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p setParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if err := h.store.Set(ctx, p.Key, []byte(p.Value)); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

type getParams struct {
	Key string `json:"key"`
}

type getResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Found bool   `json:"found"`
}

func (h *RPCHandler) rpcGet(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	val, ok := h.store.Get(p.Key)
	return getResult{Key: p.Key, Value: string(val), Found: ok}, nil
}

type deleteParams struct {
	Key string `json:"key"`
}

func (h *RPCHandler) rpcDelete(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p deleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if err := h.store.Delete(ctx, p.Key); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

type listParams struct {
	Prefix string `json:"prefix"`
}

func (h *RPCHandler) rpcList(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p listParams
	if params != nil {
		json.Unmarshal(params, &p)
	}
	keys := h.store.List(p.Prefix)
	return map[string]interface{}{"keys": keys, "count": len(keys)}, nil
}

type getAllParams struct {
	Prefix string `json:"prefix"`
}

func (h *RPCHandler) rpcGetAll(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p getAllParams
	if params != nil {
		json.Unmarshal(params, &p)
	}
	entries := h.store.GetAll(p.Prefix)
	result := make(map[string]string, len(entries))
	for k, v := range entries {
		result[k] = string(v)
	}
	return map[string]interface{}{"entries": result, "count": len(result)}, nil
}

func (h *RPCHandler) rpcSync(ctx context.Context) (interface{}, error) {
	if err := h.store.SyncOnce(ctx); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func (h *RPCHandler) rpcStatus(_ context.Context) (interface{}, error) {
	status, err := h.store.Status()
	if err != nil {
		return nil, err
	}
	return status, nil
}
