package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("aaa"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "b.md"), []byte("bbb"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0644)

	files, _, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	if len(files) != 3 {
		t.Errorf("got %d files, want 3 (dotfiles included)", len(files))
	}

	for _, want := range []string{"a.md", "sub/b.md", ".hidden"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
}

func TestDiffLocalRemote(t *testing.T) {
	t.Parallel()

	local := map[string]string{
		"new.md":       "hash-new",
		"modified.md":  "hash-local",
		"unchanged.md": "hash-same",
	}
	remote := map[string]FileEntry{
		"modified.md":  {Checksum: "hash-remote", Namespace: "default"},
		"unchanged.md": {Checksum: "hash-same", Namespace: "default"},
		"remote.md":    {Checksum: "hash-r", Namespace: "default"},
	}

	diffs := DiffLocalRemote(local, remote)

	types := make(map[string]DiffType)
	for _, d := range diffs {
		types[d.Path] = d.Type
	}

	if types["new.md"] != DiffUpload {
		t.Error("new.md should be upload")
	}
	if types["modified.md"] != DiffUpload {
		t.Error("modified.md should be upload (local wins)")
	}
	if _, ok := types["unchanged.md"]; ok {
		t.Error("unchanged.md should not appear in diff")
	}
	if types["remote.md"] != DiffDownload {
		t.Error("remote.md should be download")
	}
}
