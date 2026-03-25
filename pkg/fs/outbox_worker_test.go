package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestOutboxWorkerUpload(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	outboxPath := filepath.Join(tmpDir, "outbox.jsonl")

	outbox := NewSyncLog[OutboxEntry](outboxPath)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create a local file
	localFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(localFile, []byte("hello outbox"), 0644)

	// Add to outbox
	outbox.Append(NewOutboxPut("test.txt", "abc123", "Test", localFile))

	// Run worker
	worker := NewOutboxWorker(store, outbox, localLog, nil)
	ctx, cancel := context.WithCancel(context.Background())

	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(2 * time.Second)
	cancel()

	// Outbox should be drained
	if outbox.Len() != 0 {
		t.Errorf("outbox has %d entries, want 0", outbox.Len())
	}

	// File should be in S3
	entries, _ := store.List(ctx, "")
	found := false
	for _, e := range entries {
		if e.Path == "test.txt" {
			found = true
		}
	}
	if !found {
		t.Error("test.txt not uploaded to S3")
	}
}

func TestOutboxWorkerDelete(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put a file first so there's something to delete
	ctx := context.Background()
	store.Put(ctx, "delete-me.txt", strings.NewReader("data"))

	tmpDir := t.TempDir()
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Add delete to outbox
	outbox.Append(NewOutboxDelete("delete-me.txt", "somechecksum", "Test"))

	worker := NewOutboxWorker(store, outbox, localLog, nil)
	ctx, cancel := context.WithCancel(ctx)
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(2 * time.Second)
	cancel()

	// Outbox should be drained
	if outbox.Len() != 0 {
		t.Errorf("outbox has %d entries, want 0", outbox.Len())
	}

	// At least one delete op should exist in S3
	keys, _ := backend.List(context.Background(), "ops/")
	if len(keys) == 0 {
		t.Error("expected at least 1 op after delete")
	}
}

func TestOutboxWorkerCrashRecovery(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	outboxPath := filepath.Join(tmpDir, "outbox.jsonl")

	// Simulate crash: write to outbox but don't process
	outbox1 := NewSyncLog[OutboxEntry](outboxPath)
	localFile := filepath.Join(tmpDir, "survive.txt")
	os.WriteFile(localFile, []byte("crash recovery"), 0644)
	outbox1.Append(NewOutboxPut("survive.txt", "xxx", "Test", localFile))

	// "Restart" — new worker, same outbox file
	outbox2 := NewSyncLog[OutboxEntry](outboxPath)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	worker := NewOutboxWorker(store, outbox2, localLog, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	time.Sleep(2 * time.Second)
	cancel()

	// Should have processed the entry from the "crashed" session
	if outbox2.Len() != 0 {
		t.Errorf("outbox has %d entries after recovery, want 0", outbox2.Len())
	}
}

// Regression: when the outbox worker finds a file gone from disk, it must
// append a delete op to the local log. Without this, the stale put stays in
// the snapshot. If the same file reappears with the same content, the watcher
// dedup matches the old checksum and skips it — the blob never gets uploaded.
func TestOutboxWorkerFileGoneAppendsDelete(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Simulate watcher handler: AppendLocal(put) + queue in outbox
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "vanish.txt", Checksum: "abc123",
		Size: 12, Namespace: "Test",
	})
	outbox.Append(NewOutboxPut("vanish.txt", "abc123", "Test", filepath.Join(tmpDir, "vanish.txt")))

	// File is NOT on disk — simulates deletion before upload
	worker := NewOutboxWorker(store, outbox, localLog, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(time.Second)
	cancel()

	// Outbox should be drained
	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d, want 0", outbox.Len())
	}

	// Local log should no longer have the file — the delete must supersede the put
	if _, ok := localLog.Lookup("vanish.txt"); ok {
		t.Error("vanish.txt should NOT be in local log after file-gone delete")
	}
}

// Regression: after a file-gone delete is recorded, re-creating the file with
// the same content must NOT be skipped by the watcher handler's dedup check.
func TestOutboxWorkerFileGoneThenReappear(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Step 1: watcher handler records put + outbox entry for file
	localFile := filepath.Join(localDir, "reappear.txt")
	os.WriteFile(localFile, []byte("hello"), 0644)
	cksum, _ := fileChecksum(localFile)

	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "reappear.txt", Checksum: cksum,
		Size: 5, Namespace: "Test",
	})
	outbox.Append(NewOutboxPut("reappear.txt", cksum, "Test", localFile))

	// Step 2: file deleted before upload
	os.Remove(localFile)

	worker := NewOutboxWorker(store, outbox, localLog, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(time.Second)
	cancel()

	// Step 3: file reappears with same content
	os.WriteFile(localFile, []byte("hello"), 0644)

	// Step 4: watcher handler fires again
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "reappear.txt", Type: FileCreated}})

	// The outbox should have a new put entry — dedup must NOT skip it
	entries, _ := outbox.ReadAll()
	found := false
	for _, e := range entries {
		if e.Op == OpPut && e.Path == "reappear.txt" {
			found = true
		}
	}
	if !found {
		t.Error("reappear.txt should be re-queued in outbox — watcher dedup should not skip after file-gone delete")
	}
}

func TestOutboxWorkerMissingFile(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Add upload for a file that doesn't exist
	outbox.Append(NewOutboxPut("ghost.txt", "xxx", "Test", filepath.Join(tmpDir, "ghost.txt")))

	worker := NewOutboxWorker(store, outbox, localLog, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	worker.Poke()
	time.Sleep(time.Second)
	cancel()

	// Should remove the entry (file is gone, nothing to upload)
	if outbox.Len() != 0 {
		t.Errorf("outbox has %d, want 0 (missing file should be removed)", outbox.Len())
	}
}
