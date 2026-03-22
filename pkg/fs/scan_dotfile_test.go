package fs

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression test: dotfiles must be scanned and synced like any other file.
// Only .git directories should be skipped (via the ignore function).
func TestScanDirectoryIncludesDotfiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log"), 0644)
	os.MkdirAll(filepath.Join(dir, ".config"), 0755)
	os.WriteFile(filepath.Join(dir, ".config", "settings.json"), []byte("{}"), 0644)

	files, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	want := []string{"readme.md", ".env", ".gitignore", ".config/settings.json"}
	for _, path := range want {
		if _, ok := files[path]; !ok {
			t.Errorf("missing %s — dotfiles must be scanned", path)
		}
	}
}
