package fs

import (
	"bytes"
	"context"
	"crypto/sha3"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// putAndLog uploads content to S3 via store.Put, then writes an opslog.Entry
// to the local log with the resulting chunks/checksum. The entry appears as
// if a remote device uploaded it, so the reconciler will download it.
func putAndLog(t *testing.T, store *Store, localLog *opslog.LocalOpsLog, path, content string, seq int) {
	t.Helper()
	ctx := context.Background()
	if err := store.Put(ctx, path, strings.NewReader(content)); err != nil {
		t.Fatalf("store.Put(%s): %v", path, err)
	}
	res := store.LastPutResult()
	if res == nil {
		t.Fatalf("store.LastPutResult() nil after Put(%s)", path)
	}
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      path,
		Chunks:    res.Chunks,
		Checksum:  res.Checksum,
		Size:      res.Size,
		Namespace: NamespaceFromPath(path),
		Device:    "remote-device",
		Timestamp: int64(1000 + seq),
		Seq:       seq,
	})
}

func TestReconcilerDownload(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Upload blob to S3, then write entry to local log with chunks.
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "remote.txt", "from remote", 1)

	// Run reconciler — should download the file
	ctx := context.Background()
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

func TestReconcilerDownloadUsesConfiguredStagingDir(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "remote.txt", "from remote", 1)

	stagingDir := filepath.Join(tmpDir, "drive-data", "transfer", "staging")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.stagingDir = stagingDir
	r.reconcile(context.Background())

	data, err := os.ReadFile(filepath.Join(localDir, "remote.txt"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if string(data) != "from remote" {
		t.Fatalf("content = %q", string(data))
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("read staging dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging dir should be empty after publish, found %d entries", len(entries))
	}
	sessionEntries, err := os.ReadDir(transferSessionsDirFromStaging(stagingDir))
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	if len(sessionEntries) != 0 {
		t.Fatalf("sessions dir should be empty after publish, found %d entries", len(sessionEntries))
	}
}

func TestReconcilerReusesLocalFileChunksBeforeBackendFetch(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatal(err)
	}

	base := make([]byte, 5*1024*1024)
	for i := range base {
		base[i] = byte(i % 251)
	}
	appended := bytes.Repeat([]byte("z"), 1024*1024)
	remoteContent := append(append([]byte(nil), base...), appended...)

	if err := os.WriteFile(filepath.Join(localDir, "shared.bin"), base, 0644); err != nil {
		t.Fatalf("WriteFile local base: %v", err)
	}

	ctx := context.Background()
	if err := store.Put(ctx, "shared.bin", bytes.NewReader(remoteContent)); err != nil {
		t.Fatalf("store.Put(shared.bin): %v", err)
	}
	res := store.LastPutResult()
	if res == nil {
		t.Fatal("store.LastPutResult() nil after Put(shared.bin)")
	}

	baseHashes := chunkHashesForTest(t, base)
	overlap := 0
	for _, hash := range res.Chunks {
		if _, ok := baseHashes[hash]; ok {
			overlap++
		}
	}
	if overlap == 0 {
		t.Fatal("expected local file to share at least one chunk with remote content")
	}
	if overlap >= len(res.Chunks) {
		t.Fatalf("expected partial overlap, got overlap=%d total=%d", overlap, len(res.Chunks))
	}

	counted := &countingBackend{Backend: backend}
	store.backend = counted

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "shared.bin",
		Chunks:    res.Chunks,
		Checksum:  res.Checksum,
		Size:      res.Size,
		Namespace: NamespaceFromPath("shared.bin"),
		Device:    "remote-device",
		Timestamp: 1001,
		Seq:       1,
	}); err != nil {
		t.Fatalf("localLog.Append(shared.bin): %v", err)
	}

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	data, err := os.ReadFile(filepath.Join(localDir, "shared.bin"))
	if err != nil {
		t.Fatalf("file not downloaded: %v", err)
	}
	if !bytes.Equal(data, remoteContent) {
		t.Fatal("downloaded file content mismatch")
	}

	if got, want := int(counted.getCalls.Load()), len(res.Chunks)-overlap; got != want {
		t.Fatalf("backend Get calls = %d, want %d with local chunk reuse", got, want)
	}
	if got := counted.getRangeCalls.Load(); got != 0 {
		t.Fatalf("backend GetRange calls = %d, want 0", got)
	}
}

func TestReconcilerBoundsConcurrentFileDownloads(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatal(err)
	}

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for i := 0; i < 6; i++ {
		putAndLog(t, store, localLog, fmt.Sprintf("file-%d.txt", i), fmt.Sprintf("content-%d", i), i+1)
	}

	gated := newGatedCountingBackend(backend)
	store.backend = gated
	store.chunkPrefetch = 1
	store.remoteFetchSem = make(chan struct{}, 16)

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.maxConcurrentDownloads = 2

	done := make(chan struct{})
	go func() {
		r.reconcile(context.Background())
		close(done)
	}()

	waitForBackendEntries(t, gated.entered, 2)
	gated.release(16)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for bounded reconcile")
	}

	if got := gated.MaxInFlight(); got != 2 {
		t.Fatalf("max concurrent file downloads = %d, want 2", got)
	}
}

// Regression: empty files (size=0, chunks=0) were skipped by the
// reconciler because the chunkless-put guard treated them as "upload
// pending." An empty file's presence is state — it must be created.
func TestReconcilerCreatesEmptyFile(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Simulate a remote device that uploaded an empty file.
	// Empty files have size=0, no chunks, and the SHA3-256 of empty input.
	emptyHash := ContentHash(nil)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "local-dev")
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "empty.txt",
		Checksum:  emptyHash,
		Size:      0,
		Namespace: "default",
		Device:    "remote-dev",
		Timestamp: 1000,
		Seq:       1,
	})

	ctx := context.Background()
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	// Empty file should exist on disk
	info, err := os.Stat(filepath.Join(localDir, "empty.txt"))
	if err != nil {
		t.Fatalf("empty file not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}

func chunkHashesForTest(t *testing.T, data []byte) map[string]struct{} {
	t.Helper()
	chunker := NewChunker(bytes.NewReader(data))
	hashes := make(map[string]struct{})
	for {
		chunk, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("chunker.Next(): %v", err)
		}
		hashes[chunk.Hash] = struct{}{}
	}
	return hashes
}

func TestReconcilerDelete(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// File on disk that was synced, then deleted remotely
	os.WriteFile(filepath.Join(localDir, "deleted-remote.txt"), []byte("should go"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Log has put then delete — file was explicitly deleted by remote device
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.Append(opslog.Entry{
		Type: opslog.Put, Path: "deleted-remote.txt", Checksum: checksumOf("should go"),
		Device: "dev-remote", Timestamp: 100, Seq: 1,
	})
	localLog.Append(opslog.Entry{
		Type: opslog.Delete, Path: "deleted-remote.txt",
		Device: "dev-remote", Timestamp: 200, Seq: 2,
	})

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(context.Background())

	// File should be deleted — explicit remote delete op
	if _, err := os.Stat(filepath.Join(localDir, "deleted-remote.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted (remote delete op in log)")
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
	r.onEvent = func(string, map[string]any) { active = true }
	r.reconcile(context.Background())

	if active {
		t.Error("reconciler should not be active when files match")
	}
}

func TestReconcilerCreatePlusDeleteCompaction(t *testing.T) {
	t.Parallel()
	// If a file was created then deleted remotely, the snapshot shows
	// nothing and the reconciler does no work.
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Upload blob, write put entry, then append a delete.
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "ephemeral.txt", "short lived", 1)
	localLog.Append(opslog.Entry{
		Type: opslog.Delete, Path: "ephemeral.txt",
		Device: "remote-device", Timestamp: 1002, Seq: 2,
	})

	// Verify snapshot is empty
	snap, _ := localLog.Snapshot()
	if snap.Len() != 0 {
		t.Fatalf("snapshot should be empty, has %d files", snap.Len())
	}

	active := false
	ctx := context.Background()
	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.onEvent = func(string, map[string]any) { active = true }
	r.reconcile(ctx)

	if active {
		t.Error("reconciler should do no work for create+delete (compacted)")
	}
}

func TestReconcilerSkipsEmptyOverNonEmpty(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Upload an empty file to S3 and write entry to local log.
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "notwipe.txt", "", 1)

	// Local file has real content
	os.WriteFile(filepath.Join(localDir, "notwipe.txt"), []byte("real content"), 0644)

	ctx := context.Background()
	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	// Local file should still have real content
	data, _ := os.ReadFile(filepath.Join(localDir, "notwipe.txt"))
	if string(data) != "real content" {
		t.Errorf("content wiped: %q", string(data))
	}
}

func TestReconcilerAtomicWrite(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "big-file.txt", "important data", 1)

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

	ctx := context.Background()
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
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "a.txt", "aaa", 1)
	putAndLog(t, store, localLog, "b.txt", "bbb", 2)

	ctx := context.Background()
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

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "doomed.txt", "will be deleted", 1)

	// Simulate: watcher already queued a delete in the outbox (user deleted the file).
	// The reconciler should NOT re-download it.
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	outbox.Append(OutboxEntry{
		Op:   OpDelete,
		Path: "doomed.txt",
	})

	ctx := context.Background()
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	if _, err := os.Stat(filepath.Join(localDir, "doomed.txt")); err == nil {
		t.Error("doomed.txt should NOT have been downloaded — pending delete in outbox")
	}
}

func TestReconcilerRemovesStaleDirectories(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")

	// Create local directories with files
	os.MkdirAll(filepath.Join(localDir, "keep", "sub"), 0755)
	os.MkdirAll(filepath.Join(localDir, "stale", "nested"), 0755)
	os.WriteFile(filepath.Join(localDir, "keep", "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(localDir, "keep", "sub", "b.txt"), []byte("b"), 0644)
	// stale/ has no files — only empty dirs

	// Snapshot: only keep/a.txt and keep/sub/b.txt exist
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "keep/a.txt", Checksum: checksumOf("a"), Namespace: "Test",
	})
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "keep/sub/b.txt", Checksum: checksumOf("b"), Namespace: "Test",
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// stale/ and stale/nested/ should be gone
	if _, err := os.Stat(filepath.Join(localDir, "stale")); err == nil {
		t.Error("stale/ directory should have been removed")
	}
	// keep/ should still exist
	if _, err := os.Stat(filepath.Join(localDir, "keep")); err != nil {
		t.Error("keep/ directory should still exist")
	}
	if _, err := os.Stat(filepath.Join(localDir, "keep", "sub")); err != nil {
		t.Error("keep/sub/ directory should still exist")
	}
}

func TestReconcilerDeleteDirEndToEnd(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")

	// Create local files simulating a synced directory
	os.MkdirAll(filepath.Join(localDir, "photos", "vacation"), 0755)
	os.WriteFile(filepath.Join(localDir, "photos", "a.jpg"), []byte("img-a"), 0644)
	os.WriteFile(filepath.Join(localDir, "photos", "vacation", "b.jpg"), []byte("img-b"), 0644)
	os.WriteFile(filepath.Join(localDir, "notes.txt"), []byte("keep"), 0644)

	// Local log has all three files, then a delete_dir for photos/
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "photos/a.jpg", Checksum: checksumOf("img-a"), Namespace: "Test",
	})
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "photos/vacation/b.jpg", Checksum: checksumOf("img-b"), Namespace: "Test",
	})
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "notes.txt", Checksum: checksumOf("keep"), Namespace: "Test",
	})

	// Remote device deleted the directory
	localLog.Append(opslog.Entry{
		Type: opslog.DeleteDir, Path: "photos",
		Device: "dev-b", Timestamp: 9999999999, Seq: 1,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// photos/ and all contents should be gone
	if _, err := os.Stat(filepath.Join(localDir, "photos")); err == nil {
		t.Error("photos/ directory should have been removed")
	}
	// notes.txt should still exist
	if _, err := os.Stat(filepath.Join(localDir, "notes.txt")); err != nil {
		t.Error("notes.txt should still exist")
	}
}

func TestReconcilerCreatesDirectories(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Snapshot has an explicit create_dir entry
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.Append(opslog.Entry{
		Type: opslog.CreateDir, Path: "empty-dir",
		Device: "dev-b", Timestamp: 100, Seq: 1,
	})
	localLog.Append(opslog.Entry{
		Type: opslog.CreateDir, Path: "nested/deep",
		Device: "dev-b", Timestamp: 100, Seq: 2,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Directories should be created
	if _, err := os.Stat(filepath.Join(localDir, "empty-dir")); err != nil {
		t.Error("empty-dir should have been created")
	}
	if _, err := os.Stat(filepath.Join(localDir, "nested", "deep")); err != nil {
		t.Error("nested/deep should have been created")
	}
}

func TestReconcilerKeepsExplicitEmptyDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")

	// Create an empty dir on disk
	os.MkdirAll(filepath.Join(localDir, "keep-me"), 0755)

	// Snapshot has a create_dir entry for it (no files)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.Append(opslog.Entry{
		Type: opslog.CreateDir, Path: "keep-me",
		Device: "dev-a", Timestamp: 100, Seq: 1,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Directory should NOT be removed — it's explicitly tracked
	if _, err := os.Stat(filepath.Join(localDir, "keep-me")); err != nil {
		t.Error("keep-me should still exist — explicit create_dir in snapshot")
	}
}

// Regression: reconciler must not recreate a directory that was deleted
// since the snapshot was taken. The stale snapshot has the dir in Dirs(),
// but a delete_dir appended during reconciliation removes it. Without
// the re-check, the reconciler creates the dir, the watcher emits
// create_dir (which beats the delete_dir in the CRDT), and the directory
// keeps coming back in a ping-pong cycle.
func TestReconcilerDoesNotRecreateDirDeletedDuringReconcile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Set up local log with a create_dir
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.Append(opslog.Entry{
		Type: opslog.CreateDir, Path: "mydir",
		Device: "dev-b", Timestamp: 100, Seq: 1,
	})

	// Take a stale snapshot (what the reconciler would have at start)
	staleSnap, _ := localLog.Snapshot()
	if staleSnap == nil {
		t.Fatal("snapshot should not be nil")
	}
	staleDirs := staleSnap.Dirs()
	if _, ok := staleDirs["mydir"]; !ok {
		t.Fatal("mydir should be in stale snapshot dirs")
	}

	// Simulate: after the reconciler took the snapshot, a delete_dir
	// is appended (by the watcher processing the user's rm -rf).
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.DeleteDir, Path: "mydir", Namespace: "Test",
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	// Call createDirectories with the STALE dirs — simulates the race
	// where reconcile() took the snapshot before the delete_dir.
	r.createDirectories(staleDirs)

	// mydir should NOT have been created on disk
	if _, err := os.Stat(filepath.Join(localDir, "mydir")); err == nil {
		t.Error("mydir should NOT be created — it was deleted during reconciliation")
	}
}

// Regression: watcher handler should not emit create_dir for a directory
// that already exists in the snapshot. This prevents the reconciler from
// triggering spurious create_dir ops when it creates directories.
func TestWatcherHandlerSkipsDirAlreadyInSnapshot(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	// Directory already tracked in the snapshot
	localLog.Append(opslog.Entry{
		Type: opslog.CreateDir, Path: "existing",
		Device: "dev-b", Timestamp: 100, Seq: 1,
	})

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	// Watcher fires DirCreated for "existing" (reconciler just created it on disk)
	handler.HandleEvents([]FileEvent{{Path: "existing", Type: DirCreated}})

	// Outbox should NOT have a create_dir for "existing"
	entries, _ := outbox.ReadAll()
	for _, e := range entries {
		if e.Op == OpCreateDir && e.Path == "existing" {
			t.Error("watcher should not emit create_dir for dir already in snapshot")
		}
	}
}

// Untracked files in the root survive (no delete op, no stale directory).
// But untracked files inside directories unknown to the snapshot are removed
// by reconcileDirectories' os.RemoveAll — see the comment there for the
// tradeoff (macOS .DS_Store blocking directory cleanup vs. small data-loss
// window during watcher debounce).
func TestReconcilerDoesNotDeleteUntrackedFiles(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Files on disk that were never tracked — watcher missed them (kqueue burst).
	// No ops in the log at all for these paths.
	os.MkdirAll(filepath.Join(localDir, "theme", "example.iapresenter", "assets"), 0755)
	os.WriteFile(filepath.Join(localDir, "theme", "example.iapresenter", "info.json"), []byte(`{"title":"test"}`), 0644)
	os.WriteFile(filepath.Join(localDir, "theme", "example.iapresenter", "text.md"), []byte("# Hello"), 0644)
	os.WriteFile(filepath.Join(localDir, "theme", "example.iapresenter", "assets", "logo.png"), []byte("png-data"), 0644)
	os.WriteFile(filepath.Join(localDir, "standalone.txt"), []byte("also untracked"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Empty log — these files were never seen by watcher or seed
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Root-level untracked file survives — file deletion loop only deletes
	// files with explicit delete ops, and root dir is never stale.
	if _, err := os.Stat(filepath.Join(localDir, "standalone.txt")); os.IsNotExist(err) {
		t.Error("root-level untracked file was deleted — reconciler must not delete without a delete op")
	}

	// Files inside stale directories are removed by os.RemoveAll in
	// reconcileDirectories. This is a known tradeoff: .DS_Store was blocking
	// directory cleanup on macOS (every Finder-browsed dir had one).
	for _, path := range []string{
		"theme/example.iapresenter/info.json",
		"theme/example.iapresenter/text.md",
		"theme/example.iapresenter/assets/logo.png",
	} {
		if _, err := os.Stat(filepath.Join(localDir, path)); !os.IsNotExist(err) {
			t.Errorf("file %q in stale dir survived — os.RemoveAll should have cleaned it", path)
		}
	}
}

func TestReconcilerDeletesOnRemoteDeleteOp(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// File on disk — was synced previously
	os.WriteFile(filepath.Join(localDir, "report.txt"), []byte("old content"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")

	// Log has: put from dev-remote, then delete from dev-remote.
	// The CRDT resolves this as "file deleted."
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "report.txt",
		Checksum:  checksumOf("old content"),
		Device:    "dev-remote",
		Timestamp: 1000,
		Seq:       1,
		Namespace: "Test",
	})
	localLog.Append(opslog.Entry{
		Type:      opslog.Delete,
		Path:      "report.txt",
		Device:    "dev-remote",
		Timestamp: 2000,
		Seq:       2,
		Namespace: "Test",
	})

	// Verify snapshot shows file as deleted
	snap, _ := localLog.Snapshot()
	if _, exists := snap.Files()["report.txt"]; exists {
		t.Fatal("snapshot should not contain report.txt after delete op")
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// File SHOULD be deleted — there's an explicit delete op
	if _, err := os.Stat(filepath.Join(localDir, "report.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted — remote delete op exists in log")
	}
}

func checksumOf(content string) string {
	h := sha3.New256()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

// flakyBackend wraps MemoryBackend to simulate transient download failures.
type flakyBackend struct {
	*s3adapter.MemoryBackend
	mu       sync.Mutex
	failKeys map[string]int // blob key → remaining failure count
}

func (f *flakyBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	if n := f.failKeys[key]; n > 0 {
		f.failKeys[key] = n - 1
		if n == 1 {
			delete(f.failKeys, key)
		}
		f.mu.Unlock()
		return nil, fmt.Errorf("transient S3 error")
	}
	f.mu.Unlock()
	return f.MemoryBackend.Get(ctx, key)
}

func TestReconcilerRetriesFailedDownloads(t *testing.T) {
	t.Parallel()

	mem := s3adapter.NewMemory()
	flaky := &flakyBackend{MemoryBackend: mem, failKeys: make(map[string]int)}
	id, _ := GenerateDeviceKey()
	store := New(flaky, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")

	// Upload blobs and record entries in the local log.
	putAndLog(t, store, localLog, "a.txt", "aaa", 1)
	putAndLog(t, store, localLog, "b.txt", "bbb", 2)
	putAndLog(t, store, localLog, "c.txt", "ccc", 3)

	// Mark b.txt's blob for one transient failure by reading the snapshot.
	snap, _ := localLog.Snapshot()
	bInfo, _ := snap.Lookup("b.txt")
	if len(bInfo.Chunks) > 0 {
		blobKey := (&Chunk{Hash: bInfo.Chunks[0]}).BlobKey()
		flaky.mu.Lock()
		flaky.failKeys[blobKey] = 1
		flaky.mu.Unlock()
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)

	// Run reconciler with timeout — no external pokes.
	// With the bug: b.txt fails on first pass, reconciler stops, test times out.
	// With the fix: reconciler retries, b.txt succeeds on second pass.
	ctx := context.Background()
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		r.Run(runCtx)
		close(done)
	}()

	// Poll for all 3 files
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(4 * time.Second)

	for {
		select {
		case <-deadline:
			cancel()
			<-done
			_, aErr := os.Stat(filepath.Join(localDir, "a.txt"))
			_, bErr := os.Stat(filepath.Join(localDir, "b.txt"))
			_, cErr := os.Stat(filepath.Join(localDir, "c.txt"))
			t.Fatalf("timed out: a=%v b=%v c=%v", aErr == nil, bErr == nil, cErr == nil)
		case <-tick.C:
			aOK := fileExistsAt(filepath.Join(localDir, "a.txt"))
			bOK := fileExistsAt(filepath.Join(localDir, "b.txt"))
			cOK := fileExistsAt(filepath.Join(localDir, "c.txt"))
			if aOK && bOK && cOK {
				cancel()
				<-done
				data, _ := os.ReadFile(filepath.Join(localDir, "b.txt"))
				if string(data) != "bbb" {
					t.Errorf("b.txt content = %q, want %q", string(data), "bbb")
				}
				return
			}
		}
	}
}

func fileExistsAt(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Regression: when a remote device uploads a chunkless Put (e.g. AppendLocal
// leaked to S3 before outbox worker confirmed chunks), the reconciler must
// treat it as "pending" — not as a download failure. Counting it as failed
// triggers a 2-second retry poke that spins forever because chunks will
// never appear on their own.
func TestReconcilerSkipsChunklessRemoteEntries(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	// Remote device has a chunkless Put — upload pending on their side.
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "pending-upload.txt",
		Checksum:  "abc123",
		Size:      100,
		Namespace: "Test",
		Device:    "dev-remote",
		Timestamp: 1000,
		Seq:       1,
		// Chunks intentionally nil — this is the chunkless op
	})

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)

	// Run reconciliation directly.
	// The chunkless entry must NOT count as failed.
	ctx := context.Background()
	r.reconcile(ctx)

	// The chunkless file should NOT be on disk (no chunks to download)
	if _, err := os.Stat(filepath.Join(localDir, "pending-upload.txt")); err == nil {
		t.Error("pending-upload.txt should NOT be on disk — no chunks available")
	}

	// Wait for any retry goroutine to fire (the bug: 2-second retry poke)
	time.Sleep(3 * time.Second)

	// Drain the notify channel — with the bug, a retry poke is queued.
	select {
	case <-r.notify:
		t.Error("chunkless entry should NOT trigger retry poke")
	default:
		// good — no poke
	}
}

// Regression: chunkless Put entries should not count toward the failed
// counter in reconcile(), which means failed==0 and no retry poke fires.
// This specifically tests the case where ALL remote entries are chunkless.
func TestReconcilerChunklessOnlyNoRetryPoke(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	// 5 chunkless remote Puts — all pending uploads on source machine
	for i := 0; i < 5; i++ {
		localLog.Append(opslog.Entry{
			Type:      opslog.Put,
			Path:      fmt.Sprintf("pending-%d.txt", i),
			Checksum:  fmt.Sprintf("hash%d", i),
			Size:      100,
			Namespace: "Test",
			Device:    "dev-remote",
			Timestamp: int64(1000 + i),
			Seq:       i + 1,
		})
	}

	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)

	// Run reconciler in a goroutine with Run() — if retry pokes fire,
	// it will keep reconciling in a tight loop.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	reconcileCount := 0
	r.onEvent = func(evt string, _ map[string]any) {
		if evt == "sync.complete" || evt == "download.start" {
			reconcileCount++
		}
	}

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	<-ctx.Done()
	<-done

	// With the bug: reconciler retries every 2 seconds → reconcileCount >= 2
	// With the fix: reconciler runs once on startup, no retries → reconcileCount <= 1
	if reconcileCount > 1 {
		t.Errorf("reconciler ran %d times in 4s — chunkless entries are causing retry spin", reconcileCount)
	}
}
