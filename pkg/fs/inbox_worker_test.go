package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestInboxWorkerDownload(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put a file in S3
	ctx := context.Background()
	store.Put(ctx, "remote.txt", strings.NewReader("from device B"))

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Add download entry
	inbox.Append(NewInboxPut("remote.txt", "xxx", "Test", "device-b", nil))

	worker := NewInboxWorker(store, inbox, state, localDir, nil)
	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(2 * time.Second)
	cancel()

	// Inbox should be drained
	if inbox.Len() != 0 {
		t.Errorf("inbox has %d entries, want 0", inbox.Len())
	}

	// File should exist locally
	data, err := os.ReadFile(filepath.Join(localDir, "remote.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if string(data) != "from device B" {
		t.Errorf("content = %q", string(data))
	}

	// State should be updated
	fs, ok := state.GetFile("remote.txt")
	if !ok {
		t.Error("state missing remote.txt")
	}
	if fs.Namespace != "Test" {
		t.Errorf("namespace = %q", fs.Namespace)
	}
}

func TestInboxWorkerDelete(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create a local file
	localFile := filepath.Join(localDir, "to-delete.txt")
	os.WriteFile(localFile, []byte("delete me"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))
	state.SetFile("to-delete.txt", FileState{Checksum: "abc", Namespace: "Test"})

	// Add delete entry
	inbox.Append(NewInboxDelete("to-delete.txt", "device-b"))

	worker := NewInboxWorker(store, inbox, state, localDir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(time.Second)
	cancel()

	// Inbox drained
	if inbox.Len() != 0 {
		t.Errorf("inbox has %d", inbox.Len())
	}

	// File should be gone
	if _, err := os.Stat(localFile); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}

	// State should not have the file
	if _, ok := state.GetFile("to-delete.txt"); ok {
		t.Error("state should not have to-delete.txt")
	}
}

func TestInboxWorkerCrashRecovery(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "recover.txt", strings.NewReader("crash data"))

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	inboxPath := filepath.Join(tmpDir, "inbox.jsonl")

	// Simulate crash: write entry but don't process
	inbox1 := NewSyncLog[InboxEntry](inboxPath)
	inbox1.Append(NewInboxPut("recover.txt", "xxx", "Test", "device-b", nil))

	// "Restart"
	inbox2 := NewSyncLog[InboxEntry](inboxPath)
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))
	worker := NewInboxWorker(store, inbox2, state, localDir, nil)

	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	time.Sleep(2 * time.Second)
	cancel()

	// Entry should be processed
	if inbox2.Len() != 0 {
		t.Errorf("inbox has %d after recovery", inbox2.Len())
	}

	data, _ := os.ReadFile(filepath.Join(localDir, "recover.txt"))
	if string(data) != "crash data" {
		t.Errorf("content = %q", string(data))
	}
}

func TestInboxWorkerSkipsEmptyOverNonEmpty(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put an empty file in S3
	ctx := context.Background()
	store.Put(ctx, "notwipe.txt", strings.NewReader(""))

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Local file has real content
	os.WriteFile(filepath.Join(localDir, "notwipe.txt"), []byte("real content"), 0644)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	emptyHash := "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"
	inbox.Append(InboxEntry{Op: OpPut, Path: "notwipe.txt", Checksum: emptyHash, Namespace: "Test", Device: "device-b", Timestamp: time.Now().Unix()})

	worker := NewInboxWorker(store, inbox, state, localDir, nil)
	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(time.Second)
	cancel()

	// Local file should still have real content
	data, _ := os.ReadFile(filepath.Join(localDir, "notwipe.txt"))
	if string(data) != "real content" {
		t.Errorf("content wiped: %q", string(data))
	}
}
