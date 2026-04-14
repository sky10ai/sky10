package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestRPCPutRejectsAgentPolicy(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)

	_, err := handler.rpcPut(context.Background(), mustRPCJSON(t, map[string]any{
		"name":           "openai",
		"payload":        base64.StdEncoding.EncodeToString([]byte("sk-test")),
		"allowed_agents": []string{"A-agent"},
	}))
	if err == nil || !strings.Contains(err.Error(), "device-scoped only") {
		t.Fatalf("rpcPut error = %v, want deferred device-scoped error", err)
	}
}

func TestRPCRewrapRejectsAgentPolicy(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)

	_, err := handler.rpcRewrap(context.Background(), mustRPCJSON(t, map[string]any{
		"id_or_name":       "openai",
		"require_approval": true,
	}))
	if err == nil || !strings.Contains(err.Error(), "device-scoped only") {
		t.Fatalf("rpcRewrap error = %v, want deferred device-scoped error", err)
	}
}

func TestRPCGetRejectsAgentRequesterMode(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)
	if _, err := handler.store.Put(context.Background(), PutParams{
		Name:    "openai",
		Payload: []byte("sk-test"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, err := handler.rpcGet(mustRPCJSON(t, map[string]any{
		"id_or_name":     "openai",
		"requester_type": RequesterAgent,
		"requester_id":   "A-agent",
	}))
	if err == nil || !strings.Contains(err.Error(), "not part of secrets v1") {
		t.Fatalf("rpcGet error = %v, want v1 rejection", err)
	}
}

func TestRPCGetAndListDoNotExposePolicy(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)
	if _, err := handler.store.Put(context.Background(), PutParams{
		Name:    "policy-secret",
		Payload: []byte("value"),
		Policy: AccessPolicy{
			AllowedAgents: []string{"A-agent"},
		},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := handler.rpcGet(mustRPCJSON(t, map[string]any{
		"id_or_name": "policy-secret",
	}))
	if err != nil {
		t.Fatalf("rpcGet: %v", err)
	}
	getResp := got.(map[string]interface{})
	if _, ok := getResp["policy"]; ok {
		t.Fatalf("rpcGet unexpectedly exposed policy: %v", getResp["policy"])
	}

	listed, err := handler.rpcList()
	if err != nil {
		t.Fatalf("rpcList: %v", err)
	}
	listResp := listed.(map[string]interface{})
	items := listResp["items"].([]map[string]interface{})
	if len(items) != 1 {
		t.Fatalf("rpcList items = %d, want 1", len(items))
	}
	if _, ok := items[0]["policy"]; ok {
		t.Fatalf("rpcList unexpectedly exposed policy: %v", items[0]["policy"])
	}
}

func TestRPCDeleteRemovesSecret(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)
	if _, err := handler.store.Put(context.Background(), PutParams{
		Name:    "delete-me",
		Payload: []byte("value"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := handler.rpcDelete(context.Background(), mustRPCJSON(t, map[string]any{
		"id_or_name": "delete-me",
	}))
	if err != nil {
		t.Fatalf("rpcDelete: %v", err)
	}
	if got.(map[string]string)["status"] != "ok" {
		t.Fatalf("rpcDelete result = %#v, want status ok", got)
	}

	if _, err := handler.rpcGet(mustRPCJSON(t, map[string]any{
		"id_or_name": "delete-me",
	})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rpcGet after delete error = %v, want ErrNotFound", err)
	}

	listed, err := handler.rpcList()
	if err != nil {
		t.Fatalf("rpcList: %v", err)
	}
	listResp := listed.(map[string]interface{})
	items := listResp["items"].([]map[string]interface{})
	if len(items) != 0 {
		t.Fatalf("rpcList items after delete = %+v, want none", items)
	}
}

func TestRPCDeleteRequiresIDOrName(t *testing.T) {
	t.Parallel()

	handler := newRPCTestHandler(t)
	_, err := handler.rpcDelete(context.Background(), mustRPCJSON(t, map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "id_or_name is required") {
		t.Fatalf("rpcDelete error = %v, want id_or_name required", err)
	}
}

func newRPCTestHandler(t *testing.T) *RPCHandler {
	t.Helper()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	return NewRPCHandler(store)
}

func mustRPCJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}
