package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAgentMindLayoutSeedsPortableFiles(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	if err := EnsureAgentMindLayout(sharedDir, AgentMindSeed{
		DisplayName: "Hermes Dev",
		Slug:        "hermes-dev",
		Template:    templateHermes,
		Model:       "openrouter/anthropic/claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("EnsureAgentMindLayout() error: %v", err)
	}

	mindDir := filepath.Join(sharedDir, agentMindDirName)
	workspaceDir := filepath.Join(sharedDir, agentWorkspaceDirName)

	for _, rel := range []string{
		agentMindSoulFile,
		agentMindMemoryFile,
		agentMindContractFile,
		agentMindAgentsFile,
		agentMindIdentityFile,
		agentMindBootstrapFile,
		agentMindToolsFile,
		agentMindUserFile,
	} {
		if _, err := os.Stat(filepath.Join(mindDir, rel)); err != nil {
			t.Fatalf("Stat(%q) error: %v", rel, err)
		}
	}

	soulData, err := os.ReadFile(filepath.Join(mindDir, agentMindSoulFile))
	if err != nil {
		t.Fatalf("ReadFile(soul.md) error: %v", err)
	}
	if !strings.Contains(string(soulData), "Hermes Dev") {
		t.Fatalf("soul.md = %q, want display name", string(soulData))
	}

	contractData, err := os.ReadFile(filepath.Join(mindDir, agentMindContractFile))
	if err != nil {
		t.Fatalf("ReadFile(sky10.md) error: %v", err)
	}
	contractText := string(contractData)
	if !strings.Contains(contractText, `profile_id: "hermes-dev"`) {
		t.Fatalf("sky10.md = %q, want slug-backed profile_id", contractText)
	}
	if !strings.Contains(contractText, `family: "hermes"`) {
		t.Fatalf("sky10.md = %q, want hermes runtime family", contractText)
	}
	if !strings.Contains(contractText, `provider: "openrouter"`) {
		t.Fatalf("sky10.md = %q, want parsed model provider", contractText)
	}

	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentMindAgentsFile), filepath.Join("..", agentMindDirName, agentMindAgentsFile))
	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentMindRuntimeSoul), filepath.Join("..", agentMindDirName, agentMindSoulFile))
	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentMindRuntimeMemory), filepath.Join("..", agentMindDirName, agentMindMemoryFile))
}

func TestEnsureAgentMindLayoutPreservesExistingFiles(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	mindSoulPath := filepath.Join(sharedDir, agentMindDirName, agentMindSoulFile)
	if err := os.MkdirAll(filepath.Dir(mindSoulPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(mind) error: %v", err)
	}
	if err := os.WriteFile(mindSoulPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(soul.md) error: %v", err)
	}

	workspaceAgentsPath := filepath.Join(sharedDir, agentWorkspaceDirName, agentMindAgentsFile)
	if err := os.MkdirAll(filepath.Dir(workspaceAgentsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error: %v", err)
	}
	if err := os.WriteFile(workspaceAgentsPath, []byte("custom workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(AGENTS.md) error: %v", err)
	}

	if err := EnsureAgentMindLayout(sharedDir, AgentMindSeed{
		DisplayName: "OpenClaw Dev",
		Slug:        "openclaw-dev",
		Template:    templateOpenClaw,
	}); err != nil {
		t.Fatalf("EnsureAgentMindLayout() error: %v", err)
	}

	gotSoul, err := os.ReadFile(mindSoulPath)
	if err != nil {
		t.Fatalf("ReadFile(soul.md) error: %v", err)
	}
	if string(gotSoul) != "keep me\n" {
		t.Fatalf("soul.md = %q, want preserved content", string(gotSoul))
	}

	info, err := os.Lstat(workspaceAgentsPath)
	if err != nil {
		t.Fatalf("Lstat(AGENTS.md) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("workspace AGENTS.md should remain a regular file when user content already exists")
	}
}

func assertSymlinkTarget(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink(%q) error: %v", path, err)
	}
	if got != want {
		t.Fatalf("Readlink(%q) = %q, want %q", path, got, want)
	}
}
