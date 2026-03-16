package fs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestOpKeyFormat(t *testing.T) {
	t.Parallel()

	op := Op{Timestamp: 1707900000, Device: "abc123", Seq: 1}
	got := op.OpKey()
	want := "ops/1707900000-abc123-0001.enc"
	if got != want {
		t.Errorf("OpKey() = %q, want %q", got, want)
	}
}

func TestWriteReadOps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	ops := []Op{
		{Type: OpPut, Path: "file1.md", Chunks: []string{"aaa"}, Size: 100, Checksum: "c1", Device: "dev-a", Timestamp: 1000, Seq: 1},
		{Type: OpPut, Path: "file2.md", Chunks: []string{"bbb"}, Size: 200, Checksum: "c2", Device: "dev-a", Timestamp: 1001, Seq: 2},
		{Type: OpDelete, Path: "file1.md", Device: "dev-a", Timestamp: 1002, Seq: 3},
	}

	for i := range ops {
		if err := WriteOp(ctx, backend, &ops[i], encKey); err != nil {
			t.Fatalf("WriteOp %d: %v", i, err)
		}
	}

	// Read all ops
	readOps, err := ReadAllOps(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("ReadAllOps: %v", err)
	}
	if len(readOps) != 3 {
		t.Fatalf("got %d ops, want 3", len(readOps))
	}

	// Read ops since timestamp 1000 (should exclude first op)
	filtered, err := ReadOps(ctx, backend, 1000, encKey)
	if err != nil {
		t.Fatalf("ReadOps: %v", err)
	}
	if len(filtered) != 2 {
		t.Errorf("filtered ops: got %d, want 2", len(filtered))
	}
}

func TestBuildState(t *testing.T) {
	t.Parallel()

	ops := []Op{
		{Type: OpPut, Path: "a.md", Chunks: []string{"c1"}, Size: 10, Checksum: "h1", Namespace: "default", Timestamp: 1},
		{Type: OpPut, Path: "b.md", Chunks: []string{"c2"}, Size: 20, Checksum: "h2", Namespace: "default", Timestamp: 2},
		{Type: OpDelete, Path: "a.md", Timestamp: 3},
	}

	state := BuildState(nil, ops)

	if _, ok := state.Tree["a.md"]; ok {
		t.Error("a.md should be deleted")
	}
	entry, ok := state.Tree["b.md"]
	if !ok {
		t.Fatal("b.md not found")
	}
	if entry.Size != 20 {
		t.Errorf("b.md size = %d, want 20", entry.Size)
	}
}

func TestBuildStateWithSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := NewManifest()
	snapshot.Set("existing.md", FileEntry{Chunks: []string{"c0"}, Size: 50, Checksum: "h0", Namespace: "default"})

	ops := []Op{
		{Type: OpPut, Path: "new.md", Chunks: []string{"c1"}, Size: 10, Checksum: "h1", Namespace: "default", Timestamp: 1},
	}

	state := BuildState(snapshot, ops)

	if _, ok := state.Tree["existing.md"]; !ok {
		t.Error("existing.md from snapshot should be preserved")
	}
	if _, ok := state.Tree["new.md"]; !ok {
		t.Error("new.md from ops should be added")
	}
}

func TestBuildStateDeterministic(t *testing.T) {
	t.Parallel()

	ops := []Op{
		{Type: OpPut, Path: "f.md", Chunks: []string{"v1"}, Checksum: "h1", Device: "dev-a", Timestamp: 1, Seq: 1},
		{Type: OpPut, Path: "f.md", Chunks: []string{"v2"}, Checksum: "h2", Device: "dev-b", Timestamp: 2, Seq: 1},
	}

	// Build state with ops in original order
	state1 := BuildState(nil, ops)

	// Build state with ops reversed (should sort internally)
	reversed := []Op{ops[1], ops[0]}
	sortOps(reversed)
	state2 := BuildState(nil, reversed)

	if state1.Tree["f.md"].Checksum != state2.Tree["f.md"].Checksum {
		t.Error("deterministic replay should produce same state regardless of input order")
	}
	// LWW: dev-b's write at timestamp 2 should win
	if state1.Tree["f.md"].Checksum != "h2" {
		t.Errorf("LWW should pick timestamp 2's value, got checksum %q", state1.Tree["f.md"].Checksum)
	}
}

func TestDetectConflicts(t *testing.T) {
	t.Parallel()

	ops := []Op{
		{Type: OpPut, Path: "f.md", PrevChecksum: "base", Checksum: "v1", Device: "dev-a", Timestamp: 1},
		{Type: OpPut, Path: "f.md", PrevChecksum: "base", Checksum: "v2", Device: "dev-b", Timestamp: 2},
	}

	conflicts := DetectConflicts(ops)
	if len(conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(conflicts))
	}
	if conflicts[0].Path != "f.md" {
		t.Errorf("conflict path = %q, want %q", conflicts[0].Path, "f.md")
	}
}

func TestDetectConflictsNoConflict(t *testing.T) {
	t.Parallel()

	// Sequential edits (different prev_checksum) are not conflicts
	ops := []Op{
		{Type: OpPut, Path: "f.md", PrevChecksum: "base", Checksum: "v1", Device: "dev-a", Timestamp: 1},
		{Type: OpPut, Path: "f.md", PrevChecksum: "v1", Checksum: "v2", Device: "dev-b", Timestamp: 2},
	}

	conflicts := DetectConflicts(ops)
	if len(conflicts) != 0 {
		t.Errorf("got %d conflicts, want 0 (sequential edits)", len(conflicts))
	}
}

func TestStoreOpsLogIntegration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	// Put via ops log
	if err := store.Put(ctx, "test.md", strings.NewReader("hello ops")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Should have ops in S3
	opsKeys, _ := backend.List(ctx, "ops/")
	if len(opsKeys) == 0 {
		t.Error("expected ops in S3")
	}

	// Get should work by replaying ops
	var buf bytes.Buffer
	if err := store.Get(ctx, "test.md", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.String() != "hello ops" {
		t.Errorf("got %q, want %q", buf.String(), "hello ops")
	}

	// List should show the file
	entries, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List: got %d entries, want 1", len(entries))
	}

	// Remove via ops log
	if err := store.Remove(ctx, "test.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Should have 2 ops now (put + delete)
	opsKeys, _ = backend.List(ctx, "ops/")
	if len(opsKeys) != 2 {
		t.Errorf("expected 2 ops, got %d", len(opsKeys))
	}

	// File should be gone
	err = store.Get(ctx, "test.md", &buf)
	if err != ErrFileNotFound {
		t.Errorf("after Remove: got %v, want ErrFileNotFound", err)
	}
}

func TestStoreMultiDeviceOps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	// Two DIFFERENT identities — simulating two real devices
	idA, _ := GenerateIdentity()
	idB, _ := GenerateIdentity()

	// Device A initializes the store and creates the namespace key
	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "file1.md", strings.NewReader("from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Simulate invite flow: wrap the default namespace key for Device B
	nsKey, err := storeA.opsKey(ctx)
	if err != nil {
		t.Fatalf("getting ops key: %v", err)
	}
	wrappedForB, err := WrapNamespaceKey(nsKey, idB.PublicKey)
	if err != nil {
		t.Fatalf("wrapping for B: %v", err)
	}
	deviceBID := shortPubkeyID(idB.Address())
	bKeyPath := "keys/namespaces/default." + deviceBID + ".ns.enc"
	r := bytes.NewReader(wrappedForB)
	if err := backend.Put(ctx, bKeyPath, r, int64(len(wrappedForB))); err != nil {
		t.Fatalf("storing B's wrapped key: %v", err)
	}

	// Device B writes with its OWN identity
	storeB := NewWithDevice(backend, idB, "device-b")
	if err := storeB.Put(ctx, "file2.md", strings.NewReader("from B")); err != nil {
		t.Fatalf("B Put: %v", err)
	}

	// Device A should see both files (including B's)
	entriesA, err := storeA.List(ctx, "")
	if err != nil {
		t.Fatalf("A List: %v", err)
	}
	if len(entriesA) != 2 {
		t.Errorf("A: expected 2 files, got %d", len(entriesA))
	}

	// Device B should see both files (including A's)
	entriesB, err := storeB.List(ctx, "")
	if err != nil {
		t.Fatalf("B List: %v", err)
	}
	if len(entriesB) != 2 {
		t.Errorf("B: expected 2 files, got %d", len(entriesB))
	}

	// Cross-device reads
	var buf bytes.Buffer
	if err := storeA.Get(ctx, "file2.md", &buf); err != nil {
		t.Fatalf("A reading B's file: %v", err)
	}
	if buf.String() != "from B" {
		t.Errorf("got %q, want %q", buf.String(), "from B")
	}

	buf.Reset()
	if err := storeB.Get(ctx, "file1.md", &buf); err != nil {
		t.Fatalf("B reading A's file: %v", err)
	}
	if buf.String() != "from A" {
		t.Errorf("got %q, want %q", buf.String(), "from A")
	}
}

// Regression: device without access must NOT overwrite the namespace key.
// Before the fix, an unauthorized device would call getOrCreateNamespaceKey,
// fail to unwrap the existing key, then create a NEW key at the same path —
// destroying the original and making all data unreadable by any device.
func TestUnauthorizedDeviceCannotOverwriteKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateIdentity()
	idB, _ := GenerateIdentity()

	// Device A creates the store and writes data
	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "secret.md", strings.NewReader("important data")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Verify A can read its data
	var buf bytes.Buffer
	if err := storeA.Get(ctx, "secret.md", &buf); err != nil {
		t.Fatalf("A Get before: %v", err)
	}
	if buf.String() != "important data" {
		t.Fatalf("A got %q", buf.String())
	}

	// Device B connects WITHOUT being granted access (no invite flow)
	storeB := NewWithDevice(backend, idB, "device-b")

	// B trying to write should fail with access denied, NOT overwrite the key
	err := storeB.Put(ctx, "evil.md", strings.NewReader("overwrite attempt"))
	if err == nil {
		t.Fatal("expected error from unauthorized device, got nil")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected 'access denied' error, got: %v", err)
	}

	// Device A's data must still be readable after B's failed attempt
	buf.Reset()
	storeA2 := NewWithDevice(backend, idA, "device-a")
	if err := storeA2.Get(ctx, "secret.md", &buf); err != nil {
		t.Fatalf("A Get after B's attempt: %v", err)
	}
	if buf.String() != "important data" {
		t.Fatalf("A's data corrupted: got %q", buf.String())
	}

	// The namespace key in S3 should still be unwrappable by A
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	found := false
	for _, k := range nsKeys {
		if k == "keys/namespaces/default.ns.enc" {
			found = true
		}
	}
	if !found {
		t.Error("default.ns.enc missing from S3")
	}
}

// Regression: the full invite flow must work end-to-end.
// Device A creates store, generates invite, Device B joins,
// auto-approve wraps keys, then both devices read/write.
func TestFullInviteFlowEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateIdentity()
	idB, _ := GenerateIdentity()

	// Step 1: Device A initializes and writes a file
	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "from-a.md", strings.NewReader("hello from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Step 2: Device B tries WITHOUT invite — must fail
	storeB := NewWithDevice(backend, idB, "device-b")
	err := storeB.Put(ctx, "from-b.md", strings.NewReader("hello from B"))
	if err == nil {
		t.Fatal("B should not be able to write without invite")
	}

	// Step 3: Simulate approve — wrap all namespace keys for B
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	for _, nsKeyPath := range nsKeys {
		rc, err := backend.Get(ctx, nsKeyPath)
		if err != nil {
			continue
		}
		wrapped, _ := io.ReadAll(rc)
		rc.Close()

		nsKey, err := UnwrapNamespaceKey(wrapped, idA.PrivateKey)
		if err != nil {
			continue
		}
		wrappedForB, err := WrapNamespaceKey(nsKey, idB.PublicKey)
		if err != nil {
			t.Fatalf("wrapping for B: %v", err)
		}

		bID := shortPubkeyID(idB.Address())
		nsName := strings.TrimPrefix(nsKeyPath, "keys/namespaces/")
		nsName = strings.TrimSuffix(nsName, ".ns.enc")
		bKeyPath := "keys/namespaces/" + nsName + "." + bID + ".ns.enc"
		r := bytes.NewReader(wrappedForB)
		backend.Put(ctx, bKeyPath, r, int64(len(wrappedForB)))
	}

	// Step 4: Device B should now be able to write (fresh store to clear cache)
	storeB2 := NewWithDevice(backend, idB, "device-b")
	if err := storeB2.Put(ctx, "from-b.md", strings.NewReader("hello from B")); err != nil {
		t.Fatalf("B Put after invite: %v", err)
	}

	// Step 5: Both devices see both files
	entriesA, err := storeA.List(ctx, "")
	if err != nil {
		t.Fatalf("A List: %v", err)
	}
	if len(entriesA) != 2 {
		t.Errorf("A sees %d files, want 2", len(entriesA))
	}

	entriesB, err := storeB2.List(ctx, "")
	if err != nil {
		t.Fatalf("B List: %v", err)
	}
	if len(entriesB) != 2 {
		t.Errorf("B sees %d files, want 2", len(entriesB))
	}

	// Step 6: Cross-reads work
	var buf bytes.Buffer
	if err := storeB2.Get(ctx, "from-a.md", &buf); err != nil {
		t.Fatalf("B reading A's file: %v", err)
	}
	if buf.String() != "hello from A" {
		t.Errorf("B got %q, want %q", buf.String(), "hello from A")
	}

	buf.Reset()
	if err := storeA.Get(ctx, "from-b.md", &buf); err != nil {
		t.Fatalf("A reading B's file: %v", err)
	}
	if buf.String() != "hello from B" {
		t.Errorf("A got %q, want %q", buf.String(), "hello from B")
	}
}

func TestStoreSnapshotAndReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	// Write some files
	if err := store.Put(ctx, "a.md", strings.NewReader("aaa")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := store.Put(ctx, "b.md", strings.NewReader("bbb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	// Save snapshot
	if err := store.SaveSnapshot(ctx); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Verify snapshot includes both files
	store2 := New(backend, id)
	entries, err := store2.List(ctx, "")
	if err != nil {
		t.Fatalf("List after snapshot: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("snapshot should contain 2 files, got %d", len(entries))
	}

	// Verify all files readable from snapshot
	var buf bytes.Buffer
	if err := store2.Get(ctx, "a.md", &buf); err != nil {
		t.Fatalf("Get a.md: %v", err)
	}
	if buf.String() != "aaa" {
		t.Errorf("a.md = %q, want %q", buf.String(), "aaa")
	}
}

func TestOpsEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	secret := "this-is-secret-data"
	if err := store.Put(ctx, "secret.md", strings.NewReader(secret)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	opsKeys, _ := backend.List(ctx, "ops/")
	for _, key := range opsKeys {
		rc, _ := backend.Get(ctx, key)
		raw, _ := io.ReadAll(rc)
		rc.Close()

		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("op %s contains plaintext", key)
		}
		if bytes.Contains(raw, []byte("secret.md")) {
			t.Errorf("op %s contains plaintext path", key)
		}
	}
}
