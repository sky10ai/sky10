package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncLogAppendAndReadAll(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	log := NewSyncLog[OutboxEntry](path)

	log.Append(NewOutboxPut("a.txt", "aaa", "Test", "/tmp/a.txt"))
	log.Append(NewOutboxPut("b.txt", "bbb", "Test", "/tmp/b.txt"))
	log.Append(NewOutboxDelete("c.txt", "ccc", "Test"))

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Path != "a.txt" || entries[0].Op != OpPut {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if entries[2].Path != "c.txt" || entries[2].Op != OpDelete {
		t.Errorf("entry 2: %+v", entries[2])
	}
}

func TestSyncLogReadAllEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	log := NewSyncLog[OutboxEntry](path)

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d, want 0", len(entries))
	}
}

func TestSyncLogRemove(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	log := NewSyncLog[OutboxEntry](path)

	log.Append(NewOutboxPut("keep.txt", "aaa", "Test", "/tmp/keep.txt"))
	log.Append(NewOutboxPut("remove.txt", "bbb", "Test", "/tmp/remove.txt"))
	log.Append(NewOutboxDelete("also-keep.txt", "ccc", "Test"))

	err := log.Remove(func(e OutboxEntry) bool {
		return e.Path == "remove.txt"
	})
	if err != nil {
		t.Fatal(err)
	}

	entries, _ := log.ReadAll()
	if len(entries) != 2 {
		t.Fatalf("got %d, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Path == "remove.txt" {
			t.Error("remove.txt should be gone")
		}
	}
}

func TestSyncLogLen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	log := NewSyncLog[OutboxEntry](path)

	if log.Len() != 0 {
		t.Errorf("empty log len = %d", log.Len())
	}

	log.Append(NewOutboxPut("a.txt", "aaa", "Test", "/tmp/a.txt"))
	log.Append(NewOutboxPut("b.txt", "bbb", "Test", "/tmp/b.txt"))

	if log.Len() != 2 {
		t.Errorf("len = %d, want 2", log.Len())
	}
}

func TestSyncLogClear(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	log := NewSyncLog[OutboxEntry](path)

	log.Append(NewOutboxPut("a.txt", "aaa", "Test", "/tmp/a.txt"))
	log.Clear()

	if log.Len() != 0 {
		t.Errorf("len after clear = %d", log.Len())
	}
}

func TestSyncLogSurvivesReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "persist.jsonl")

	// Write with one instance
	log1 := NewSyncLog[OutboxEntry](path)
	log1.Append(NewOutboxPut("survive.txt", "aaa", "Test", "/tmp/survive.txt"))

	// Read with a new instance (simulates restart)
	log2 := NewSyncLog[OutboxEntry](path)
	entries, _ := log2.ReadAll()
	if len(entries) != 1 {
		t.Fatalf("got %d, want 1 after reopen", len(entries))
	}
	if entries[0].Path != "survive.txt" {
		t.Errorf("path = %q", entries[0].Path)
	}
}

func TestSyncLogCorruptLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "corrupt.jsonl")

	// Write a valid entry, then corrupt data, then another valid entry
	f, _ := os.Create(path)
	f.WriteString(`{"op":"put","path":"good1.txt","ts":1}` + "\n")
	f.WriteString("not json garbage\n")
	f.WriteString(`{"op":"delete","path":"good2.txt","ts":2}` + "\n")
	f.Close()

	log := NewSyncLog[OutboxEntry](path)
	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	// Should skip corrupt line, return 2 valid entries
	if len(entries) != 2 {
		t.Fatalf("got %d, want 2 (skip corrupt)", len(entries))
	}
}
