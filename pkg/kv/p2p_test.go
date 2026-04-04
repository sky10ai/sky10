package kv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

// TestStoreNilBackend verifies that a KV store works with nil backend
// (P2P-only mode) — Set, Get, Delete, List all function correctly.
func TestStoreNilBackend(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()

	store := New(nil, identity, Config{
		Namespace: "test-p2p",
		DataDir:   t.TempDir(),
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run should not block or crash (it resolves keys locally).
	done := make(chan error, 1)
	go func() { done <- store.Run(ctx) }()

	// Give Run time to resolve keys.
	time.Sleep(100 * time.Millisecond)

	// Set + Get
	if err := store.Set(ctx, "greeting", []byte("hello")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, ok := store.Get("greeting")
	if !ok || string(val) != "hello" {
		t.Errorf("Get(greeting) = %q, %v; want hello, true", val, ok)
	}

	// Delete + verify gone
	if err := store.Delete(ctx, "greeting"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok = store.Get("greeting")
	if ok {
		t.Error("greeting should be deleted")
	}

	// List
	store.Set(ctx, "a/1", []byte("1"))
	store.Set(ctx, "a/2", []byte("2"))
	store.Set(ctx, "b/1", []byte("3"))
	keys := store.List("a/")
	if len(keys) != 2 {
		t.Errorf("List(a/) = %v, want 2 keys", keys)
	}

	cancel()
	<-done
}

// TestStoreNilBackendNamespaceKeyStable verifies that namespace keys are
// generated once and cached — restarting with nil backend uses the same key.
func TestStoreNilBackendNamespaceKeyStable(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	dataDir := t.TempDir()
	deviceID := ShortDeviceID(identity)

	// First run — generates key.
	store1 := New(nil, identity, Config{Namespace: "stable-ns", DataDir: dataDir}, nil)
	ctx1, cancel1 := context.WithCancel(context.Background())
	go store1.Run(ctx1)
	time.Sleep(100 * time.Millisecond)
	store1.Set(ctx1, "x", []byte("1"))
	cancel1()

	nsKey1, _ := loadCachedKey("stable-ns", deviceID)

	// Second run — should reuse the same key.
	store2 := New(nil, identity, Config{Namespace: "stable-ns", DataDir: dataDir}, nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	go store2.Run(ctx2)
	time.Sleep(100 * time.Millisecond)

	nsKey2, _ := loadCachedKey("stable-ns", deviceID)
	if string(nsKey1) != string(nsKey2) {
		t.Error("namespace key should be stable across restarts")
	}

	// Data should persist.
	val, ok := store2.Get("x")
	if !ok || string(val) != "1" {
		t.Errorf("Get(x) = %q, %v; want 1, true", val, ok)
	}
	cancel2()
}

// TestDiffAndMergeFunction verifies the shared diffAndMerge logic used by
// both the S3 poller and P2P sync handler.
func TestDiffAndMergeFunction(t *testing.T) {
	t.Parallel()

	localDir := t.TempDir()
	localLog := NewLocalLog(filepath.Join(localDir, "ops.jsonl"), "deviceA")

	// Seed local with one entry.
	localLog.AppendLocal(Entry{Type: Set, Key: "shared", Value: []byte("local")})

	// Remote snapshot has a newer value for "shared" and a new key "remote-only".
	remoteSnap := buildSnapshot(nil, []Entry{
		{Type: Set, Key: "shared", Value: []byte("remote"), Device: "deviceB",
			Timestamp: time.Now().Add(time.Second).Unix(), Seq: 1},
		{Type: Set, Key: "remote-only", Value: []byte("yes"), Device: "deviceB",
			Timestamp: time.Now().Unix(), Seq: 2},
	})

	merged := diffAndMerge(localLog, remoteSnap, nil, nil)
	if merged != 2 {
		t.Errorf("diffAndMerge = %d, want 2", merged)
	}

	// "shared" should be overwritten by remote (higher timestamp).
	vi, ok := localLog.Lookup("shared")
	if !ok {
		t.Fatal("shared should exist")
	}
	if string(vi.Value) != "remote" {
		t.Errorf("shared = %q, want remote", vi.Value)
	}

	// "remote-only" should be added.
	vi, ok = localLog.Lookup("remote-only")
	if !ok || string(vi.Value) != "yes" {
		t.Errorf("remote-only = %q, %v; want yes, true", vi.Value, ok)
	}
}
