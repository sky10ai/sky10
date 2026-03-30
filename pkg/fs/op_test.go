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

// TestStoreOpsLogIntegration and TestStoreSnapshotAndReplay deleted:
// S3 ops log is dead. Snapshot exchange replaced it.

func TestOpsEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
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
