package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultLimaSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := defaultLimaSharedDir("bobs-burgers")
	if err != nil {
		t.Fatalf("defaultLimaSharedDir: %v", err)
	}

	want := filepath.Join(home, "sky10", "sandboxes", "bobs-burgers")
	if got != want {
		t.Fatalf("defaultLimaSharedDir = %q, want %q", got, want)
	}
}

func TestWalkUp(t *testing.T) {
	t.Parallel()

	base := filepath.Join(string(filepath.Separator), "tmp", "sky10", "nested")
	got := walkUp(base)
	if len(got) < 4 {
		t.Fatalf("walkUp(%q) returned too few directories: %v", base, got)
	}
	if got[0] != base {
		t.Fatalf("walkUp(%q) first dir = %q, want %q", base, got[0], base)
	}
	if got[len(got)-1] != string(filepath.Separator) {
		t.Fatalf("walkUp(%q) last dir = %q, want %q", base, got[len(got)-1], string(filepath.Separator))
	}
}

func TestHasLimaTemplateAssets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range append(append([]string(nil), agentLimaAssetFiles...), agentLimaHostsScript) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	if !hasLimaTemplateAssets(dir) {
		t.Fatal("hasLimaTemplateAssets() = false, want true")
	}
}

func TestValidateSandboxCreate(t *testing.T) {
	t.Parallel()

	if err := validateSandboxCreate(sandboxProviderLima, sandboxTemplateOpenClaw); err != nil {
		t.Fatalf("validateSandboxCreate(valid): %v", err)
	}
	if err := validateSandboxCreate("docker", sandboxTemplateOpenClaw); err == nil {
		t.Fatal("validateSandboxCreate(docker): want error")
	}
	if err := validateSandboxCreate(sandboxProviderLima, "claude"); err == nil {
		t.Fatal("validateSandboxCreate(unknown template): want error")
	}
}

func TestRenderLimaTemplate(t *testing.T) {
	t.Parallel()

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__`)
	got := string(renderLimaTemplate(body, "bobs-burgers", "/Users/bf/sky10/sandboxes/bobs-burgers"))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) {
		t.Fatalf("renderLimaTemplate() left placeholder tokens behind: %q", got)
	}
	if !strings.Contains(got, "bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing sandbox name: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/sky10/sandboxes/bobs-burgers") {
		t.Fatalf("renderLimaTemplate() missing shared dir: %q", got)
	}
}

func TestPrepareLimaSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	pluginAssets := map[string][]byte{
		agentLimaPluginManifest: []byte(`{"id":"sky10"}` + "\n"),
		agentLimaPluginIndex:    []byte("export default function register() {}\n"),
	}
	if err := prepareLimaSharedDir(sharedDir, []byte("#!/bin/sh\n"), pluginAssets, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}); err != nil {
		t.Fatalf("prepareLimaSharedDir() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sharedDir, agentLimaHostsScript)); err != nil {
		t.Fatalf("Stat(hosts helper) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharedDir, ".env")); err != nil {
		t.Fatalf("Stat(.env) error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(sharedDir, ".env"))
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=openai-key") {
		t.Fatalf(".env = %q, want resolved openai key", string(data))
	}
	if _, err := os.Stat(filepath.Join(sharedDir, agentLimaPluginManifest)); err != nil {
		t.Fatalf("Stat(plugin manifest) error: %v", err)
	}
}
