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

func TestIntegrationCompactThenNewOpsMinIO(t *testing.T) {
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
