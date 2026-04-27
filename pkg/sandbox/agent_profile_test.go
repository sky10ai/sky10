package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAgentProfileLayoutSeedsPortableFiles(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	if err := EnsureAgentProfileLayout(sharedDir, AgentProfileSeed{
		DisplayName: "Hermes Dev",
		Slug:        "hermes-dev",
		Template:    templateHermes,
		Model:       "openrouter/anthropic/claude-opus-4-6",
	}); err != nil {
		t.Fatalf("EnsureAgentProfileLayout() error: %v", err)
	}

	workspaceDir := filepath.Join(sharedDir, agentWorkspaceDirName)

	for _, rel := range []string{
		agentProfileSoulFile,
		agentProfileMemoryFile,
		agentProfileContractFile,
		agentProfileAgentsFile,
		agentProfileIdentityFile,
		agentProfileBootstrapFile,
		agentProfileToolsFile,
		agentProfileUserFile,
	} {
		if _, err := os.Stat(filepath.Join(sharedDir, rel)); err != nil {
			t.Fatalf("Stat(%q) error: %v", rel, err)
		}
	}

	soulData, err := os.ReadFile(filepath.Join(sharedDir, agentProfileSoulFile))
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", agentProfileSoulFile, err)
	}
	if !strings.Contains(string(soulData), "Hermes Dev") {
		t.Fatalf("%s = %q, want display name", agentProfileSoulFile, string(soulData))
	}

	contractData, err := os.ReadFile(filepath.Join(sharedDir, agentProfileContractFile))
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

	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentProfileAgentsFile), filepath.Join("..", agentProfileAgentsFile))
	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentProfileRuntimeSoul), filepath.Join("..", agentProfileSoulFile))
	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentProfileRuntimeMemory), filepath.Join("..", agentProfileMemoryFile))
}

func TestEnsureAgentProfileLayoutPreservesExistingFiles(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	soulPath := filepath.Join(sharedDir, agentProfileSoulFile)
	if err := os.MkdirAll(filepath.Dir(soulPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error: %v", err)
	}
	if err := os.WriteFile(soulPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error: %v", agentProfileSoulFile, err)
	}

	workspaceAgentsPath := filepath.Join(sharedDir, agentWorkspaceDirName, agentProfileAgentsFile)
	if err := os.MkdirAll(filepath.Dir(workspaceAgentsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error: %v", err)
	}
	if err := os.WriteFile(workspaceAgentsPath, []byte("custom workspace instructions\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(AGENTS.md) error: %v", err)
	}

	if err := EnsureAgentProfileLayout(sharedDir, AgentProfileSeed{
		DisplayName: "OpenClaw Dev",
		Slug:        "openclaw-dev",
		Template:    templateOpenClaw,
	}); err != nil {
		t.Fatalf("EnsureAgentProfileLayout() error: %v", err)
	}

	gotSoul, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", agentProfileSoulFile, err)
	}
	if string(gotSoul) != "keep me\n" {
		t.Fatalf("%s = %q, want preserved content", agentProfileSoulFile, string(gotSoul))
	}

	info, err := os.Lstat(workspaceAgentsPath)
	if err != nil {
		t.Fatalf("Lstat(AGENTS.md) error: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("workspace AGENTS.md should remain a regular file when user content already exists")
	}
}

func TestEnsureAgentProfileLayoutMigratesLegacyLowercaseFiles(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	workspaceDir := filepath.Join(sharedDir, agentWorkspaceDirName)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(sharedDir, "soul.md"), []byte("legacy soul\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(legacy soul.md) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "memory.md"), []byte("legacy memory\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(legacy memory.md) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "identity.md"), []byte("legacy identity\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(legacy identity.md) error: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "identity.md"), filepath.Join(workspaceDir, "identity.md")); err != nil {
		t.Fatalf("Symlink(workspace/identity.md) error: %v", err)
	}

	if err := EnsureAgentProfileLayout(sharedDir, AgentProfileSeed{
		DisplayName: "Hermes Dev",
		Slug:        "hermes-dev",
		Template:    templateHermes,
	}); err != nil {
		t.Fatalf("EnsureAgentProfileLayout() error: %v", err)
	}

	gotSoul, err := os.ReadFile(filepath.Join(sharedDir, agentProfileSoulFile))
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", agentProfileSoulFile, err)
	}
	if string(gotSoul) != "legacy soul\n" {
		t.Fatalf("%s = %q, want preserved migrated content", agentProfileSoulFile, string(gotSoul))
	}

	gotMemory, err := os.ReadFile(filepath.Join(sharedDir, agentProfileMemoryFile))
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", agentProfileMemoryFile, err)
	}
	if string(gotMemory) != "legacy memory\n" {
		t.Fatalf("%s = %q, want preserved migrated content", agentProfileMemoryFile, string(gotMemory))
	}

	gotIdentity, err := os.ReadFile(filepath.Join(sharedDir, agentProfileIdentityFile))
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", agentProfileIdentityFile, err)
	}
	if string(gotIdentity) != "legacy identity\n" {
		t.Fatalf("%s = %q, want preserved migrated content", agentProfileIdentityFile, string(gotIdentity))
	}

	assertSymlinkTarget(t, filepath.Join(workspaceDir, agentProfileIdentityFile), filepath.Join("..", agentProfileIdentityFile))

	entries, err := os.ReadDir(sharedDir)
	if err != nil {
		t.Fatalf("ReadDir(sharedDir) error: %v", err)
	}
	if hasEntryName(entries, "soul.md") {
		t.Fatal("legacy lowercase soul.md should be migrated away")
	}
	if hasEntryName(entries, "memory.md") {
		t.Fatal("legacy lowercase memory.md should be migrated away")
	}
	if hasEntryName(entries, "identity.md") {
		t.Fatal("legacy lowercase identity.md should be migrated away")
	}

	workspaceEntries, err := os.ReadDir(workspaceDir)
	if err != nil {
		t.Fatalf("ReadDir(workspace) error: %v", err)
	}
	if hasEntryName(workspaceEntries, "identity.md") {
		t.Fatal("legacy lowercase workspace identity.md should be migrated away")
	}
}

func hasEntryName(entries []os.DirEntry, want string) bool {
	for _, entry := range entries {
		if entry.Name() == want {
			return true
		}
	}
	return false
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
