package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestWatcherHandlerCreate(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create a file
	os.WriteFile(filepath.Join(localDir, "new.txt"), []byte("hello"), 0644)

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "new.txt", Type: FileCreated}})

	// Outbox should have 1 entry
	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpPut || entries[0].Path != "new.txt" || entries[0].Namespace != "Test" {
		t.Errorf("entry: %+v", entries[0])
	}
	if entries[0].LocalPath != filepath.Join(localDir, "new.txt") {
		t.Errorf("local_path = %q", entries[0].LocalPath)
	}

	// Upload-then-record: local log should NOT have the entry yet.
	// The outbox worker writes it after upload succeeds.
	if _, ok := localLog.Lookup("new.txt"); ok {
		t.Error("local log should not have new.txt before outbox drain")
	}
}

func TestWatcherHandlerModify(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create then modify
	os.WriteFile(filepath.Join(localDir, "doc.txt"), []byte("v1"), 0644)
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "doc.txt", Type: FileCreated}})

	os.WriteFile(filepath.Join(localDir, "doc.txt"), []byte("v2 changed"), 0644)
	handler.HandleEvents([]FileEvent{{Path: "doc.txt", Type: FileModified}})

	// Both events go to outbox (dedup check won't find first in local log,
	// so both are queued — outbox worker handles idempotently).
	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("outbox has %d, want 2", len(entries))
	}
}

func TestWatcherHandlerSkipsUnstableRecentFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	target := filepath.Join(localDir, "large.txt")
	if err := os.WriteFile(target, []byte("still writing"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.stableWriteWindow = 2 * time.Second
	handler.HandleEvents([]FileEvent{{Path: "large.txt", Type: FileCreated}})

	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d entries, want 0 for unstable file", outbox.Len())
	}

	old := time.Now().Add(-3 * time.Second)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	handler.HandleEvents([]FileEvent{{Path: "large.txt", Type: FileModified}})

	entries, err := outbox.ReadAll()
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox has %d entries, want 1 after file settles", len(entries))
	}
}

func TestWatcherHandlerSkipsConflictCopyCreate(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	conflictPath := filepath.Join(localDir, "doc.conflict-dev123-1711700000.txt")
	if err := os.WriteFile(conflictPath, []byte("loser"), 0644); err != nil {
		t.Fatalf("write conflict copy: %v", err)
	}

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "doc.conflict-dev123-1711700000.txt", Type: FileCreated}})

	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d entries, want 0 for conflict copy", outbox.Len())
	}
}

func TestWatcherHandlerSkipsInvalidLogicalPath(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: `folder\name.txt`, Type: FileCreated}})

	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d entries, want 0 for invalid logical path", outbox.Len())
	}
}

func TestWatcherHandlerSkipsWindowsCaseCollisionAgainstSnapshot(t *testing.T) {
	withWindowsPathPolicy(t, true)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(filepath.Join(localDir, "docs"), 0755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "Docs/Readme.md", Checksum: "remote", Namespace: "Test"})

	if err := os.WriteFile(filepath.Join(localDir, "docs", "readme.md"), []byte("local"), 0644); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "docs/readme.md", Type: FileCreated}})

	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d entries, want 0 for Windows case collision", outbox.Len())
	}
}

func TestWatcherHandlerSkipsWindowsCaseCollisionAgainstPendingOutbox(t *testing.T) {
	withWindowsPathPolicy(t, true)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(filepath.Join(localDir, "docs"), 0755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")
	if err := outbox.Append(NewOutboxPut("Docs/Readme.md", "aaa", "Test", filepath.Join(localDir, "Docs", "Readme.md"))); err != nil {
		t.Fatalf("append outbox: %v", err)
	}

	if err := os.WriteFile(filepath.Join(localDir, "docs", "readme.md"), []byte("local"), 0644); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "docs/readme.md", Type: FileCreated}})

	entries, err := outbox.ReadAll()
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox has %d entries, want 1 existing entry only", len(entries))
	}
}

func TestWatcherHandlerSkipsWindowsCaseCollisionWithinBatch(t *testing.T) {
	withWindowsPathPolicy(t, true)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("mkdir sync: %v", err)
	}

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{
		{Path: "Docs", Type: DirCreated},
		{Path: "docs", Type: DirCreated},
	})

	if outbox.Len() != 0 {
		t.Fatalf("outbox has %d entries, want 0 for colliding batch paths", outbox.Len())
	}
}

func TestWatcherHandlerDelete(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Simulate a file that was previously uploaded (in local log).
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "bye.txt", Checksum: "abc123", Namespace: "Test",
	})

	os.WriteFile(filepath.Join(localDir, "bye.txt"), []byte("goodbye"), 0644)
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	os.Remove(filepath.Join(localDir, "bye.txt"))
	handler.HandleEvents([]FileEvent{{Path: "bye.txt", Type: FileDeleted}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpDelete {
		t.Errorf("entry: %+v", entries[0])
	}
	if entries[0].Checksum == "" {
		t.Error("delete entry should have checksum from local log")
	}
}

func TestWatcherHandlerSkipsUnchanged(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	os.WriteFile(filepath.Join(localDir, "same.txt"), []byte("same"), 0644)
	cksum, _ := fileChecksum(filepath.Join(localDir, "same.txt"))

	// Simulate the file already being in the local log (previously uploaded).
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "same.txt", Checksum: cksum, Namespace: "Test",
	})

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	// Event for same content — should skip (dedup check finds it in local log).
	handler.HandleEvents([]FileEvent{{Path: "same.txt", Type: FileModified}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 0 {
		t.Errorf("outbox has %d, want 0 (skip unchanged)", len(entries))
	}
}

func TestWatcherHandlerDeleteUntracked(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	// Delete a file that was never tracked
	handler.HandleEvents([]FileEvent{{Path: "ghost.txt", Type: FileDeleted}})

	if outbox.Len() != 0 {
		t.Error("should not write outbox entry for untracked file")
	}
}

func TestWatcherHandlerDirectoryTrash(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Simulate files that were in a subdirectory by recording them in the local log.
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "subdir/a.txt", Checksum: "aaa", Namespace: "Test"})
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "subdir/b.txt", Checksum: "bbb", Namespace: "Test"})
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "other/c.txt", Checksum: "ccc", Namespace: "Test"})

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleDirectoryTrash("subdir")

	// Should emit one delete_dir op to outbox
	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1 (one delete_dir)", len(entries))
	}
	if entries[0].Op != OpDeleteDir || entries[0].Path != "subdir" {
		t.Errorf("expected delete_dir for subdir, got %+v", entries[0])
	}

	// Local log still has the entries — outbox worker will record the
	// delete_dir after processing. The CRDT applies delete_dir as a
	// prefix delete when the outbox worker writes it.
}

func TestWatcherHandlerDeleteDirectoryViaHandleEvents(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Simulate tracked files under a directory
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "archive/a.txt", Checksum: "aaa", Namespace: "Test"})
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "archive/sub/b.txt", Checksum: "bbb", Namespace: "Test"})
	localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "other/c.txt", Checksum: "ccc", Namespace: "Test"})

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	// kqueue sends a single FileDeleted for the directory, not individual files
	handler.HandleEvents([]FileEvent{{Path: "archive", Type: FileDeleted}})

	// Should emit one delete_dir op to outbox
	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1 (one delete_dir)", len(entries))
	}
	if entries[0].Op != OpDeleteDir || entries[0].Path != "archive" {
		t.Errorf("expected delete_dir for archive, got %+v", entries[0])
	}
}

func TestWatcherHandlerDirCreated(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "newdir", Type: DirCreated}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpCreateDir || entries[0].Path != "newdir" {
		t.Errorf("expected create_dir for newdir, got %+v", entries[0])
	}
}

// Regression: deleting an empty directory that was created via create_dir
// must emit a delete_dir op. Previously HandleDirectoryTrash only checked
// snap.Files() and silently ignored dirs with no files.
func TestWatcherHandlerDeleteEmptyCreatedDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create an empty directory (tracked via create_dir, no files)
	localLog.AppendLocal(opslog.Entry{Type: opslog.CreateDir, Path: "empty", Namespace: "Test"})

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	// Simulate kqueue FileDeleted for the directory
	handler.HandleEvents([]FileEvent{{Path: "empty", Type: FileDeleted}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1 (delete_dir for empty dir)", len(entries))
	}
	if entries[0].Op != OpDeleteDir || entries[0].Path != "empty" {
		t.Errorf("expected delete_dir for empty, got %+v", entries[0])
	}
}

func TestWatcherHandlerSymlinkOutboxOnly(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create a real symlink
	target := filepath.Join(localDir, "target.txt")
	os.WriteFile(target, []byte("target"), 0644)
	link := filepath.Join(localDir, "link.txt")
	os.Symlink(target, link)

	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "link.txt", Type: SymlinkCreated}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("outbox has %d, want 1", len(entries))
	}
	if entries[0].Op != OpSymlink {
		t.Errorf("expected symlink op, got %+v", entries[0])
	}
	if entries[0].LinkTarget != target {
		t.Errorf("link target = %q, want %q", entries[0].LinkTarget, target)
	}
}
