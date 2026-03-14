package skyfs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/skyadapter/s3"
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
	id, _ := GenerateIdentity()
	encKey, _ := deriveManifestKey(id)

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
	id, _ := GenerateIdentity()

	// Two stores simulating two devices
	storeA := NewWithDevice(backend, id, "device-a")
	storeB := NewWithDevice(backend, id, "device-b")

	// Device A writes file1
	if err := storeA.Put(ctx, "file1.md", strings.NewReader("from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Device B writes file2
	if err := storeB.Put(ctx, "file2.md", strings.NewReader("from B")); err != nil {
		t.Fatalf("B Put: %v", err)
	}

	// Both devices should see both files
	for _, store := range []*Store{storeA, storeB} {
		entries, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("expected 2 files, got %d", len(entries))
		}
	}

	// Both files should be readable
	var buf bytes.Buffer
	if err := storeA.Get(ctx, "file2.md", &buf); err != nil {
		t.Fatalf("A reading B's file: %v", err)
	}
	if buf.String() != "from B" {
		t.Errorf("got %q, want %q", buf.String(), "from B")
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
