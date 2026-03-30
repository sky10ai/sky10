package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestIntegrationCompactMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	store := NewWithDevice(backend, id, "dev-a")
	store.SetNamespace("shared")

	// Upload 20 files
	for i := 0; i < 20; i++ {
		store.Put(ctx, "file"+string(rune('a'+i))+".txt", strings.NewReader("content"))
	}

	// Verify ops exist
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 20 {
		t.Fatalf("expected 20 ops, got %d", len(opKeys))
	}

	// Compact
	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsCompacted != 20 {
		t.Errorf("OpsCompacted = %d, want 20", result.OpsCompacted)
	}
	if result.OpsDeleted != 20 {
		t.Errorf("OpsDeleted = %d, want 20", result.OpsDeleted)
	}

	// All ops deleted from S3
	opKeys, _ = backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Snapshot exists
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) == 0 {
		t.Error("no snapshot after compact")
	}

	// New store can read the snapshot and see all 20 files
	store2 := NewWithDevice(backend, id, "dev-b")
	store2.SetNamespace("shared")
	log2, _ := store2.getOpsLog(ctx)
	snap, err := log2.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Len() != 20 {
		t.Errorf("snapshot has %d files, want 20", snap.Len())
	}
}

func TestIntegrationCompactWithDeletesMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	store := NewWithDevice(backend, id, "dev-a")
	store.SetNamespace("shared")

	// Upload 10 files, delete 5
	for i := 0; i < 10; i++ {
		store.Put(ctx, "f"+string(rune('a'+i))+".txt", strings.NewReader("data"))
	}
	for i := 0; i < 5; i++ {
		store.Remove(ctx, "f"+string(rune('a'+i))+".txt")
	}

	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// All ops (10 puts + 5 deletes) should be deleted
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Snapshot should have only 5 surviving files
	store2 := NewWithDevice(backend, id, "dev-b")
	store2.SetNamespace("shared")
	log2, _ := store2.getOpsLog(ctx)
	snap, _ := log2.Snapshot(ctx)
	if snap.Len() != 5 {
		t.Errorf("snapshot has %d files, want 5", snap.Len())
	}

	_ = result
}

func TestIntegrationBatchedPollMinIO(t *testing.T) {
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	// Device A uploads 15 files
	idA, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "dev-a")
	storeA.SetNamespace("shared")

	for i := 0; i < 15; i++ {
		storeA.Put(ctx, "f"+string(rune('a'+i))+".txt", strings.NewReader("data"))
	}

	// Device B polls with batch size (default 200, so all in one batch for 15 files)
	storeB := NewWithDevice(backend, idA, "dev-b")
	storeB.SetNamespace("shared")

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)

	var pokeMu sync.Mutex
	pokes := 0
	poller.pokeReconciler = func() {
		pokeMu.Lock()
		pokes++
		pokeMu.Unlock()
	}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	// All 15 files should be in the local log
	snap, _ := localLog.Snapshot()
	if snap.Len() != 15 {
		t.Errorf("snapshot has %d files, want 15", snap.Len())
	}

	pokeMu.Lock()
	if pokes < 1 {
		t.Error("reconciler should have been poked at least once")
	}
	pokeMu.Unlock()
}

func TestIntegrationProgressEventsMinIO(t *testing.T) {
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	// Device A uploads files
	id, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, id, "dev-a")
	storeA.SetNamespace("shared")

	for i := 0; i < 12; i++ {
		storeA.Put(ctx, "f"+string(rune('a'+i))+".txt", strings.NewReader("content-"+string(rune('a'+i))))
	}

	// Device B: poll + reconcile with event tracking
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	var eventMu sync.Mutex
	events := make(map[string]int)

	onEvent := func(event string, _ map[string]any) {
		eventMu.Lock()
		events[event]++
		eventMu.Unlock()
	}

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)
	reconciler := NewReconciler(storeB, localLog, outbox, localDir, nil, nil)

	poller.onEvent = onEvent
	poller.pokeReconciler = func() {}
	reconciler.onEvent = onEvent

	// Poll
	poller.pollOnce(ctx)

	// Reconcile
	reconciler.reconcile(ctx)

	// Verify events fired
	eventMu.Lock()
	defer eventMu.Unlock()

	if events["sync.active"] == 0 {
		t.Error("expected sync.active events")
	}
	if events["poll.progress"] == 0 {
		t.Error("expected poll.progress events")
	}
	if events["download.start"] == 0 {
		t.Error("expected download.start event")
	}
	// download.progress fires every 10 files, with 12 files we get at least 1
	if events["download.progress"] == 0 {
		t.Error("expected download.progress events")
	}
	if events["sync.complete"] == 0 {
		t.Error("expected sync.complete event")
	}

	// Verify files actually downloaded
	downloaded := 0
	filepath.WalkDir(localDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			downloaded++
		}
		return nil
	})
	if downloaded != 12 {
		t.Errorf("downloaded %d files, want 12", downloaded)
	}
}

// --- Post-compaction bootstrap gap tests ---
//
// These MinIO integration tests document and regression-test the known gap:
// after S3 compaction, the poller only reads ops/ (empty after compact),
// so a fresh device misses files captured in the S3 snapshot. The S3
// OpsLog.Snapshot() handles this correctly, but the poller + local log
// path does not bootstrap from it.

func TestIntegrationPollerAfterCompactMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, id, "dev-a")
	storeA.SetNamespace("shared")

	// Upload 10 files and compact
	for i := 0; i < 10; i++ {
		storeA.Put(ctx, "file"+string(rune('a'+i))+".txt", strings.NewReader("content"))
	}

	result, err := Compact(ctx, backend, id, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.OpsCompacted != 10 {
		t.Errorf("OpsCompacted = %d, want 10", result.OpsCompacted)
	}

	// S3 OpsLog.Snapshot() correctly sees all 10 via snapshot
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")
	s3Log, _ := storeB.getOpsLog(ctx)
	s3Snap, err := s3Log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("S3 Snapshot: %v", err)
	}
	if s3Snap.Len() != 10 {
		t.Errorf("S3 snapshot has %d files, want 10", s3Snap.Len())
	}

	// Poller + local log: fresh device sees nothing (known gap)
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 0 {
		t.Errorf("poller local log has %d files, want 0 (poller doesn't read S3 snapshots)", localSnap.Len())
	}

	// Reconciler has nothing to work with — no files on disk
	reconciler := NewReconciler(storeB, localLog, outbox, localDir, nil, nil)
	reconciler.onEvent = func(string, map[string]any) {}
	reconciler.reconcile(ctx)

	downloaded := 0
	filepath.WalkDir(localDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			downloaded++
		}
		return nil
	})
	if downloaded != 0 {
		t.Errorf("downloaded %d files, want 0 (compacted files unreachable via poller)", downloaded)
	}
}

func TestIntegrationMultiCompactPollMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	store := NewWithDevice(backend, id, "dev-a")
	store.SetNamespace("shared")

	// Round 1: 5 files → compact
	for i := 0; i < 5; i++ {
		store.Put(ctx, "r1-"+string(rune('a'+i))+".txt", strings.NewReader("r1"))
	}
	Compact(ctx, backend, id, 2)

	// Round 2: 3 files → compact
	for i := 0; i < 3; i++ {
		store.Put(ctx, "r2-"+string(rune('a'+i))+".txt", strings.NewReader("r2"))
	}
	Compact(ctx, backend, id, 2)

	// Round 3: 2 more (not compacted)
	for i := 0; i < 2; i++ {
		store.Put(ctx, "r3-"+string(rune('a'+i))+".txt", strings.NewReader("r3"))
	}

	// S3 snapshot: all 10 files
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")
	s3Log, _ := storeB.getOpsLog(ctx)
	s3Snap, _ := s3Log.Snapshot(ctx)
	if s3Snap.Len() != 10 {
		t.Errorf("S3 snapshot has %d files, want 10", s3Snap.Len())
	}

	// Poller: only 2 uncompacted ops
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 2 {
		t.Errorf("poller local log has %d files, want 2 (only uncompacted ops)", localSnap.Len())
	}
}

func TestIntegrationCompactDeletesThenPollMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	store := NewWithDevice(backend, id, "dev-a")
	store.SetNamespace("shared")

	// Upload 10, delete 5, compact
	for i := 0; i < 10; i++ {
		store.Put(ctx, "f"+string(rune('a'+i))+".txt", strings.NewReader("data"))
	}
	for i := 0; i < 5; i++ {
		store.Remove(ctx, "f"+string(rune('a'+i))+".txt")
	}
	Compact(ctx, backend, id, 2)

	// Add 2 new files post-compact
	store.Put(ctx, "new1.txt", strings.NewReader("n1"))
	store.Put(ctx, "new2.txt", strings.NewReader("n2"))

	// S3 snapshot: 5 surviving + 2 new = 7
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")
	s3Log, _ := storeB.getOpsLog(ctx)
	s3Snap, _ := s3Log.Snapshot(ctx)
	if s3Snap.Len() != 7 {
		t.Errorf("S3 snapshot has %d files, want 7 (5 surviving + 2 new)", s3Snap.Len())
	}

	// Poller: only 2 post-compact ops
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)

	localSnap, _ := localLog.Snapshot()
	if localSnap.Len() != 2 {
		t.Errorf("poller local log has %d files, want 2 (only post-compact ops)", localSnap.Len())
	}

	// Surviving pre-compact files not in local log
	for i := 5; i < 10; i++ {
		path := "f" + string(rune('a'+i)) + ".txt"
		if _, ok := localSnap.Lookup(path); ok {
			t.Errorf("%s should not be in local log (compacted into snapshot)", path)
		}
	}
}

func TestIntegrationCompactThenNewOpsMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	// Device A uploads, compacts, then adds more
	id, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, id, "dev-a")
	storeA.SetNamespace("shared")

	storeA.Put(ctx, "before.txt", strings.NewReader("before compact"))
	Compact(ctx, backend, id, 2)
	storeA.Put(ctx, "after.txt", strings.NewReader("after compact"))

	// Verify: 0 old ops (compacted), 1 new op, 1 snapshot
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 1 {
		t.Errorf("expected 1 op after compact+put, got %d", len(opKeys))
	}
	snapKeys, _ := backend.List(ctx, "manifests/snapshot-")
	if len(snapKeys) == 0 {
		t.Fatal("no snapshot after compact")
	}

	// Device B: uses the S3 OpsLog directly (which reads snapshot + ops)
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")
	logB, _ := storeB.getOpsLog(ctx)
	snap, _ := logB.Snapshot(ctx)

	// S3 snapshot has both files (before from snapshot, after from op)
	if snap.Len() != 2 {
		t.Errorf("S3 snapshot has %d files, want 2", snap.Len())
	}
	if _, ok := snap.Lookup("before.txt"); !ok {
		t.Error("before.txt missing from S3 snapshot")
	}
	if _, ok := snap.Lookup("after.txt"); !ok {
		t.Error("after.txt missing from S3 snapshot")
	}

	// The poller only reads ops (not S3 snapshots), so a new device
	// that uses the poller + local log will only get after.txt from ops.
	// This is a known gap: the daemon's initial sync should bootstrap
	// from the S3 snapshot, not just replay ops.
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	poller := NewPollerV2(storeB, localLog, 30*time.Second, "shared", nil)
	reconciler := NewReconciler(storeB, localLog, outbox, localDir, nil, nil)
	poller.pokeReconciler = func() {}
	poller.onEvent = func(string, map[string]any) {}
	reconciler.onEvent = func(string, map[string]any) {}

	poller.pollOnce(ctx)
	reconciler.reconcile(ctx)

	// after.txt comes through (from ops)
	data, err := os.ReadFile(filepath.Join(localDir, "after.txt"))
	if err != nil {
		t.Fatalf("after.txt not downloaded: %v", err)
	}
	if string(data) != "after compact" {
		t.Errorf("content = %q", string(data))
	}
}
