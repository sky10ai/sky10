package skyfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreDefaults(t *testing.T) {
	t.Parallel()

	m := NewIgnoreMatcher(t.TempDir())

	tests := []struct {
		path string
		want bool
	}{
		{".git/config", true},
		{".DS_Store", true},
		{"Thumbs.db", true},
		{"file.swp", true},
		{"file~", true},
		{"file.tmp", true},
		{"normal.md", false},
		{"docs/readme.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			got := m.Matches(tt.path)
			if got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIgnoreCustomPatterns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".skyfsignore"), []byte("build/\n*.log\n!important.log\n"), 0644)

	m := NewIgnoreMatcher(dir)

	tests := []struct {
		path string
		want bool
	}{
		{"build/output.bin", true},
		{"app.log", true},
		{"important.log", false}, // negated
		{"normal.md", false},
		{".git/HEAD", true}, // default
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			got := m.Matches(tt.path)
			if got != tt.want {
				t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIgnoreNoFile(t *testing.T) {
	t.Parallel()

	// No .skyfsignore — only defaults
	m := NewIgnoreMatcher(t.TempDir())

	if m.Matches("normal.md") {
		t.Error("normal.md should not be ignored")
	}
	if !m.Matches(".DS_Store") {
		t.Error(".DS_Store should be ignored")
	}
}
