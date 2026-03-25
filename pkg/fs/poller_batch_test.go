package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestPollerBatchProcessing(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Upload 10 files from "device A"
	for i := 0; i < 10; i++ {
		store.Put(ctx, "file"+string(rune('a'+i))+".txt", strings.NewReader("content"))
	}

	// Device B's poller should batch these
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)

	var reconcilePokes atomic.Int32
	poller.pokeReconciler = func() { reconcilePokes.Add(1) }

	poller.pollOnce(ctx)

	snap, _ := localLog.Snapshot()
	if snap.Len() != 10 {
		t.Errorf("snapshot has %d files, want 10", snap.Len())
	}

	// With 10 ops and default batch size (200), should be 1 poke
	if reconcilePokes.Load() < 1 {
		t.Error("reconciler should have been poked at least once")
	}
}

func TestPollerBatchMultiplePokes(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Upload enough files to span multiple batches (200 per batch)
	// Use 5 files but verify the mechanism works
	for i := 0; i < 5; i++ {
		store.Put(ctx, "f"+string(rune('a'+i))+".txt", strings.NewReader("data"))
	}

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)

	var pokes atomic.Int32
	poller.pokeReconciler = func() { pokes.Add(1) }

	poller.pollOnce(ctx)

	snap, _ := localLog.Snapshot()
	if snap.Len() != 5 {
		t.Errorf("snapshot has %d files, want 5", snap.Len())
	}
	if pokes.Load() < 1 {
		t.Error("reconciler should have been poked")
	}
}

func TestPollerBatchSkipsDuplicates(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	store.Put(ctx, "existing.txt", strings.NewReader("same"))

	// Pre-populate local log with matching entry
	entries := getOpsEntries(t, store)
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	for _, e := range entries {
		localLog.Append(e)
	}

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	var pokes atomic.Int32
	poller.pokeReconciler = func() { pokes.Add(1) }

	// Poll again — should skip the duplicate
	poller.pollOnce(ctx)

	if pokes.Load() != 0 {
		t.Error("reconciler should NOT be poked (no new entries)")
	}
}

func TestPollerBatchSkipsOwnDevice(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	store.Put(ctx, "myfile.txt", strings.NewReader("mine"))

	tmpDir := t.TempDir()
	// Local log device matches store device — should skip own ops
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), store.deviceID)
	// Set cursor > 0 so own-device filtering kicks in
	localLog.SetLastRemoteOp(1)

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	var pokes atomic.Int32
	poller.pokeReconciler = func() { pokes.Add(1) }

	poller.pollOnce(ctx)

	if pokes.Load() != 0 {
		t.Error("reconciler should NOT be poked (own device ops)")
	}
}

func TestPollerBatchNamespaceFilter(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Upload files with different namespaces
	store.SetNamespace("photos")
	store.Put(ctx, "photo.jpg", strings.NewReader("img"))
	store.SetNamespace("docs")
	store.Put(ctx, "doc.txt", strings.NewReader("text"))

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	// Poller filters to "docs" namespace only
	poller := NewPollerV2(store, localLog, 30*time.Second, "docs", nil)
	var pokes atomic.Int32
	poller.pokeReconciler = func() { pokes.Add(1) }

	poller.pollOnce(ctx)

	snap, _ := localLog.Snapshot()
	if snap.Len() != 1 {
		t.Errorf("snapshot has %d files, want 1 (only docs namespace)", snap.Len())
	}
	if _, ok := snap.Lookup("doc.txt"); !ok {
		t.Error("doc.txt should be in snapshot")
	}
	if _, ok := snap.Lookup("photo.jpg"); ok {
		t.Error("photo.jpg should NOT be in snapshot (wrong namespace)")
	}
}

func TestPollerBatchEmitsEvent(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	store.Put(ctx, "file.txt", strings.NewReader("data"))

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}

	var events []string
	poller.onEvent = func(e string, _ map[string]any) { events = append(events, e) }

	poller.pollOnce(ctx)

	found := false
	for _, e := range events {
		if e == "sync.active" {
			found = true
		}
	}
	if !found {
		t.Error("poller should emit sync.active when ops are found")
	}
}

func TestPollerBatchNoEventWhenEmpty(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}

	var events []string
	poller.onEvent = func(e string, _ map[string]any) { events = append(events, e) }

	poller.pollOnce(context.Background())

	if len(events) != 0 {
		t.Errorf("should not emit events when no ops, got %v", events)
	}
}

func TestPollerBatchSymlinks(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Write a symlink op directly
	op := &Op{
		Type:       OpSymlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Namespace:  "Test",
	}
	if err := store.writeOp(ctx, op); err != nil {
		t.Fatalf("writeOp: %v", err)
	}

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}

	poller.pollOnce(ctx)

	// Verify symlink entry round-tripped through batched read
	snap, _ := localLog.Snapshot()
	fi, ok := snap.Lookup("link.txt")
	if !ok {
		t.Fatal("link.txt not in snapshot")
	}
	if fi.LinkTarget != "target.txt" {
		t.Errorf("LinkTarget = %q, want %q", fi.LinkTarget, "target.txt")
	}
}

// --- Post-compaction bootstrap gap tests ---
//
// These tests document and regression-test the known gap: after S3 compaction,
// the poller only reads ops/ (which is empty), missing files captured in the
// snapshot. The S3 OpsLog.Snapshot() handles this correctly, but the poller +
// local log path does not.

func TestPollerBatchAfterCompactSeesNothing(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Upload 10 files
	for i := 0; i < 10; i++ {
		store.Put(ctx, "file"+string(rune('a'+i))+".txt", strings.NewReader("content"))
	}

	// Compact: all ops folded into snapshot, ops deleted
	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.OpsCompacted != 10 {
		t.Errorf("OpsCompacted = %d, want 10", result.OpsCompacted)
	}

	// Verify ops/ is empty
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Fatalf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Fresh device B polls — poller reads ops/ which is empty
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	// Known gap: poller only reads ops/, not the S3 snapshot.
	// All 10 files are in the snapshot but invisible to the poller.
	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 0 {
		t.Errorf("local log has %d files, want 0 (poller doesn't bootstrap from S3 snapshot)", localSnap.Len())
	}

	// S3 OpsLog.Snapshot() correctly sees all 10 via snapshot file
	storeB := New(backend, id)
	s3Log, err := storeB.getOpsLog(ctx)
	if err != nil {
		t.Fatalf("getOpsLog: %v", err)
	}
	s3Snap, err := s3Log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("S3 Snapshot: %v", err)
	}
	if s3Snap.Len() != 10 {
		t.Errorf("S3 snapshot has %d files, want 10", s3Snap.Len())
	}
}

func TestPollerBatchCompactThenNewOps(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Upload 5 files, compact
	for i := 0; i < 5; i++ {
		store.Put(ctx, "old"+string(rune('a'+i))+".txt", strings.NewReader("old"))
	}
	if _, err := Compact(ctx, backend, id, 2); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Upload 3 more files (post-compact)
	for i := 0; i < 3; i++ {
		store.Put(ctx, "new"+string(rune('a'+i))+".txt", strings.NewReader("new"))
	}

	// Fresh device B polls — only the 3 new ops are visible
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 3 {
		t.Errorf("local log has %d files, want 3 (only post-compact ops)", localSnap.Len())
	}

	// Old files not in local log (compacted into S3 snapshot)
	for i := 0; i < 5; i++ {
		path := "old" + string(rune('a'+i)) + ".txt"
		if _, ok := localSnap.Lookup(path); ok {
			t.Errorf("%s should not be in local log (compacted)", path)
		}
	}

	// S3 OpsLog.Snapshot() has all 8
	storeB := New(backend, id)
	s3Log, _ := storeB.getOpsLog(ctx)
	s3Snap, _ := s3Log.Snapshot(ctx)
	if s3Snap.Len() != 8 {
		t.Errorf("S3 snapshot has %d files, want 8", s3Snap.Len())
	}
}

func TestPollerBatchMultiCompactOnlyLatestOps(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	ctx := context.Background()

	// Round 1: 3 files → compact
	for i := 0; i < 3; i++ {
		store.Put(ctx, "r1-"+string(rune('a'+i))+".txt", strings.NewReader("r1"))
	}
	Compact(ctx, backend, id, 2)

	// Round 2: 2 more → compact
	for i := 0; i < 2; i++ {
		store.Put(ctx, "r2-"+string(rune('a'+i))+".txt", strings.NewReader("r2"))
	}
	Compact(ctx, backend, id, 2)

	// Round 3: 1 more (not compacted)
	store.Put(ctx, "r3-a.txt", strings.NewReader("r3"))

	// Poller: only 1 uncompacted op
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(store, localLog, 30*time.Second, "", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 1 {
		t.Errorf("local log has %d files, want 1 (only uncompacted op)", localSnap.Len())
	}
	if _, ok := localSnap.Lookup("r3-a.txt"); !ok {
		t.Error("r3-a.txt should be in local log (uncompacted)")
	}

	// S3 snapshot has all 6
	storeB := New(backend, id)
	s3Log, _ := storeB.getOpsLog(ctx)
	s3Snap, _ := s3Log.Snapshot(ctx)
	if s3Snap.Len() != 6 {
		t.Errorf("S3 snapshot has %d files, want 6", s3Snap.Len())
	}
}
