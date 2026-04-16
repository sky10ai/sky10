package fs

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeLogicalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: `docs\readme.md`, want: "docs/readme.md", ok: true},
		{in: "docs/readme.md", want: "docs/readme.md", ok: true},
		{in: `nested\deep\file.txt`, want: "nested/deep/file.txt", ok: true},
		{in: "", ok: false},
		{in: ".", ok: false},
		{in: "../escape.txt", ok: false},
		{in: `..\escape.txt`, ok: false},
		{in: "/absolute.txt", ok: false},
		{in: `C:\temp\file.txt`, ok: false},
		{in: "docs//double.txt", ok: false},
		{in: "docs/./same.txt", ok: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeLogicalPath(tt.in)
			if tt.ok {
				if err != nil {
					t.Fatalf("NormalizeLogicalPath(%q) error = %v", tt.in, err)
				}
				if got != tt.want {
					t.Fatalf("NormalizeLogicalPath(%q) = %q, want %q", tt.in, got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("NormalizeLogicalPath(%q) unexpectedly succeeded with %q", tt.in, got)
			}
		})
	}
}

func TestLocalPathToLogicalRejectsUnixBackslashFilename(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("backslash is a path separator on Windows")
	}

	root := t.TempDir()
	local := filepath.Join(root, `folder\name.txt`)
	if _, err := LocalPathToLogical(root, local); err == nil {
		t.Fatal("LocalPathToLogical accepted a local filename containing backslash")
	}
}

func TestLogicalPathToLocal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := LogicalPathToLocal(root, "docs/readme.md")
	if err != nil {
		t.Fatalf("LogicalPathToLocal() error = %v", err)
	}

	want := filepath.Join(root, "docs", "readme.md")
	if got != want {
		t.Fatalf("LogicalPathToLocal() = %q, want %q", got, want)
	}
}

func TestValidateWindowsLogicalPath(t *testing.T) {
	t.Parallel()

	valid := []string{
		"docs/readme.md",
		"Agents/claude-code/sky10.md",
	}
	for _, path := range valid {
		if err := ValidateWindowsLogicalPath(path); err != nil {
			t.Fatalf("ValidateWindowsLogicalPath(%q) error = %v", path, err)
		}
	}

	invalid := []string{
		"CON.txt",
		"docs/trailing-dot.",
		"docs/trailing-space ",
		"docs/name:stream.txt",
		"docs/bad?.txt",
		"LPT1/report.md",
	}
	for _, path := range invalid {
		if err := ValidateWindowsLogicalPath(path); err == nil {
			t.Fatalf("ValidateWindowsLogicalPath(%q) unexpectedly succeeded", path)
		}
	}
}
