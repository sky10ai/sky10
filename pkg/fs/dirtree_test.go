package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirHashEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	h1, err := DirHash(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == "" {
		t.Fatal("hash should not be empty")
	}

	// Another empty dir should produce the same hash
	dir2 := t.TempDir()
	h2, err := DirHash(dir2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("two empty dirs should have same hash: %s != %s", h1, h2)
	}
}

func TestDirHashSingleFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0644)

	h, err := DirHash(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Fatal("hash should not be empty")
	}

	// Same content in a different temp dir should match
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "hello.txt"), []byte("hello"), 0644)

	h2, err := DirHash(dir2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h != h2 {
		t.Errorf("identical dirs should have same hash: %s != %s", h, h2)
	}
}

func TestDirHashDifferentContent(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "f.txt"), []byte("aaa"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "f.txt"), []byte("bbb"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 == h2 {
		t.Error("different content should produce different hashes")
	}
}

func TestDirHashDifferentName(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("same"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "b.txt"), []byte("same"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 == h2 {
		t.Error("different filenames should produce different hashes")
	}
}

func TestDirHashOrderIndependent(t *testing.T) {
	t.Parallel()
	// Create files in different order — hash should be the same
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir1, "b.txt"), []byte("bbb"), 0644)
	os.WriteFile(filepath.Join(dir1, "c.txt"), []byte("ccc"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "c.txt"), []byte("ccc"), 0644)
	os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir2, "b.txt"), []byte("bbb"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 != h2 {
		t.Errorf("creation order should not affect hash: %s != %s", h1, h2)
	}
}

func TestDirHashNestedDirs(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.MkdirAll(filepath.Join(dir1, "sub", "deep"), 0755)
	os.WriteFile(filepath.Join(dir1, "root.txt"), []byte("root"), 0644)
	os.WriteFile(filepath.Join(dir1, "sub", "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir1, "sub", "deep", "b.txt"), []byte("bbb"), 0644)

	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "sub", "deep"), 0755)
	os.WriteFile(filepath.Join(dir2, "root.txt"), []byte("root"), 0644)
	os.WriteFile(filepath.Join(dir2, "sub", "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir2, "sub", "deep", "b.txt"), []byte("bbb"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 != h2 {
		t.Errorf("identical nested dirs should match: %s != %s", h1, h2)
	}

	// Change a deeply nested file — root hash should change
	os.WriteFile(filepath.Join(dir2, "sub", "deep", "b.txt"), []byte("changed"), 0644)
	h3, _ := DirHash(dir2, nil)
	if h1 == h3 {
		t.Error("changing a nested file should change the root hash")
	}
}

func TestDirHashEmptySubdir(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "f.txt"), []byte("data"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "f.txt"), []byte("data"), 0644)
	os.MkdirAll(filepath.Join(dir2, "emptydir"), 0755)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 == h2 {
		t.Error("adding an empty dir should change the hash")
	}
}

func TestDirHashSkipsDotfiles(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "visible.txt"), []byte("data"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "visible.txt"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir2, ".hidden"), []byte("secret"), 0644)
	os.MkdirAll(filepath.Join(dir2, ".git"), 0755)
	os.WriteFile(filepath.Join(dir2, ".git", "HEAD"), []byte("ref"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 != h2 {
		t.Errorf("dotfiles should be ignored: %s != %s", h1, h2)
	}
}

func TestDirHashIgnoreFunc(t *testing.T) {
	t.Parallel()
	ignore := func(path string) bool {
		return path == "ignored.txt" || path == "skipdir"
	}

	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "keep.txt"), []byte("data"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "keep.txt"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir2, "ignored.txt"), []byte("noise"), 0644)
	os.MkdirAll(filepath.Join(dir2, "skipdir"), 0755)
	os.WriteFile(filepath.Join(dir2, "skipdir", "x.txt"), []byte("x"), 0644)

	h1, _ := DirHash(dir1, ignore)
	h2, _ := DirHash(dir2, ignore)
	if h1 != h2 {
		t.Errorf("ignored files should not affect hash: %s != %s", h1, h2)
	}
}

func TestDirHashExtraFileChangesHash(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("aaa"), 0644)

	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir2, "b.txt"), []byte("bbb"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 == h2 {
		t.Error("adding a file should change the hash")
	}
}

func TestDirTreeReturnsPerPathHashes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "child.txt"), []byte("child"), 0644)

	tree, err := DirTree(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have entries for: ".", "root.txt", "sub", "sub/child.txt"
	for _, key := range []string{".", "root.txt", "sub", "sub/child.txt"} {
		if _, ok := tree[key]; !ok {
			t.Errorf("tree missing key %q", key)
		}
	}

	// Root hash from DirTree should match DirHash
	rootHash, _ := DirHash(dir, nil)
	if tree["."] != rootHash {
		t.Errorf("tree root %q != DirHash %q", tree["."], rootHash)
	}
}

func TestDirTreeSubtreeHashIsolated(t *testing.T) {
	t.Parallel()
	// Two dirs with identical "sub/" but different root files
	dir1 := t.TempDir()
	os.MkdirAll(filepath.Join(dir1, "sub"), 0755)
	os.WriteFile(filepath.Join(dir1, "different.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir1, "sub", "same.txt"), []byte("shared"), 0644)

	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "sub"), 0755)
	os.WriteFile(filepath.Join(dir2, "different.txt"), []byte("bbb"), 0644)
	os.WriteFile(filepath.Join(dir2, "sub", "same.txt"), []byte("shared"), 0644)

	tree1, _ := DirTree(dir1, nil)
	tree2, _ := DirTree(dir2, nil)

	// Root hashes should differ
	if tree1["."] == tree2["."] {
		t.Error("root hashes should differ")
	}

	// Sub-directory hashes should match (identical subtree)
	if tree1["sub"] != tree2["sub"] {
		t.Errorf("sub/ hashes should match: %s != %s", tree1["sub"], tree2["sub"])
	}
}

func TestDirHashDeterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a"), 0755)
	os.MkdirAll(filepath.Join(dir, "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "1.txt"), []byte("one"), 0644)
	os.WriteFile(filepath.Join(dir, "b", "2.txt"), []byte("two"), 0644)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0644)

	// Hash the same dir 10 times — must always be the same
	first, _ := DirHash(dir, nil)
	for i := 0; i < 10; i++ {
		h, _ := DirHash(dir, nil)
		if h != first {
			t.Fatalf("run %d: hash changed: %s != %s", i, h, first)
		}
	}
}

func TestDirHashMovedFileChangesHash(t *testing.T) {
	t.Parallel()
	// Same file content but in different directories
	dir1 := t.TempDir()
	os.MkdirAll(filepath.Join(dir1, "a"), 0755)
	os.WriteFile(filepath.Join(dir1, "a", "f.txt"), []byte("data"), 0644)

	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "b"), 0755)
	os.WriteFile(filepath.Join(dir2, "b", "f.txt"), []byte("data"), 0644)

	h1, _ := DirHash(dir1, nil)
	h2, _ := DirHash(dir2, nil)
	if h1 == h2 {
		t.Error("file in different directory should produce different root hash")
	}
}
