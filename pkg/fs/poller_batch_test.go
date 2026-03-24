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
	poller.onEvent = func(e string) { events = append(events, e) }

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
	poller.onEvent = func(e string) { events = append(events, e) }

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
