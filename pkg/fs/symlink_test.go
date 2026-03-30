package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// --- Scan tests ---

func TestScanDirectoryDetectsSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Regular file
	os.WriteFile(filepath.Join(dir, "real.txt"), []byte("content"), 0644)

	// Symlink to file
	os.Symlink("real.txt", filepath.Join(dir, "link.txt"))

	files, symlinks, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	// Regular file in files, not in symlinks
	if _, ok := files["real.txt"]; !ok {
		t.Error("real.txt missing from files")
	}
	if _, ok := symlinks["real.txt"]; ok {
		t.Error("real.txt should not be in symlinks")
	}

	// Symlink in both files (with checksum) and symlinks (with target)
	if _, ok := files["link.txt"]; !ok {
		t.Error("link.txt missing from files (should have checksum)")
	}
	target, ok := symlinks["link.txt"]
	if !ok {
		t.Error("link.txt missing from symlinks")
	}
	if target != "real.txt" {
		t.Errorf("symlink target = %q, want %q", target, "real.txt")
	}
}

func TestScanDirectorySymlinkChecksum(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	os.Symlink("/some/target", filepath.Join(dir, "link"))

	files, _, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	got := files["link"]
	want := symlinkChecksum("/some/target")
	if got != want {
		t.Errorf("checksum = %q, want %q", got, want)
	}
}

func TestScanDirectoryDanglingSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Symlink to nonexistent target — still detected
	os.Symlink("/nonexistent/path", filepath.Join(dir, "dangling"))

	files, symlinks, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	if _, ok := files["dangling"]; !ok {
		t.Error("dangling symlink missing from files")
	}
	if target := symlinks["dangling"]; target != "/nonexistent/path" {
		t.Errorf("target = %q, want %q", target, "/nonexistent/path")
	}
}

func TestScanDirectorySymlinkToDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Real directory with a file
	os.MkdirAll(filepath.Join(dir, "realdir"), 0755)
	os.WriteFile(filepath.Join(dir, "realdir", "inside.txt"), []byte("x"), 0644)

	// Symlink to the directory — should NOT be followed
	os.Symlink("realdir", filepath.Join(dir, "linkdir"))

	files, symlinks, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	// realdir/inside.txt should be in files
	if _, ok := files["realdir/inside.txt"]; !ok {
		t.Error("realdir/inside.txt missing from files")
	}

	// linkdir should be in symlinks (not followed)
	if target := symlinks["linkdir"]; target != "realdir" {
		t.Errorf("linkdir target = %q, want %q", target, "realdir")
	}

	// linkdir/inside.txt should NOT appear — symlink not followed
	if _, ok := files["linkdir/inside.txt"]; ok {
		t.Error("linkdir/inside.txt should not appear — symlink should not be followed")
	}
}

func TestScanDirectoryMixedFilesAndSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0644)
	os.Symlink("a.txt", filepath.Join(dir, "link-a"))
	os.Symlink("/abs/path", filepath.Join(dir, "link-abs"))

	files, symlinks, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	if len(files) != 4 {
		t.Errorf("files count = %d, want 4 (2 regular + 2 symlinks)", len(files))
	}
	if len(symlinks) != 2 {
		t.Errorf("symlinks count = %d, want 2", len(symlinks))
	}

	if symlinks["link-a"] != "a.txt" {
		t.Errorf("link-a target = %q", symlinks["link-a"])
	}
	if symlinks["link-abs"] != "/abs/path" {
		t.Errorf("link-abs target = %q", symlinks["link-abs"])
	}
}

// --- CRDT / Snapshot tests ---

func TestBuildSnapshotSymlink(t *testing.T) {
	t.Parallel()
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")

	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "docs/link",
		Checksum:   symlinkChecksum("../README.md"),
		LinkTarget: "../README.md",
		Namespace:  "Test",
		Device:     "dev-b",
		Timestamp:  100,
		Seq:        1,
	})

	snap, err := localLog.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	fi, ok := snap.Lookup("docs/link")
	if !ok {
		t.Fatal("symlink not in snapshot")
	}
	if fi.LinkTarget != "../README.md" {
		t.Errorf("LinkTarget = %q, want %q", fi.LinkTarget, "../README.md")
	}
	if fi.Checksum != symlinkChecksum("../README.md") {
		t.Error("checksum mismatch")
	}
	if len(fi.Chunks) != 0 {
		t.Error("symlink should have no chunks")
	}
}

func TestBuildSnapshotSymlinkLWW(t *testing.T) {
	t.Parallel()
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")

	// First symlink
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link",
		LinkTarget: "old-target",
		Device:     "dev-a",
		Timestamp:  100,
		Seq:        1,
	})

	// Updated symlink with higher clock
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link",
		LinkTarget: "new-target",
		Device:     "dev-a",
		Timestamp:  200,
		Seq:        2,
	})

	snap, _ := localLog.Snapshot()
	fi, ok := snap.Lookup("link")
	if !ok {
		t.Fatal("symlink not in snapshot")
	}
	if fi.LinkTarget != "new-target" {
		t.Errorf("LWW failed: LinkTarget = %q, want %q", fi.LinkTarget, "new-target")
	}
}

func TestBuildSnapshotSymlinkThenDelete(t *testing.T) {
	t.Parallel()
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")

	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link",
		LinkTarget: "target",
		Device:     "dev-a",
		Timestamp:  100,
		Seq:        1,
	})
	localLog.Append(opslog.Entry{
		Type:      opslog.Delete,
		Path:      "link",
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       2,
	})

	snap, _ := localLog.Snapshot()
	if _, ok := snap.Lookup("link"); ok {
		t.Error("deleted symlink should not be in snapshot")
	}
	if !snap.DeletedFiles()["link"] {
		t.Error("link should be in deleted set")
	}
}

func TestBuildSnapshotFileToSymlink(t *testing.T) {
	t.Parallel()
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")

	// First: regular file
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "file",
		Chunks:    []string{"abc123"},
		Checksum:  "abc123",
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	})

	// Then: replaced by symlink (higher clock)
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "file",
		LinkTarget: "other",
		Checksum:   symlinkChecksum("other"),
		Device:     "dev-a",
		Timestamp:  200,
		Seq:        2,
	})

	snap, _ := localLog.Snapshot()
	fi, ok := snap.Lookup("file")
	if !ok {
		t.Fatal("entry missing from snapshot")
	}
	if fi.LinkTarget != "other" {
		t.Errorf("should be symlink, got LinkTarget = %q", fi.LinkTarget)
	}
	if len(fi.Chunks) != 0 {
		t.Error("symlink should have no chunks")
	}
}

func TestBuildSnapshotSymlinkToFile(t *testing.T) {
	t.Parallel()
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")

	// First: symlink
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "thing",
		LinkTarget: "target",
		Device:     "dev-a",
		Timestamp:  100,
		Seq:        1,
	})

	// Then: replaced by regular file (higher clock)
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "thing",
		Chunks:    []string{"hash1"},
		Checksum:  "hash1",
		Size:      42,
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       2,
	})

	snap, _ := localLog.Snapshot()
	fi, ok := snap.Lookup("thing")
	if !ok {
		t.Fatal("entry missing from snapshot")
	}
	if fi.LinkTarget != "" {
		t.Error("should be regular file, but has LinkTarget")
	}
	if fi.Checksum != "hash1" {
		t.Errorf("Checksum = %q, want %q", fi.Checksum, "hash1")
	}
}

// --- Reconciler tests ---

func TestReconcilerDownloadSymlink(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Snapshot has a symlink
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("real.txt"),
		LinkTarget: "real.txt",
		Namespace:  "Test",
		Device:     "dev-remote",
		Timestamp:  100,
		Seq:        1,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Symlink should be created on disk
	target, err := os.Readlink(filepath.Join(localDir, "link.txt"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if target != "real.txt" {
		t.Errorf("target = %q, want %q", target, "real.txt")
	}
}

func TestReconcilerSymlinkAlreadyCorrect(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create correct symlink on disk
	os.Symlink("target.txt", filepath.Join(localDir, "link.txt"))

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Namespace:  "Test",
		Device:     "dev-remote",
		Timestamp:  100,
		Seq:        1,
	})

	active := false
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.onEvent = func(string, map[string]any) { active = true }
	r.reconcile(context.Background())

	if active {
		t.Error("reconciler should not be active when symlink already matches")
	}
}

func TestReconcilerSymlinkTargetChanged(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Local symlink points to wrong target
	os.Symlink("old-target", filepath.Join(localDir, "link.txt"))

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("new-target"),
		LinkTarget: "new-target",
		Namespace:  "Test",
		Device:     "dev-remote",
		Timestamp:  100,
		Seq:        1,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	target, err := os.Readlink(filepath.Join(localDir, "link.txt"))
	if err != nil {
		t.Fatalf("symlink missing: %v", err)
	}
	if target != "new-target" {
		t.Errorf("target = %q, want %q", target, "new-target")
	}
}

func TestReconcilerDeleteSymlink(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Symlink on disk
	os.Symlink("target", filepath.Join(localDir, "link.txt"))

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Log has symlink then delete
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		LinkTarget: "target",
		Device:     "dev-remote",
		Timestamp:  100,
		Seq:        1,
	})
	localLog.Append(opslog.Entry{
		Type:      opslog.Delete,
		Path:      "link.txt",
		Device:    "dev-remote",
		Timestamp: 200,
		Seq:       2,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Symlink should be deleted — os.Remove works on symlinks
	if _, err := os.Lstat(filepath.Join(localDir, "link.txt")); !os.IsNotExist(err) {
		t.Error("symlink should have been deleted")
	}
}

func TestReconcilerFileReplacedBySymlink(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Regular file on disk
	os.WriteFile(filepath.Join(localDir, "thing"), []byte("file content"), 0644)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Snapshot: put then symlink (symlink wins with higher clock)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "thing",
		Checksum:  checksumOf("file content"),
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	})
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "thing",
		Checksum:   symlinkChecksum("link-target"),
		LinkTarget: "link-target",
		Device:     "dev-a",
		Timestamp:  200,
		Seq:        2,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Should now be a symlink, not a regular file
	target, err := os.Readlink(filepath.Join(localDir, "thing"))
	if err != nil {
		t.Fatalf("expected symlink, got error: %v", err)
	}
	if target != "link-target" {
		t.Errorf("target = %q, want %q", target, "link-target")
	}
}

func TestReconcilerSymlinkReplacedByFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Symlink on disk
	os.Symlink("old-target", filepath.Join(localDir, "thing"))

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	ctx := context.Background()

	// Put actual file content to create blobs
	if err := store.Put(ctx, "thing", strings.NewReader("new file content")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	pr := store.LastPutResult()

	// Build local log: symlink first, then replaced by file (higher clock)
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "thing",
		LinkTarget: "old-target",
		Checksum:   symlinkChecksum("old-target"),
		Device:     "dev-a",
		Timestamp:  100,
		Seq:        1,
	})
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "thing",
		Chunks:    pr.Chunks,
		Checksum:  pr.Checksum,
		Size:      pr.Size,
		Namespace: NamespaceFromPath("thing"),
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       2,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	// Should now be a regular file, not a symlink
	info, err := os.Lstat(filepath.Join(localDir, "thing"))
	if err != nil {
		t.Fatalf("file missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("should be regular file, not symlink")
	}
	data, _ := os.ReadFile(filepath.Join(localDir, "thing"))
	if string(data) != "new file content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestReconcilerSymlinkInSubdirectory(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Symlink in a subdirectory that doesn't exist yet
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "deep/nested/link",
		Checksum:   symlinkChecksum("../../target"),
		LinkTarget: "../../target",
		Namespace:  "Test",
		Device:     "dev-remote",
		Timestamp:  100,
		Seq:        1,
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(store, localLog, outbox, localDir, nil, nil)
	r.reconcile(context.Background())

	// Subdirectory should be created and symlink placed
	target, err := os.Readlink(filepath.Join(localDir, "deep", "nested", "link"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if target != "../../target" {
		t.Errorf("target = %q, want %q", target, "../../target")
	}
}

// --- WatcherHandler tests ---

func TestWatcherHandlerSymlinkCreated(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create symlink on disk
	os.Symlink("target.txt", filepath.Join(localDir, "link.txt"))

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	h := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	h.HandleEvents([]FileEvent{
		{Path: "link.txt", Type: SymlinkCreated},
	})

	// Check outbox has symlink entry
	entries, err := outbox.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox has %d entries, want 1", len(entries))
	}
	if entries[0].Op != OpSymlink {
		t.Errorf("op = %q, want %q", entries[0].Op, OpSymlink)
	}
	if entries[0].LinkTarget != "target.txt" {
		t.Errorf("link_target = %q, want %q", entries[0].LinkTarget, "target.txt")
	}
	if entries[0].Checksum != symlinkChecksum("target.txt") {
		t.Error("checksum mismatch")
	}

	// Upload-then-record: local log should NOT have the entry yet.
	// The outbox worker writes it after processing.
	if _, ok := localLog.Lookup("link.txt"); ok {
		t.Error("local log should not have link.txt before outbox drain")
	}
}

func TestWatcherHandlerSymlinkUnchanged(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	os.Symlink("target.txt", filepath.Join(localDir, "link.txt"))

	// Pre-populate local log with same symlink
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	localLog.AppendLocal(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Namespace:  "Test",
	})

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	h := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	h.HandleEvents([]FileEvent{
		{Path: "link.txt", Type: SymlinkCreated},
	})

	// Outbox should be empty — symlink unchanged
	entries, _ := outbox.ReadAll()
	if len(entries) != 0 {
		t.Errorf("outbox has %d entries, want 0 (unchanged symlink)", len(entries))
	}
}

// --- Outbox worker tests ---

func TestOutboxWorkerSymlink(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	outbox.Append(OutboxEntry{
		Op:         OpSymlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Namespace:  "Test",
		Timestamp:  100,
	})

	w := NewOutboxWorker(store, outbox, localLog, nil)
	ctx := context.Background()
	w.drain(ctx)

	// Outbox should be empty after drain
	entries, _ := outbox.ReadAll()
	if len(entries) != 0 {
		t.Errorf("outbox still has %d entries after drain", len(entries))
	}

	// Upload-then-record: symlink op should be in the local log
	fi, ok := localLog.Lookup("link.txt")
	if !ok {
		t.Fatal("symlink not in local log after outbox drain")
	}
	if fi.LinkTarget != "target.txt" {
		t.Errorf("local log LinkTarget = %q, want target.txt", fi.LinkTarget)
	}
}

// --- Integration tests (MinIO) ---

func TestSymlinkRoundTripMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	// Device A: create symlink op
	id, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, id, "dev-a")
	storeA.SetNamespace("shared")

	opA := &Op{
		Type:       OpSymlink,
		Path:       "docs/link",
		Checksum:   symlinkChecksum("../README.md"),
		LinkTarget: "../README.md",
		Namespace:  "shared",
	}
	if err := storeA.writeOp(ctx, opA); err != nil {
		t.Fatalf("writeOp: %v", err)
	}

	// Device B: same identity (shared key), different device ID — reads ops and reconciles.
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")

	logB, err := storeB.getOpsLog(ctx)
	if err != nil {
		t.Fatalf("getOpsLog: %v", err)
	}
	entries, err := logB.ReadSince(ctx, 0)
	if err != nil {
		t.Fatalf("ReadSince: %v", err)
	}

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	for _, e := range entries {
		localLog.Append(e)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(storeB, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	// Symlink should be on disk
	target, err := os.Readlink(filepath.Join(localDir, "docs", "link"))
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if target != "../README.md" {
		t.Errorf("target = %q, want %q", target, "../README.md")
	}
}

func TestSymlinkDanglingMinIO(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	if h == nil {
		return
	}

	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	id, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, id, "dev-a")
	storeA.SetNamespace("shared")

	// Dangling symlink — target doesn't exist
	op := &Op{
		Type:       OpSymlink,
		Path:       "broken-link",
		Checksum:   symlinkChecksum("/does/not/exist"),
		LinkTarget: "/does/not/exist",
		Namespace:  "shared",
	}
	if err := storeA.writeOp(ctx, op); err != nil {
		t.Fatalf("writeOp: %v", err)
	}

	// Reconcile on same-key device
	storeB := NewWithDevice(backend, id, "dev-b")
	storeB.SetNamespace("shared")

	logB, err := storeB.getOpsLog(ctx)
	if err != nil {
		t.Fatalf("getOpsLog: %v", err)
	}
	entries, err := logB.ReadSince(ctx, 0)
	if err != nil {
		t.Fatalf("ReadSince: %v", err)
	}

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-b")
	for _, e := range entries {
		localLog.Append(e)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	r := NewReconciler(storeB, localLog, outbox, localDir, nil, nil)
	r.reconcile(ctx)

	// Dangling symlink should still be created
	target, err := os.Readlink(filepath.Join(localDir, "broken-link"))
	if err != nil {
		t.Fatalf("dangling symlink not created: %v", err)
	}
	if target != "/does/not/exist" {
		t.Errorf("target = %q, want %q", target, "/does/not/exist")
	}
}

func TestSymlinkCompactRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Add a regular file and a symlink
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "file.txt",
		Chunks:    []string{"hash1"},
		Checksum:  "hash1",
		Size:      10,
		Namespace: "Test",
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	})
	localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("file.txt"),
		LinkTarget: "file.txt",
		Namespace:  "Test",
		Device:     "dev-a",
		Timestamp:  100,
		Seq:        2,
	})

	// Compact
	if err := localLog.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Force rebuild from file
	localLog.InvalidateCache()
	snap, err := localLog.Snapshot()
	if err != nil {
		t.Fatalf("snapshot after compact: %v", err)
	}

	// Regular file preserved
	fi, ok := snap.Lookup("file.txt")
	if !ok {
		t.Error("file.txt missing after compact")
	}
	if fi.LinkTarget != "" {
		t.Error("file.txt should not have LinkTarget")
	}

	// Symlink preserved
	fi, ok = snap.Lookup("link.txt")
	if !ok {
		t.Error("link.txt missing after compact")
	}
	if fi.LinkTarget != "file.txt" {
		t.Errorf("link.txt LinkTarget = %q after compact, want %q", fi.LinkTarget, "file.txt")
	}
}

// --- Seed tests ---

func TestSeedDetectsSymlinks(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create a regular file and a symlink
	os.WriteFile(filepath.Join(localDir, "real.txt"), []byte("content"), 0644)
	os.Symlink("real.txt", filepath.Join(localDir, "link.txt"))

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-local")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))

	// Seed: scan disk and populate outbox
	localFiles, localSymlinks, err := ScanDirectory(localDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	ns := "Test"
	for path, cksum := range localFiles {
		if target, isSymlink := localSymlinks[path]; isSymlink {
			localLog.AppendLocal(opslog.Entry{
				Type:       opslog.Symlink,
				Path:       path,
				Checksum:   cksum,
				LinkTarget: target,
				Namespace:  ns,
			})
			outbox.Append(OutboxEntry{
				Op:         OpSymlink,
				Path:       path,
				Checksum:   cksum,
				LinkTarget: target,
				Namespace:  ns,
			})
		} else {
			localPath := filepath.Join(localDir, filepath.FromSlash(path))
			info, _ := os.Stat(localPath)
			localLog.AppendLocal(opslog.Entry{
				Type:      opslog.Put,
				Path:      path,
				Checksum:  cksum,
				Size:      info.Size(),
				Namespace: ns,
			})
			outbox.Append(OutboxEntry{
				Op:        OpPut,
				Path:      path,
				Checksum:  cksum,
				Namespace: ns,
				LocalPath: localPath,
			})
		}
	}

	// Verify outbox
	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("outbox has %d entries, want 2", len(entries))
	}

	ops := make(map[string]OutboxEntry)
	for _, e := range entries {
		ops[e.Path] = e
	}

	if ops["real.txt"].Op != OpPut {
		t.Error("real.txt should be OpPut")
	}
	if ops["link.txt"].Op != OpSymlink {
		t.Error("link.txt should be OpSymlink")
	}
	if ops["link.txt"].LinkTarget != "real.txt" {
		t.Errorf("link.txt target = %q", ops["link.txt"].LinkTarget)
	}

	// Verify snapshot
	snap, _ := localLog.Snapshot()
	fi, ok := snap.Lookup("link.txt")
	if !ok {
		t.Fatal("link.txt missing from snapshot")
	}
	if fi.LinkTarget != "real.txt" {
		t.Errorf("snapshot LinkTarget = %q", fi.LinkTarget)
	}

	_ = backend
	_ = id
}
