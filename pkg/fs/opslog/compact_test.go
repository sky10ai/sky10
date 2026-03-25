package opslog

import (
	"context"
	"testing"
)

func TestCompactParallelDeleteMany(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, backend := newTestLog(t)

	// Write enough ops to exercise parallel delete (>10 to exceed semaphore)
	for i := 0; i < 25; i++ {
		e := Entry{Type: Put, Path: "file" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 25 {
		t.Fatalf("expected 25 ops before compact, got %d", len(opKeys))
	}

	result, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsCompacted != 25 {
		t.Errorf("OpsCompacted = %d, want 25", result.OpsCompacted)
	}
	if result.OpsDeleted != 25 {
		t.Errorf("OpsDeleted = %d, want 25", result.OpsDeleted)
	}

	// All ops gone
	opKeys, _ = backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Snapshot intact
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after compact: %v", err)
	}
	if snap.Len() != 25 {
		t.Errorf("Len() = %d after compact, want 25", snap.Len())
	}
}

func TestCompactWithDeletedFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, backend := newTestLog(t)

	// Create then delete files — compact should produce a clean snapshot
	for i := 0; i < 10; i++ {
		e := Entry{Type: Put, Path: "temp" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		e := Entry{Type: Delete, Path: "temp" + string(rune('a'+i)) + ".md"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// 15 ops total (10 puts + 5 deletes)
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 15 {
		t.Fatalf("expected 15 ops before compact, got %d", len(opKeys))
	}

	result, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsCompacted != 15 {
		t.Errorf("OpsCompacted = %d, want 15", result.OpsCompacted)
	}
	if result.OpsDeleted != 15 {
		t.Errorf("OpsDeleted = %d, want 15", result.OpsDeleted)
	}

	// All ops gone
	opKeys, _ = backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Snapshot should have only 5 surviving files
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after compact: %v", err)
	}
	if snap.Len() != 5 {
		t.Errorf("Len() = %d after compact, want 5", snap.Len())
	}
}

func TestCompactWithSymlinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, backend := newTestLog(t)

	// Mix of puts and symlinks
	e1 := Entry{Type: Put, Path: "real.txt", Checksum: "h1", Chunks: []string{"c1"}}
	log.Append(ctx, &e1)
	e2 := Entry{Type: Symlink, Path: "link.txt", Checksum: "h2", LinkTarget: "real.txt"}
	log.Append(ctx, &e2)

	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 2 {
		t.Fatalf("expected 2 ops before compact, got %d", len(opKeys))
	}

	result, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsDeleted != 2 {
		t.Errorf("OpsDeleted = %d, want 2", result.OpsDeleted)
	}

	opKeys, _ = backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Symlink preserved in snapshot
	log.InvalidateCache()
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after compact: %v", err)
	}
	fi, ok := snap.Lookup("link.txt")
	if !ok {
		t.Fatal("link.txt missing after compact")
	}
	if fi.LinkTarget != "real.txt" {
		t.Errorf("LinkTarget = %q, want %q", fi.LinkTarget, "real.txt")
	}
}

func TestCompactNoOps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Compact with no ops — should not error
	result, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.OpsCompacted != 0 {
		t.Errorf("OpsCompacted = %d, want 0", result.OpsCompacted)
	}
	if result.OpsDeleted != 0 {
		t.Errorf("OpsDeleted = %d, want 0", result.OpsDeleted)
	}
}

func TestCompactIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	for i := 0; i < 5; i++ {
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h"}
		log.Append(ctx, &e)
	}

	// Compact twice — second should be a no-op
	r1, err := log.Compact(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r1.OpsDeleted != 5 {
		t.Errorf("first compact: OpsDeleted = %d, want 5", r1.OpsDeleted)
	}

	r2, err := log.Compact(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r2.OpsDeleted != 0 {
		t.Errorf("second compact: OpsDeleted = %d, want 0 (no ops left)", r2.OpsDeleted)
	}

	// Snapshot still valid
	snap, _ := log.Snapshot(ctx)
	if snap.Len() != 5 {
		t.Errorf("Len() = %d, want 5", snap.Len())
	}
}

func TestCompactSnapshotPruning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, backend := newTestLog(t)

	// Compact 3 times to create 3 snapshots
	for round := 0; round < 3; round++ {
		e := Entry{Type: Put, Path: "f.md", Checksum: "v" + string(rune('0'+round))}
		log.Append(ctx, &e)
		log.Compact(ctx, 2) // keep max 2
	}

	// Should have at most 2 snapshots
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) > 2 {
		t.Errorf("expected <= 2 snapshots, got %d", len(snapKeys))
	}
}
