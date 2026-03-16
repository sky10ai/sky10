package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestCompact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Write several files
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := store.Put(ctx, name, strings.NewReader("content-"+name)); err != nil {
			t.Fatalf("Put %s: %v", name, err)
		}
	}

	// Verify ops exist
	opsKeys, _ := backend.List(ctx, "ops/")
	if len(opsKeys) == 0 {
		t.Fatal("expected ops before compaction")
	}

	// Compact
	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsCompacted != 3 {
		t.Errorf("OpsCompacted = %d, want 3", result.OpsCompacted)
	}
	if result.OpsDeleted != 3 {
		t.Errorf("OpsDeleted = %d, want 3", result.OpsDeleted)
	}

	// Ops should be gone
	opsKeys, _ = backend.List(ctx, "ops/")
	if len(opsKeys) != 0 {
		t.Errorf("expected 0 ops after compaction, got %d", len(opsKeys))
	}

	// Snapshot should exist
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) == 0 {
		t.Error("expected snapshot after compaction")
	}

	// State should be correct after compaction
	store2 := New(backend, id)
	entries, err := store2.List(ctx, "")
	if err != nil {
		t.Fatalf("List after compact: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 files after compact, got %d", len(entries))
	}

	// All files should be readable
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		var buf bytes.Buffer
		if err := store2.Get(ctx, name, &buf); err != nil {
			t.Errorf("Get %s after compact: %v", name, err)
		}
		if buf.String() != "content-"+name {
			t.Errorf("%s = %q, want %q", name, buf.String(), "content-"+name)
		}
	}
}

func TestCompactSnapshotRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Create multiple compaction cycles
	for i := 0; i < 5; i++ {
		if err := store.Put(ctx, "file.md", strings.NewReader("v"+string(rune('0'+i)))); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := Compact(ctx, backend, id, 2); err != nil {
			t.Fatalf("Compact %d: %v", i, err)
		}
	}

	// Should keep at most 2 snapshots
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) > 2 {
		t.Errorf("expected <= 2 snapshots, got %d", len(snapKeys))
	}
}

func TestCompactEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()

	// Compact with no ops should work (no-op)
	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact empty: %v", err)
	}
	if result.OpsCompacted != 0 {
		t.Errorf("OpsCompacted = %d, want 0", result.OpsCompacted)
	}
}

func TestCompactPreservesState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put, delete, put pattern
	if err := store.Put(ctx, "temp.md", strings.NewReader("temp")); err != nil {
		t.Fatalf("Put temp: %v", err)
	}
	if err := store.Remove(ctx, "temp.md"); err != nil {
		t.Fatalf("Remove temp: %v", err)
	}
	if err := store.Put(ctx, "keep.md", strings.NewReader("keep")); err != nil {
		t.Fatalf("Put keep: %v", err)
	}

	// State before compaction
	entries1, _ := store.List(ctx, "")

	// Compact
	if _, err := Compact(ctx, backend, id, 2); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// State after compaction should be identical
	store2 := New(backend, id)
	entries2, _ := store2.List(ctx, "")

	if len(entries1) != len(entries2) {
		t.Fatalf("entry count changed: %d → %d", len(entries1), len(entries2))
	}
	if len(entries2) != 1 || entries2[0].Path != "keep.md" {
		t.Errorf("unexpected entries after compact: %v", entries2)
	}
}
