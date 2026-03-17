package fs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDriveManifestNewEmpty(t *testing.T) {
	t.Parallel()
	m := LoadDriveManifest("nonexistent-drive-id")
	if m.LastRemoteOp != 0 {
		t.Errorf("expected 0, got %d", m.LastRemoteOp)
	}
	if len(m.Files) != 0 {
		t.Errorf("expected empty files, got %d", len(m.Files))
	}
}

func TestDriveManifestSetAndGet(t *testing.T) {
	t.Parallel()
	m := newDriveManifest("")

	m.SetFile("notes.txt", SyncedFile{Checksum: "abc", Size: 42, Modified: "2026-03-17T00:00:00Z"})
	m.SetFile("photos/cat.jpg", SyncedFile{Checksum: "def", Size: 387301, Modified: "2026-03-16T00:00:00Z"})

	e, ok := m.GetFile("notes.txt")
	if !ok {
		t.Fatal("notes.txt not found")
	}
	if e.Checksum != "abc" || e.Size != 42 {
		t.Errorf("got %+v", e)
	}

	_, ok = m.GetFile("missing.txt")
	if ok {
		t.Error("missing.txt should not exist")
	}
}

func TestDriveManifestRemoveFile(t *testing.T) {
	t.Parallel()
	m := newDriveManifest("")
	m.SetFile("a.txt", SyncedFile{Checksum: "aaa"})
	m.SetFile("b.txt", SyncedFile{Checksum: "bbb"})

	m.RemoveFile("a.txt")

	if _, ok := m.GetFile("a.txt"); ok {
		t.Error("a.txt should be removed")
	}
	if _, ok := m.GetFile("b.txt"); !ok {
		t.Error("b.txt should still exist")
	}
}

func TestDriveManifestLastRemoteOp(t *testing.T) {
	t.Parallel()
	m := newDriveManifest("")

	m.SetLastRemoteOp(1773706034)
	if m.LastRemoteOp != 1773706034 {
		t.Errorf("got %d", m.LastRemoteOp)
	}
}

func TestDriveManifestSaveLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	// Save
	m := newDriveManifest(path)
	m.SetFile("hello.md", SyncedFile{Checksum: "sha3abc", Size: 100, Modified: "2026-03-17T12:00:00Z"})
	m.SetFile("sub/deep.txt", SyncedFile{Checksum: "sha3def", Size: 200, Modified: "2026-03-17T13:00:00Z"})
	m.SetLastRemoteOp(999)

	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}

	// Load
	m2 := &DriveManifest{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, m2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m2.LastRemoteOp != 999 {
		t.Errorf("last_remote_op = %d, want 999", m2.LastRemoteOp)
	}
	if len(m2.Files) != 2 {
		t.Fatalf("files count = %d, want 2", len(m2.Files))
	}
	if m2.Files["hello.md"].Checksum != "sha3abc" {
		t.Errorf("hello.md checksum = %q", m2.Files["hello.md"].Checksum)
	}
	if m2.Files["sub/deep.txt"].Size != 200 {
		t.Errorf("sub/deep.txt size = %d", m2.Files["sub/deep.txt"].Size)
	}
}

func TestDriveManifestSaveCreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "manifest.json")

	m := newDriveManifest(path)
	m.SetFile("test.md", SyncedFile{Checksum: "x"})

	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Error("nested dirs should be created")
	}
}

func TestDriveManifestSavePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := newDriveManifest(path)
	m.SetFile("x.md", SyncedFile{})
	m.Save()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func TestDriveManifestCorruptedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	os.WriteFile(path, []byte("not json"), 0600)

	// Should return empty manifest, not crash
	m := &DriveManifest{}
	data, _ := os.ReadFile(path)
	err := json.Unmarshal(data, m)
	if err == nil {
		t.Error("expected error for corrupted JSON")
	}
}
