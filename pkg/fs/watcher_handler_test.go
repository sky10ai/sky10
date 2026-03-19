package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatcherHandlerCreate(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Create a file
	os.WriteFile(filepath.Join(localDir, "new.txt"), []byte("hello"), 0644)

	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)
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

	// State should have the file
	fs, ok := state.GetFile("new.txt")
	if !ok {
		t.Fatal("state missing new.txt")
	}
	if fs.Namespace != "Test" {
		t.Errorf("namespace = %q", fs.Namespace)
	}
}

func TestWatcherHandlerModify(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Create then modify
	os.WriteFile(filepath.Join(localDir, "doc.txt"), []byte("v1"), 0644)
	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Create, track in state, then delete
	os.WriteFile(filepath.Join(localDir, "bye.txt"), []byte("goodbye"), 0644)
	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)
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
		t.Error("delete entry should have checksum from state")
	}

	// State should not have the file
	if _, ok := state.GetFile("bye.txt"); ok {
		t.Error("state should not have bye.txt")
	}
}

func TestWatcherHandlerSkipsUnchanged(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	outbox := NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl"))
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	os.WriteFile(filepath.Join(localDir, "same.txt"), []byte("same"), 0644)
	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)

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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)
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
	state := LoadDriveStateFromPath(filepath.Join(tmpDir, "state.json"))

	// Simulate files that were in a subdirectory
	state.SetFile("subdir/a.txt", FileState{Checksum: "aaa", Namespace: "Test"})
	state.SetFile("subdir/b.txt", FileState{Checksum: "bbb", Namespace: "Test"})
	state.SetFile("other/c.txt", FileState{Checksum: "ccc", Namespace: "Test"})

	handler := NewWatcherHandler(outbox, state, localDir, "Test", nil)
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

	// other/c.txt should still be in state
	if _, ok := state.GetFile("other/c.txt"); !ok {
		t.Error("other/c.txt should still be in state")
	}
}
