package fs

import (
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
	r.onEvent = func(string, map[string]any) { active = true }
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

	ctx := context.Background()
	store.Put(ctx, "a.txt", strings.NewReader("aaa"))
	store.Put(ctx, "b.txt", strings.NewReader("bbb"))
	store.Put(ctx, "c.txt", strings.NewReader("ccc"))

	entries := getOpsEntries(t, store)

	// Mark b.txt's blob for one transient failure
	for _, e := range entries {
		if e.Path == "b.txt" && len(e.Chunks) > 0 {
			blobKey := (&Chunk{Hash: e.Chunks[0]}).BlobKey()
			flaky.mu.Lock()
			flaky.failKeys[blobKey] = 1
			flaky.mu.Unlock()
		}
	}

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	for _, e := range entries {
		localLog.Append(e)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)

	// Run reconciler with timeout — no external pokes.
	// With the bug: b.txt fails on first pass, reconciler stops, test times out.
	// With the fix: reconciler retries, b.txt succeeds on second pass.
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
