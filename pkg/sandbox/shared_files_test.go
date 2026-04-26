package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestPrepareTemplateSharedDirWritesInitialFiles(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	sharedDir := filepath.Join(t.TempDir(), "shared")
	rec := Record{
		Name:      "media-accent-agent",
		Slug:      "media-accent-agent",
		Provider:  providerLima,
		Template:  templateUbuntu,
		SharedDir: sharedDir,
		Files: []SharedFile{
			{Path: "agent-manifest.json", Mode: "0644", Content: "{}\n"},
			{Path: "nested/compose.yaml", Content: "services: {}\n"},
		},
	}

	if err := m.prepareTemplateSharedDir(context.Background(), rec); err != nil {
		t.Fatalf("prepareTemplateSharedDir() error: %v", err)
	}
	for path, want := range map[string]string{
		"agent-manifest.json": "{}",
		"nested/compose.yaml": "services: {}",
	} {
		data, err := os.ReadFile(filepath.Join(sharedDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", path, err)
		}
		if strings.TrimSpace(string(data)) != want {
			t.Fatalf("%s = %q, want %q", path, string(data), want)
		}
	}
}

func TestNormalizeSharedFilesRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"", ".", "../escape", "/tmp/escape"} {
		t.Run(path, func(t *testing.T) {
			if _, err := normalizeSharedFiles([]SharedFile{{Path: path, Content: "x"}}); err == nil {
				t.Fatalf("normalizeSharedFiles(%q) error = nil, want error", path)
			}
		})
	}
}

func TestNormalizeSharedFilesRejectsDuplicatePaths(t *testing.T) {
	_, err := normalizeSharedFiles([]SharedFile{
		{Path: "agent-manifest.json", Content: "one"},
		{Path: "./agent-manifest.json", Content: "two"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("normalizeSharedFiles(duplicate) error = %v, want duplicate error", err)
	}
}
