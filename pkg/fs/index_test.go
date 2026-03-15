package fs

import (
	"path/filepath"
	"testing"
	"time"
)

func TestIndexOpenClose(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")

	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}

	count, err := idx.FileCount()
	if err != nil {
		t.Fatalf("FileCount: %v", err)
	}
	if count != 0 {
		t.Errorf("new index should have 0 files, got %d", count)
	}

	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestIndexSyncFromManifest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, _ := OpenIndex(path)
	defer idx.Close()

	m := NewManifest()
	m.Set("journal/a.md", FileEntry{
		Chunks: []string{"c1", "c2"}, Size: 100,
		Modified: time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC),
		Checksum: "h1", Namespace: "journal",
	})
	m.Set("notes.md", FileEntry{
		Chunks: []string{"c3"}, Size: 50,
		Modified: time.Date(2026, 3, 14, 11, 0, 0, 0, time.UTC),
		Checksum: "h2", Namespace: "default",
	})

	if err := idx.SyncFromManifest(m); err != nil {
		t.Fatalf("SyncFromManifest: %v", err)
	}

	count, _ := idx.FileCount()
	if count != 2 {
		t.Errorf("FileCount = %d, want 2", count)
	}
}

func TestIndexListFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, _ := OpenIndex(path)
	defer idx.Close()

	m := NewManifest()
	m.Set("journal/a.md", FileEntry{Size: 10, Namespace: "journal"})
	m.Set("journal/b.md", FileEntry{Size: 20, Namespace: "journal"})
	m.Set("financial/report.md", FileEntry{Size: 30, Namespace: "financial"})
	idx.SyncFromManifest(m)

	tests := []struct {
		prefix string
		want   int
	}{
		{"journal/", 2},
		{"financial/", 1},
		{"", 3},
		{"nonexistent/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			entries, err := idx.ListFiles(tt.prefix)
			if err != nil {
				t.Fatalf("ListFiles(%q): %v", tt.prefix, err)
			}
			if len(entries) != tt.want {
				t.Errorf("ListFiles(%q) = %d entries, want %d", tt.prefix, len(entries), tt.want)
			}
		})
	}

	// Verify sorted
	entries, _ := idx.ListFiles("journal/")
	if len(entries) == 2 && entries[0].Path > entries[1].Path {
		t.Error("entries not sorted")
	}
}

func TestIndexLookupFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, _ := OpenIndex(path)
	defer idx.Close()

	m := NewManifest()
	m.Set("test.md", FileEntry{Chunks: []string{"c1"}, Size: 42, Checksum: "h1", Namespace: "default"})
	idx.SyncFromManifest(m)

	entry, err := idx.LookupFile("test.md")
	if err != nil {
		t.Fatalf("LookupFile: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Size != 42 {
		t.Errorf("size = %d, want 42", entry.Size)
	}
	if len(entry.Chunks) != 1 || entry.Chunks[0] != "c1" {
		t.Errorf("chunks = %v, want [c1]", entry.Chunks)
	}

	// Not found
	entry, err = idx.LookupFile("nonexistent.md")
	if err != nil {
		t.Fatalf("LookupFile nonexistent: %v", err)
	}
	if entry != nil {
		t.Error("expected nil for nonexistent file")
	}
}

func TestIndexSyncState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, _ := OpenIndex(path)
	defer idx.Close()

	// Set and get
	if err := idx.SetState("last_op_timestamp", "1707900000"); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	val, err := idx.GetState("last_op_timestamp")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if val != "1707900000" {
		t.Errorf("got %q, want %q", val, "1707900000")
	}

	// Missing key
	val, err = idx.GetState("nonexistent")
	if err != nil {
		t.Fatalf("GetState missing: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty for missing key, got %q", val)
	}

	// Overwrite
	idx.SetState("last_op_timestamp", "1707900100")
	val, _ = idx.GetState("last_op_timestamp")
	if val != "1707900100" {
		t.Errorf("after overwrite: got %q, want %q", val, "1707900100")
	}
}

func TestIndexPersistence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")

	// Write
	idx, _ := OpenIndex(path)
	m := NewManifest()
	m.Set("persist.md", FileEntry{Size: 99, Namespace: "default"})
	idx.SyncFromManifest(m)
	idx.SetState("device_id", "test-dev")
	idx.Close()

	// Reopen
	idx2, _ := OpenIndex(path)
	defer idx2.Close()

	count, _ := idx2.FileCount()
	if count != 1 {
		t.Errorf("after reopen: FileCount = %d, want 1", count)
	}

	val, _ := idx2.GetState("device_id")
	if val != "test-dev" {
		t.Errorf("after reopen: device_id = %q, want %q", val, "test-dev")
	}
}

func TestIndexResync(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "index.db")
	idx, _ := OpenIndex(path)
	defer idx.Close()

	// Initial sync
	m1 := NewManifest()
	m1.Set("a.md", FileEntry{Size: 10, Namespace: "default"})
	m1.Set("b.md", FileEntry{Size: 20, Namespace: "default"})
	idx.SyncFromManifest(m1)

	// Re-sync with different data (simulates remote changes)
	m2 := NewManifest()
	m2.Set("b.md", FileEntry{Size: 25, Namespace: "default"})
	m2.Set("c.md", FileEntry{Size: 30, Namespace: "default"})
	idx.SyncFromManifest(m2)

	count, _ := idx.FileCount()
	if count != 2 {
		t.Errorf("after resync: FileCount = %d, want 2", count)
	}

	// a.md should be gone
	entry, _ := idx.LookupFile("a.md")
	if entry != nil {
		t.Error("a.md should not exist after resync")
	}

	// b.md should be updated
	entry, _ = idx.LookupFile("b.md")
	if entry == nil || entry.Size != 25 {
		t.Error("b.md should be updated to size 25")
	}
}
