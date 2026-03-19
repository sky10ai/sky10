package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDriveStateEmpty(t *testing.T) {
	t.Parallel()
	s := LoadDriveStateFromPath(filepath.Join(t.TempDir(), "nope.json"))
	if len(s.Files) != 0 {
		t.Errorf("expected empty, got %d", len(s.Files))
	}
	if s.LastRemoteOp != 0 {
		t.Errorf("expected 0, got %d", s.LastRemoteOp)
	}
}

func TestDriveStateSetGetRemove(t *testing.T) {
	t.Parallel()
	s := newDriveState("")

	s.SetFile("a.txt", FileState{Checksum: "aaa", Namespace: "Test"})
	s.SetFile("b.txt", FileState{Checksum: "bbb", Namespace: "Test"})

	f, ok := s.GetFile("a.txt")
	if !ok || f.Checksum != "aaa" || f.Namespace != "Test" {
		t.Errorf("got %+v", f)
	}

	s.RemoveFile("a.txt")
	_, ok = s.GetFile("a.txt")
	if ok {
		t.Error("a.txt should be removed")
	}
	_, ok = s.GetFile("b.txt")
	if !ok {
		t.Error("b.txt should still exist")
	}
}

func TestDriveStateSaveLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")

	s := newDriveState(path)
	s.SetFile("doc.txt", FileState{Checksum: "abc", Namespace: "ns1"})
	s.SetLastRemoteOp(12345)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2 := LoadDriveStateFromPath(path)
	if s2.LastRemoteOp != 12345 {
		t.Errorf("cursor = %d", s2.LastRemoteOp)
	}
	f, ok := s2.GetFile("doc.txt")
	if !ok || f.Checksum != "abc" || f.Namespace != "ns1" {
		t.Errorf("got %+v", f)
	}
}

func TestDriveStatePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")

	s := newDriveState(path)
	s.SetFile("x.txt", FileState{})
	s.Save()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("perm = %o, want 0600", perm)
	}
}

func TestDriveStateCreatesDir(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a", "b", "state.json")

	s := newDriveState(path)
	s.SetFile("x.txt", FileState{})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("should create nested dirs")
	}
}

func TestDriveStateCorrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte("not json"), 0600)

	s := LoadDriveStateFromPath(path)
	if len(s.Files) != 0 {
		t.Error("corrupt file should return empty state")
	}
}
