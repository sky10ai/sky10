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
		e := Entry{Type: Put, Path: "file" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
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
		e := Entry{Type: Put, Path: "temp" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
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
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
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
		e := Entry{Type: Put, Path: "f.md", Checksum: "v" + string(rune('0'+round)), Chunks: []string{"c1"}}
		log.Append(ctx, &e)
		log.Compact(ctx, 2) // keep max 2
	}

	// Should have at most 2 snapshots
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) > 2 {
		t.Errorf("expected <= 2 snapshots, got %d", len(snapKeys))
	}
}

func TestCompactThenNewOpsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Write 5 ops, compact
	for i := 0; i < 5; i++ {
		e := Entry{Type: Put, Path: "old" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
		log.Append(ctx, &e)
	}
	if _, err := log.Compact(ctx, 2); err != nil {
		t.Fatal(err)
	}

	// Write 3 more ops (post-compact)
	for i := 0; i < 3; i++ {
		e := Entry{Type: Put, Path: "new" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
		log.Append(ctx, &e)
	}

	// Force re-read from backend (simulates fresh OpsLog)
	log.InvalidateCache()
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Len() != 8 {
		t.Errorf("Len() = %d, want 8 (5 from snapshot + 3 new ops)", snap.Len())
	}

	// Old files from compacted snapshot
	for i := 0; i < 5; i++ {
		if _, ok := snap.Lookup("old" + string(rune('a'+i)) + ".md"); !ok {
			t.Errorf("old%c.md missing (should be in compacted snapshot)", rune('a'+i))
		}
	}
	// New files from ops
	for i := 0; i < 3; i++ {
		if _, ok := snap.Lookup("new" + string(rune('a'+i)) + ".md"); !ok {
			t.Errorf("new%c.md missing (should be in new ops)", rune('a'+i))
		}
	}
}

// Regression: S3 compaction must strip chunkless Put entries from snapshots.
// Chunkless Puts are local tracking state ("upload pending") that leaked to
// S3. If compaction preserves them, other machines see permanently-broken
// entries that can never be downloaded, causing infinite reconciler retries.
func TestCompactStripsChunklessPuts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Normal put with chunks — should survive compaction
	e1 := Entry{Type: Put, Path: "complete.txt", Checksum: "h1", Chunks: []string{"c1", "c2"}, Size: 100}
	log.Append(ctx, &e1)

	// Chunkless put — should be stripped by compaction
	e2 := Entry{Type: Put, Path: "pending.txt", Checksum: "h2", Size: 50}
	log.Append(ctx, &e2)

	// Another chunkless put
	e3 := Entry{Type: Put, Path: "also-pending.txt", Checksum: "h3", Size: 75}
	log.Append(ctx, &e3)

	// Delete op (always chunkless by nature) — should survive
	e4 := Entry{Type: Delete, Path: "gone.txt"}
	log.Append(ctx, &e4)

	// Symlink (chunkless by nature) — should survive
	e5 := Entry{Type: Symlink, Path: "link.txt", LinkTarget: "complete.txt"}
	log.Append(ctx, &e5)

	// Pre-compact: snapshot has 3 files + 1 symlink
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 4 {
		t.Fatalf("pre-compact Len() = %d, want 4", snap.Len())
	}

	_, err = log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Force reload from compacted snapshot
	log.InvalidateCache()
	snap, err = log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after compact: %v", err)
	}

	// Only complete.txt and link.txt should survive — chunkless Puts stripped
	if _, ok := snap.Lookup("complete.txt"); !ok {
		t.Error("complete.txt should survive compaction (has chunks)")
	}
	if _, ok := snap.Lookup("link.txt"); !ok {
		t.Error("link.txt should survive compaction (symlink)")
	}
	if _, ok := snap.Lookup("pending.txt"); ok {
		t.Error("pending.txt should be stripped by compaction (chunkless put)")
	}
	if _, ok := snap.Lookup("also-pending.txt"); ok {
		t.Error("also-pending.txt should be stripped by compaction (chunkless put)")
	}
}

// Regression: when a path has both a chunkless Put and a later Put with
// chunks, only the chunked version should survive compaction.
func TestCompactKeepsChunkedOverChunkless(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// First: chunkless put (watcher handler)
	e1 := Entry{Type: Put, Path: "file.txt", Checksum: "h1", Size: 50, Device: "dev-a"}
	log.Append(ctx, &e1)

	// Second: same path with chunks (outbox worker confirmed upload)
	e2 := Entry{Type: Put, Path: "file.txt", Checksum: "h1", Size: 50, Chunks: []string{"c1"}, Device: "dev-a"}
	log.Append(ctx, &e2)

	_, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	log.InvalidateCache()
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	fi, ok := snap.Lookup("file.txt")
	if !ok {
		t.Fatal("file.txt should survive compaction (later entry has chunks)")
	}
	if len(fi.Chunks) == 0 {
		t.Error("file.txt should have chunks after compaction (chunked entry wins)")
	}
}

func TestCompactDeletePostCompactSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Write 5 ops, compact
	for i := 0; i < 5; i++ {
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h", Chunks: []string{"c1"}}
		log.Append(ctx, &e)
	}
	if _, err := log.Compact(ctx, 2); err != nil {
		t.Fatal(err)
	}

	// Delete 2 files that are in the snapshot
	for i := 0; i < 2; i++ {
		e := Entry{Type: Delete, Path: "f" + string(rune('a'+i)) + ".md"}
		log.Append(ctx, &e)
	}

	// Force re-read — deletes should remove files from snapshot base
	log.InvalidateCache()
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (5 from snapshot - 2 deleted)", snap.Len())
	}

	// Deleted files gone
	for i := 0; i < 2; i++ {
		path := "f" + string(rune('a'+i)) + ".md"
		if _, ok := snap.Lookup(path); ok {
			t.Errorf("%s should be deleted", path)
		}
	}
	// Surviving files present
	for i := 2; i < 5; i++ {
		path := "f" + string(rune('a'+i)) + ".md"
		if _, ok := snap.Lookup(path); !ok {
			t.Errorf("%s missing (should survive)", path)
		}
	}
}
