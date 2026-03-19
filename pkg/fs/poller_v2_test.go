package fs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	poller := NewPollerV2(storeB, inbox, state, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	entries, _ := inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1", len(entries))
	}
	if entries[0].Path != "from-a.txt" || entries[0].Op != OpPut {
		t.Errorf("entry: %+v", entries[0])
	}

	// Cursor should be updated
	if state.LastRemoteOp == 0 {
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	poller := NewPollerV2(store, inbox, state, time.Hour, "", nil)
	poller.pollOnce(ctx)

	// Should not inbox our own ops
	if inbox.Len() != 0 {
		t.Errorf("inbox has %d, want 0 (own ops)", inbox.Len())
	}

	// But cursor should still advance
	if state.LastRemoteOp == 0 {
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
	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Simulate: we already have this file with the same checksum
	// (need to get the checksum from the op)
	poller := NewPollerV2(storeB, inbox, state, time.Hour, "Test", nil)

	// First poll — should inbox
	poller.pollOnce(ctx)
	if inbox.Len() != 1 {
		t.Fatalf("first poll: inbox has %d, want 1", inbox.Len())
	}

	// Get the checksum from the inbox entry and put it in state
	entries, _ := inbox.ReadAll()
	state.SetFile("existing.txt", FileState{Checksum: entries[0].Checksum, Namespace: "Test"})
	inbox.Clear()

	// Reset cursor to re-fetch
	state.SetLastRemoteOp(0)

	// Second poll — should skip (already have it)
	poller.pollOnce(ctx)
	if inbox.Len() != 0 {
		t.Errorf("second poll: inbox has %d, want 0 (already have)", inbox.Len())
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// First poll — gets the put
	poller := NewPollerV2(storeB, inbox, state, time.Hour, "Test", nil)
	poller.pollOnce(ctx)
	entries, _ := inbox.ReadAll()
	state.SetFile("del.txt", FileState{Checksum: entries[0].Checksum, Namespace: "Test"})
	inbox.Clear()

	// A deletes the file
	time.Sleep(time.Second)
	storeA.Remove(ctx, "del.txt")

	// B polls — should inbox the delete
	poller.pollOnce(ctx)
	entries, _ = inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpDelete || entries[0].Path != "del.txt" {
		t.Errorf("entry: %+v", entries[0])
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// B only syncs "journal" namespace
	poller := NewPollerV2(storeB, inbox, state, time.Hour, "journal", nil)
	poller.pollOnce(ctx)

	entries, _ := inbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("inbox has %d, want 1 (only journal)", len(entries))
	}
	if entries[0].Path != "journal/note.txt" {
		t.Errorf("path = %q", entries[0].Path)
	}
}
