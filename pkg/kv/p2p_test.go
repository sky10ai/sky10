package kv

import (
	"context"
	"encoding/json"
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

// Regression: encrypted snapshot was encoded with fmt.Sprintf("%q") which
// produces Go string escaping ("\x00\x01..."), not valid JSON. The
// receiver's json.Unmarshal silently failed, so KV never synced over P2P.
func TestP2PSyncMsgEncoding(t *testing.T) {
	t.Parallel()

	// Build a snapshot with real data.
	snap := buildSnapshot(nil, []Entry{
		{Type: Set, Key: "hello", Value: []byte("world"), Device: "dev1",
			Timestamp: time.Now().Unix(), Seq: 1},
	})

	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt with a test key.
	nsKey, _ := skykey.GenerateSymmetricKey()
	encrypted, err := encrypt(data, nsKey)
	if err != nil {
		t.Fatal(err)
	}

	// Encode the sync message the same way pushToPeer does.
	encJSON, _ := json.Marshal(encrypted)
	msg := p2pSyncMsg{
		Type: "snapshot",
		NSID: "test-ns",
		Data: encJSON,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	// Decode the same way handleSnapshot does.
	var decoded p2pSyncMsg
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal sync msg: %v", err)
	}
	if decoded.Type != "snapshot" || decoded.NSID != "test-ns" {
		t.Fatalf("wrong type/nsid: %s/%s", decoded.Type, decoded.NSID)
	}

	var decryptedBytes []byte
	if err := json.Unmarshal(decoded.Data, &decryptedBytes); err != nil {
		t.Fatalf("unmarshal encrypted data: %v", err)
	}

	plain, err := decrypt(decryptedBytes, nsKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	remote, err := UnmarshalSnapshot(plain)
	if err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	vi, ok := remote.Lookup("hello")
	if !ok || string(vi.Value) != "world" {
		t.Errorf("roundtrip failed: got %q, %v; want world, true", vi.Value, ok)
	}
}

// Regression: verify the full push→receive→merge pipeline works end-to-end
// without a real libp2p connection. Simulates what pushToPeer sends and
// what handleSnapshot receives.
func TestP2PSyncRoundtrip(t *testing.T) {
	t.Parallel()

	nsKey, _ := skykey.GenerateSymmetricKey()
	nsID := deriveNSID(nsKey, "roundtrip-ns")

	// Device A: source.
	dirA := t.TempDir()
	logA := NewLocalLog(filepath.Join(dirA, "ops.jsonl"), "devA")
	logA.AppendLocal(Entry{Type: Set, Key: "from-a", Value: []byte("value-a")})

	snapA, _ := logA.Snapshot()
	dataA, _ := MarshalSnapshot(snapA)
	encA, _ := encrypt(dataA, nsKey)
	encJSON, _ := json.Marshal(encA)

	// Simulate the wire message.
	msg := p2pSyncMsg{Type: "snapshot", NSID: nsID, Data: encJSON}

	// Device B: receiver.
	dirB := t.TempDir()
	logB := NewLocalLog(filepath.Join(dirB, "ops.jsonl"), "devB")
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))

	// Decode message (same as handleSnapshot).
	var encrypted []byte
	if err := json.Unmarshal(msg.Data, &encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := decrypt(encrypted, nsKey)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := UnmarshalSnapshot(plain)
	if err != nil {
		t.Fatal(err)
	}

	merged := diffAndMerge(logB, remote, nil, nil)
	if merged != 1 {
		t.Fatalf("merged = %d, want 1", merged)
	}
	baselinesB.Save("devA", remote)

	// Device B should now have the key.
	vi, ok := logB.Lookup("from-a")
	if !ok || string(vi.Value) != "value-a" {
		t.Errorf("from-a = %q, %v; want value-a, true", vi.Value, ok)
	}
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
