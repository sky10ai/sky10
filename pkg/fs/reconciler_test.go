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

// getOpsEntries reads all ops from S3 as opslog.Entry values.
func getOpsEntries(t *testing.T, store *Store) []opslog.Entry {
	t.Helper()
	ctx := context.Background()
	log, err := store.getOpsLog(ctx)
	if err != nil {
		t.Fatalf("getting ops log: %v", err)
	}
	entries, err := log.ReadSince(ctx, 0)
	if err != nil {
		t.Fatalf("reading entries: %v", err)
	}
	return entries
}

func TestReconcilerDownload(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "remote.txt", strings.NewReader("from remote"))

	// Get the entry from S3 (includes chunk hashes)
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Append to local log (as if the poller did it)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	// Run reconciler
	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	// File should be on disk
	data, err := os.ReadFile(filepath.Join(localDir, "remote.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if string(data) != "from remote" {
		t.Errorf("content = %q", string(data))
	}
}

func TestReconcilerDelete(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// File on disk that's NOT in the snapshot (remote delete scenario)
	os.WriteFile(filepath.Join(localDir, "deleted-remote.txt"), []byte("should go"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Empty local log — file is on disk but not in snapshot
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(context.Background())

	// File should be deleted
	if _, err := os.Stat(filepath.Join(localDir, "deleted-remote.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted (not in snapshot)")
	}
}

func TestReconcilerSkipsMatching(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Write a file locally
	os.WriteFile(filepath.Join(localDir, "existing.txt"), []byte("same content"), 0644)

	// Compute its checksum (same scheme ScanDirectory uses)
	cksum, err := fileChecksum(filepath.Join(localDir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Append to local log with matching checksum (as if watcher handler tracked it)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.Append(opslog.Entry{
		Type: opslog.Put, Path: "existing.txt", Checksum: cksum,
		Device: "dev-a", Timestamp: 100, Seq: 1,
	})

	// Track events — reconciler should do nothing
	active := false
	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.onEvent = func(string) { active = true }
	r.reconcile(context.Background())

	if active {
		t.Error("reconciler should not be active when files match")
	}
}

func TestReconcilerCreatePlusDeleteCompaction(t *testing.T) {
	// If a file was created then deleted remotely, the snapshot shows
	// nothing and the reconciler does no work.
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "ephemeral.txt", strings.NewReader("short lived"))
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Append put then delete — snapshot should be empty
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}
	localLog.Append(opslog.Entry{
		Type: opslog.Delete, Path: "ephemeral.txt",
		Device: entries[0].Device, Timestamp: entries[0].Timestamp + 1, Seq: entries[0].Seq + 1,
	})

	// Verify snapshot is empty
	snap, _ := localLog.Snapshot()
	if snap.Len() != 0 {
		t.Fatalf("snapshot should be empty, has %d files", snap.Len())
	}

	active := false
	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.onEvent = func(string) { active = true }
	r.reconcile(ctx)

	if active {
		t.Error("reconciler should do no work for create+delete (compacted)")
	}
}

func TestReconcilerSkipsEmptyOverNonEmpty(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Upload an empty file to S3
	ctx := context.Background()
	store.Put(ctx, "notwipe.txt", strings.NewReader(""))
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Local file has real content
	os.WriteFile(filepath.Join(localDir, "notwipe.txt"), []byte("real content"), 0644)

	// Append to local log
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	// Local file should still have real content
	data, _ := os.ReadFile(filepath.Join(localDir, "notwipe.txt"))
	if string(data) != "real content" {
		t.Errorf("content wiped: %q", string(data))
	}
}

func TestReconcilerAtomicWrite(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "big-file.txt", strings.NewReader("important data"))
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	// Watch for 0-byte intermediate files
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

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)
	<-watchDone

	if sawZeroByte {
		t.Error("saw 0-byte file — reconciler should write to temp first")
	}

	data, err := os.ReadFile(filepath.Join(localDir, "big-file.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if !strings.Contains(string(data), "important data") {
		t.Errorf("content = %q", string(data))
	}
}

func TestReconcilerMultipleFiles(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "a.txt", strings.NewReader("aaa"))
	store.Put(ctx, "b.txt", strings.NewReader("bbb"))
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	dataA, err := os.ReadFile(filepath.Join(localDir, "a.txt"))
	if err != nil {
		t.Fatalf("a.txt not downloaded: %v", err)
	}
	if string(dataA) != "aaa" {
		t.Errorf("a.txt content = %q", string(dataA))
	}

	dataB, err := os.ReadFile(filepath.Join(localDir, "b.txt"))
	if err != nil {
		t.Fatalf("b.txt not downloaded: %v", err)
	}
	if string(dataB) != "bbb" {
		t.Errorf("b.txt content = %q", string(dataB))
	}
}

func TestReconcilerSkipsPendingDeletes(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()
	store.Put(ctx, "doomed.txt", strings.NewReader("will be deleted"))
	entries := getOpsEntries(t, store)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	// Simulate: watcher already queued a delete in the outbox (user deleted the file).
	// The reconciler should NOT re-download it.
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	outbox.Append(OutboxEntry{
		Op:   OpDelete,
		Path: "doomed.txt",
	})

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	if _, err := os.Stat(filepath.Join(localDir, "doomed.txt")); err == nil {
		t.Error("doomed.txt should NOT have been downloaded — pending delete in outbox")
	}
}
