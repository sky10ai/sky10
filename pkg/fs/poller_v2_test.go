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
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, inbox, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	// Inbox should have the entry (for download)
	entries, _ := inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1", len(entries))
	}
	if entries[0].Path != "from-a.txt" || entries[0].Op != OpPut {
		t.Errorf("entry: %+v", entries[0])
	}

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
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), store.deviceID)

	poller := NewPollerV2(store, inbox, localLog, time.Hour, "", nil)
	poller.pollOnce(ctx)

	// Should not inbox our own ops
	if inbox.Len() != 0 {
		t.Errorf("inbox has %d, want 0 (own ops)", inbox.Len())
	}

	// But cursor should still advance past own ops
	if localLog.LastRemoteOp() == 0 {
		t.Error("cursor should advance past own ops")
	}

	// Own ops should NOT be in local log (poller skips them)
	if _, ok := localLog.Lookup("my-file.txt"); ok {
		t.Error("own ops should not be appended to local log")
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
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, inbox, localLog, time.Hour, "Test", nil)

	// First poll — should inbox the put and append to local log
	poller.pollOnce(ctx)
	if inbox.Len() != 1 {
		t.Fatalf("first poll: inbox has %d, want 1", inbox.Len())
	}
	inbox.Clear()

	// Local log should now have the file
	fi, ok := localLog.Lookup("existing.txt")
	if !ok {
		t.Fatal("existing.txt not in local log after first poll")
	}
	if fi.Checksum == "" {
		t.Fatal("checksum should not be empty")
	}

	// Reset cursor to re-fetch same ops — use a fresh local log
	// with the same file pre-populated (simulates "already have")
	localLog2 := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops2.jsonl"), storeB.deviceID)
	localLog2.Append(opslog.Entry{
		Type: opslog.Put, Path: "existing.txt", Checksum: fi.Checksum,
		Namespace: "Test", Device: "device-a", Timestamp: 1, Seq: 1,
	})
	inbox2 := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox2.jsonl"))
	poller2 := NewPollerV2(storeB, inbox2, localLog2, time.Hour, "Test", nil)

	// Second poll — should skip (already have same checksum)
	poller2.pollOnce(ctx)
	if inbox2.Len() != 0 {
		t.Errorf("second poll: inbox has %d, want 0 (already have)", inbox2.Len())
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
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	// First poll — gets the put
	poller := NewPollerV2(storeB, inbox, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)
	if inbox.Len() != 1 {
		t.Fatalf("first poll: inbox has %d, want 1", inbox.Len())
	}
	inbox.Clear()

	// Verify file is in local log
	if _, ok := localLog.Lookup("del.txt"); !ok {
		t.Fatal("del.txt not in local log after first poll")
	}

	// A deletes the file
	time.Sleep(time.Second)
	storeA.Remove(ctx, "del.txt")

	// B polls — should inbox the delete
	poller.pollOnce(ctx)
	entries, _ := inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpDelete || entries[0].Path != "del.txt" {
		t.Errorf("entry: %+v", entries[0])
	}

	// Local log should no longer have the file (delete appended)
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
	// A writes to two namespaces
	storeA.Put(ctx, "journal/note.txt", strings.NewReader("journal"))
	storeA.Put(ctx, "photos/cat.jpg", strings.NewReader("cat"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	// B only syncs "journal" namespace
	poller := NewPollerV2(storeB, inbox, localLog, time.Hour, "journal", nil)
	poller.pollOnce(ctx)

	entries, _ := inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1 (only journal)", len(entries))
	}
	if entries[0].Path != "journal/note.txt" {
		t.Errorf("path = %q", entries[0].Path)
	}

	// Only journal file should be in local log
	if _, ok := localLog.Lookup("journal/note.txt"); !ok {
		t.Error("journal/note.txt should be in local log")
	}
	if _, ok := localLog.Lookup("photos/cat.jpg"); ok {
		t.Error("photos/cat.jpg should NOT be in local log (wrong namespace)")
	}
}

func TestPollerV2AppendsToLocalLog(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "a.txt", strings.NewReader("aaa"))
	storeA.Put(ctx, "b.txt", strings.NewReader("bbb"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, inbox, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	// Both files should be in the local log snapshot
	snap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 2 {
		t.Errorf("snapshot has %d files, want 2", snap.Len())
	}

	// Second poll should fetch nothing (cursor advanced)
	inbox.Clear()
	poller.pollOnce(ctx)
	if inbox.Len() != 0 {
		t.Errorf("second poll: inbox has %d, want 0", inbox.Len())
	}
}
