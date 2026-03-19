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

// getChunksFromOps extracts chunk hashes for a path from the ops written to S3.
func getChunksFromOps(t *testing.T, store *Store, path string) []string {
	t.Helper()
	ctx := context.Background()
	opsKey, _ := store.opsKey(ctx)
	ops, err := ReadOps(ctx, store.backend, 0, opsKey)
	if err != nil {
		t.Fatalf("reading ops: %v", err)
	}
	for _, op := range ops {
		if op.Path == path && op.Type == OpPut {
			return op.Chunks
		}
	}
	t.Fatalf("no put op found for %s", path)
	return nil
}

func TestInboxWorkerDownload(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put a file in S3
	ctx := context.Background()
	store.Put(ctx, "remote.txt", strings.NewReader("from device B"))
	chunks := getChunksFromOps(t, store, "remote.txt")

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Add download entry with chunks
	inbox.Append(NewInboxPut("remote.txt", "xxx", "default", "device-b", chunks))

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
	if fs.Namespace != "default" {
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
	chunks := getChunksFromOps(t, store, "recover.txt")

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	inboxPath := filepath.Join(tmpDir, "inbox.jsonl")

	// Simulate crash: write entry but don't process
	inbox1 := NewSyncLog[InboxEntry](inboxPath)
	inbox1.Append(NewInboxPut("recover.txt", "xxx", "default", "device-b", chunks))

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

// Regression: inbox must download to temp dir, not create 0-byte file in
// the watched directory. Previously, os.Create wrote a 0-byte file that the
// watcher immediately re-uploaded, overwriting real content on S3.
func TestInboxWorkerDownloadsToTemp(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "big-file.txt", strings.NewReader("important data that must not be 0 bytes"))
	chunks := getChunksFromOps(t, store, "big-file.txt")

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	inbox.Append(NewInboxPut("big-file.txt", "xxx", "default", "device-b", chunks))

	worker := NewInboxWorker(store, inbox, state, localDir, nil)

	// Start a watcher on the local dir to detect intermediate 0-byte files
	sawZeroByte := false
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			info, err := os.Stat(filepath.Join(localDir, "big-file.txt"))
			if err == nil && info.Size() == 0 {
				sawZeroByte = true
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(3 * time.Second)
	cancel()
	<-watchDone

	if sawZeroByte {
		t.Error("saw 0-byte file in watched dir — inbox should download to temp first")
	}

	// File should exist with real content
	data, err := os.ReadFile(filepath.Join(localDir, "big-file.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if !strings.Contains(string(data), "important data") {
		t.Errorf("content = %q", string(data))
	}
}

// Regression: inbox should use direct chunk download (GetChunks) when chunk
// hashes are provided, avoiding loadCurrentState which reads ALL ops from S3.
func TestInboxWorkerDirectChunkDownload(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	content := "direct chunk download test content"
	store.Put(ctx, "chunked.txt", strings.NewReader(content))

	// Get the chunk hashes from the op we just wrote
	ops, err := ReadOps(ctx, backend, 0, nil)
	if err != nil {
		// Need to get the ops key first
		opsKey, _ := store.opsKey(ctx)
		ops, err = ReadOps(ctx, backend, 0, opsKey)
		if err != nil {
			t.Fatalf("reading ops: %v", err)
		}
	}

	var chunks []string
	var ns string
	for _, op := range ops {
		if op.Path == "chunked.txt" && op.Type == OpPut {
			chunks = op.Chunks
			ns = op.Namespace
		}
	}

	if len(chunks) == 0 {
		t.Fatal("no chunks found in op for chunked.txt")
	}

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	inbox := NewSyncLog[InboxEntry](filepath.Join(tmpDir, "inbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Provide chunk hashes — should bypass loadCurrentState
	inbox.Append(NewInboxPut("chunked.txt", "xxx", ns, "device-b", chunks))

	worker := NewInboxWorker(store, inbox, state, localDir, nil)
	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(2 * time.Second)
	cancel()

	if inbox.Len() != 0 {
		t.Errorf("inbox has %d entries", inbox.Len())
	}

	data, err := os.ReadFile(filepath.Join(localDir, "chunked.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
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
