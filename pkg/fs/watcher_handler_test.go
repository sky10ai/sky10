package fs

import (
	"os"
	"path/filepath"
	"testing"

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

	// Local log should have the file
	fi, ok := localLog.Lookup("new.txt")
	if !ok {
		t.Fatal("local log missing new.txt")
	}
	if fi.Namespace != "Test" {
		t.Errorf("namespace = %q", fi.Namespace)
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

	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("outbox has %d, want 2", len(entries))
	}
}

func TestWatcherHandlerDelete(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "dev-a")

	// Create, track in local log, then delete
	os.WriteFile(filepath.Join(localDir, "bye.txt"), []byte("goodbye"), 0644)
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "bye.txt", Type: FileCreated}})

	os.Remove(filepath.Join(localDir, "bye.txt"))
	handler.HandleEvents([]FileEvent{{Path: "bye.txt", Type: FileDeleted}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("outbox has %d, want 2", len(entries))
	}
	if entries[1].Op != OpDelete {
		t.Errorf("second entry: %+v", entries[1])
	}
	if entries[1].Checksum == "" {
		t.Error("delete entry should have checksum from local log")
	}

	// Local log should show file as deleted
	if _, ok := localLog.Lookup("bye.txt"); ok {
		t.Error("local log should not have bye.txt after delete")
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
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)

	// First event — should write
	handler.HandleEvents([]FileEvent{{Path: "same.txt", Type: FileCreated}})
	// Second event, same content — should skip
	handler.HandleEvents([]FileEvent{{Path: "same.txt", Type: FileModified}})

	entries, _ := outbox.ReadAll()
	if len(entries) != 1 {
		t.Errorf("outbox has %d, want 1 (skip unchanged)", len(entries))
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

	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("outbox has %d, want 2 (only subdir files)", len(entries))
	}
	for _, e := range entries {
		if e.Op != OpDelete {
			t.Errorf("expected delete, got %+v", e)
		}
	}

	// other/c.txt should still be in local log
	if _, ok := localLog.Lookup("other/c.txt"); !ok {
		t.Error("other/c.txt should still be in local log")
	}
}

func TestWatcherHandlerOpsLogPersistence(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	opsPath := filepath.Join(tmpDir, "ops.jsonl")

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	localLog := opslog.NewLocalOpsLog(opsPath, "dev-a")

	os.WriteFile(filepath.Join(localDir, "persist.txt"), []byte("data"), 0644)
	handler := NewWatcherHandler(outbox, localLog, localDir, "Test", nil)
	handler.HandleEvents([]FileEvent{{Path: "persist.txt", Type: FileCreated}})

	// "Crash" — create new instance from same ops.jsonl
	localLog2 := opslog.NewLocalOpsLog(opsPath, "dev-a")
	fi, ok := localLog2.Lookup("persist.txt")
	if !ok {
		t.Fatal("persist.txt not recovered from ops.jsonl")
	}
	if fi.Checksum == "" {
		t.Error("recovered entry should have checksum")
	}
}
