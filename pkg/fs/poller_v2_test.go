package fs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestPollerV2FetchesRemoteOps(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	// A uploads a file
	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "from-a.txt", strings.NewReader("hello from A"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B's poller should pick up A's op
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	// Local log should have the op
	fi, ok := localLog.Lookup("from-a.txt")
	if !ok {
		t.Fatal("from-a.txt not in local log after poll")
	}
	if fi.Device != "device-a" {
		t.Errorf("Device = %q, want device-a", fi.Device)
	}

	// Cursor should be updated
	if localLog.LastRemoteOp() == 0 {
		t.Error("cursor not updated")
	}
}

func TestPollerV2SkipsOwnOps(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "my-file.txt", strings.NewReader("my data"))

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), store.deviceID)

	poller := NewPollerV2(store, localLog, time.Hour, "", nil)
	poller.pollOnce(ctx)

	// Should not append our own ops
	if _, ok := localLog.Lookup("my-file.txt"); ok {
		t.Error("own ops should not be appended to local log")
	}

	// But cursor should still advance past own ops
	if localLog.LastRemoteOp() == 0 {
		t.Error("cursor should advance past own ops")
	}
}

func TestPollerV2SkipsAlreadyHave(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "existing.txt", strings.NewReader("data"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)

	// First poll — should append to local log
	poller.pollOnce(ctx)
	fi, ok := localLog.Lookup("existing.txt")
	if !ok {
		t.Fatal("existing.txt not in local log after first poll")
	}
	if fi.Checksum == "" {
		t.Fatal("checksum should not be empty")
	}

	// Pre-populate a fresh local log with the same file (simulates "already have")
	localLog2 := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops2.jsonl"), storeB.deviceID)
	localLog2.Append(opslog.Entry{
		Type: opslog.Put, Path: "existing.txt", Checksum: fi.Checksum,
		Namespace: "Test", Device: "device-a", Timestamp: 1, Seq: 1,
	})

	poked := false
	poller2 := NewPollerV2(storeB, localLog2, time.Hour, "Test", nil)
	poller2.pokeReconciler = func() { poked = true }

	// Second poll — should skip (already have same checksum)
	poller2.pollOnce(ctx)
	if poked {
		t.Error("reconciler should not be poked when all ops already have")
	}
}

func TestPollerV2RemoteDelete(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "del.txt", strings.NewReader("delete me"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)

	// First poll — gets the put
	poller.pollOnce(ctx)
	if _, ok := localLog.Lookup("del.txt"); !ok {
		t.Fatal("del.txt not in local log after first poll")
	}

	// A deletes the file
	time.Sleep(time.Second)
	storeA.Remove(ctx, "del.txt")

	// B polls — delete should be appended
	poller.pollOnce(ctx)

	// Local log should no longer have the file
	if _, ok := localLog.Lookup("del.txt"); ok {
		t.Error("del.txt should be removed from local log after delete")
	}
}

func TestPollerV2NamespaceFilter(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.Put(ctx, "journal/note.txt", strings.NewReader("journal"))
	storeA.Put(ctx, "photos/cat.jpg", strings.NewReader("cat"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	// B only syncs "journal" namespace
	poller := NewPollerV2(storeB, localLog, time.Hour, "journal", nil)
	poller.pollOnce(ctx)

	// Only journal file should be in local log
	if _, ok := localLog.Lookup("journal/note.txt"); !ok {
		t.Error("journal/note.txt should be in local log")
	}
	if _, ok := localLog.Lookup("photos/cat.jpg"); ok {
		t.Error("photos/cat.jpg should NOT be in local log (wrong namespace)")
	}
}

func TestPollerV2PokesReconciler(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "a.txt", strings.NewReader("aaa"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poked := false
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pokeReconciler = func() { poked = true }
	poller.pollOnce(ctx)

	if !poked {
		t.Error("reconciler should be poked after new ops")
	}

	// Second poll (no new ops) should NOT poke
	poked = false
	poller.pollOnce(ctx)
	if poked {
		t.Error("reconciler should not be poked when no new ops")
	}
}
