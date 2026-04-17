package kv

import (
	"context"
	"encoding/json"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestRPCListHidesInternalKeysByDefault(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	appendRPCTestEntry(t, store, "public/token", "value-1")
	appendRPCTestEntry(t, store, "_sys/secrets/heads/secret-1", "value-2")

	handler := NewRPCHandler(store)

	result, err := handler.rpcList(context.Background(), mustJSON(t, map[string]any{"prefix": ""}))
	if err != nil {
		t.Fatalf("rpcList: %v", err)
	}

	resp := result.(map[string]interface{})
	keys := resp["keys"].([]string)
	if len(keys) != 1 || keys[0] != "public/token" {
		t.Fatalf("visible keys = %v, want only public/token", keys)
	}

	result, err = handler.rpcList(context.Background(), mustJSON(t, map[string]any{
		"prefix":           "",
		"include_internal": true,
	}))
	if err != nil {
		t.Fatalf("rpcList(include_internal): %v", err)
	}
	resp = result.(map[string]interface{})
	keys = resp["keys"].([]string)
	if len(keys) != 2 {
		t.Fatalf("keys with include_internal = %v, want 2 keys", keys)
	}
}

func TestRPCGetAllAndStatusHideInternalKeysByDefault(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	appendRPCTestEntry(t, store, "public/token", "value-1")
	appendRPCTestEntry(t, store, "_sys/secrets/versions/secret-1/meta", "value-2")

	handler := NewRPCHandler(store)

	result, err := handler.rpcGetAll(context.Background(), mustJSON(t, map[string]any{"prefix": ""}))
	if err != nil {
		t.Fatalf("rpcGetAll: %v", err)
	}
	resp := result.(map[string]interface{})
	entries := resp["entries"].(map[string]string)
	if len(entries) != 1 || entries["public/token"] != "value-1" {
		t.Fatalf("visible entries = %v, want only public/token", entries)
	}

	status, err := store.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Keys != 1 {
		t.Fatalf("Status.Keys = %d, want 1 visible key", status.Keys)
	}
}

func TestRPCDeleteMatchingDryRun(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	appendRPCTestEntry(t, store, "users/alice", "value-1")
	appendRPCTestEntry(t, store, "users/team/bob", "value-2")
	appendRPCTestEntry(t, store, "config", "value-3")

	handler := NewRPCHandler(store)

	result, err := handler.rpcDeleteMatching(context.Background(), mustJSON(t, map[string]any{
		"pattern": "users/*",
		"dry_run": true,
	}))
	if err != nil {
		t.Fatalf("rpcDeleteMatching(dry_run): %v", err)
	}

	resp := result.(deleteMatchingResult)
	if resp.Count != 2 {
		t.Fatalf("dry-run count = %d, want 2", resp.Count)
	}
	if _, ok := store.Get("users/alice"); !ok {
		t.Fatal("users/alice should still exist after dry-run")
	}
	if _, ok := store.Get("users/team/bob"); !ok {
		t.Fatal("users/team/bob should still exist after dry-run")
	}
}

func TestRPCDeleteMatchingDeletesMatchedKeys(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	appendRPCTestEntry(t, store, "users/alice", "value-1")
	appendRPCTestEntry(t, store, "users/team/bob", "value-2")
	appendRPCTestEntry(t, store, "config", "value-3")

	handler := NewRPCHandler(store)

	result, err := handler.rpcDeleteMatching(context.Background(), mustJSON(t, map[string]any{
		"pattern": "users/*",
	}))
	if err != nil {
		t.Fatalf("rpcDeleteMatching: %v", err)
	}

	resp := result.(deleteMatchingResult)
	if resp.Count != 2 {
		t.Fatalf("delete count = %d, want 2", resp.Count)
	}
	if _, ok := store.Get("users/alice"); ok {
		t.Fatal("users/alice should be deleted")
	}
	if _, ok := store.Get("users/team/bob"); ok {
		t.Fatal("users/team/bob should be deleted")
	}
	if _, ok := store.Get("config"); !ok {
		t.Fatal("config should remain")
	}
}

func TestRPCDeleteMatchingHidesInternalKeysByDefault(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	appendRPCTestEntry(t, store, "public/token", "value-1")
	appendRPCTestEntry(t, store, "_sys/secrets/heads/secret-1", "value-2")

	handler := NewRPCHandler(store)

	result, err := handler.rpcDeleteMatching(context.Background(), mustJSON(t, map[string]any{
		"pattern": "*",
		"dry_run": true,
	}))
	if err != nil {
		t.Fatalf("rpcDeleteMatching(dry_run): %v", err)
	}

	resp := result.(deleteMatchingResult)
	if len(resp.Keys) != 1 || resp.Keys[0] != "public/token" {
		t.Fatalf("visible matches = %v, want only public/token", resp.Keys)
	}

	result, err = handler.rpcDeleteMatching(context.Background(), mustJSON(t, map[string]any{
		"pattern":          "*",
		"dry_run":          true,
		"include_internal": true,
	}))
	if err != nil {
		t.Fatalf("rpcDeleteMatching(include_internal): %v", err)
	}

	resp = result.(deleteMatchingResult)
	if resp.Count != 2 {
		t.Fatalf("matches with include_internal = %v, want 2 keys", resp.Keys)
	}
}

func TestRPCDeleteMatchingRequiresPattern(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	handler := NewRPCHandler(store)

	if _, err := handler.rpcDeleteMatching(context.Background(), mustJSON(t, map[string]any{"pattern": ""})); err == nil {
		t.Fatal("rpcDeleteMatching(empty pattern) error = nil, want error")
	}
}

func TestRPCSyncP2POnlyReturnsOK(t *testing.T) {
	t.Parallel()

	store := newRPCTestStore(t)
	handler := NewRPCHandler(store)

	result, err := handler.rpcSync(context.Background())
	if err != nil {
		t.Fatalf("rpcSync: %v", err)
	}
	resp := result.(map[string]string)
	if resp["status"] != "ok" {
		t.Fatalf("rpcSync status = %q, want ok", resp["status"])
	}
}

func newRPCTestStore(t *testing.T) *Store {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	store := New(nil, identity, Config{
		Namespace: "default",
		DataDir:   t.TempDir(),
	}, nil)
	store.nsKey = []byte("test-namespace-key")
	store.nsID = "test-nsid"
	return store
}

func appendRPCTestEntry(t *testing.T, store *Store, key, value string) {
	t.Helper()
	if err := store.localLog.AppendLocal(Entry{
		Type:  Set,
		Key:   key,
		Value: []byte(value),
	}); err != nil {
		t.Fatalf("AppendLocal(%s): %v", key, err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}
