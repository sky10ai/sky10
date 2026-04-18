package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	agentMindSoulFile      = "soul.md"
	agentMindMemoryFile    = "memory.md"
	agentMindContractFile  = "sky10.md"
	agentMindAgentsFile    = "AGENTS.md"
	agentMindBootstrapFile = "BOOTSTRAP.md"
	agentMindIdentityFile  = "IDENTITY.md"
	agentMindRuntimeSoul   = "SOUL.md"
	agentMindRuntimeMemory = "MEMORY.md"
	agentMindToolsFile     = "TOOLS.md"
	agentMindUserFile      = "USER.md"
)

var agentMindWorkspaceLinks = map[string]string{
	agentMindAgentsFile:    filepath.Join("..", agentMindDirName, agentMindAgentsFile),
	agentMindBootstrapFile: filepath.Join("..", agentMindDirName, agentMindBootstrapFile),
	agentMindIdentityFile:  filepath.Join("..", agentMindDirName, agentMindIdentityFile),
	agentMindRuntimeMemory: filepath.Join("..", agentMindDirName, agentMindMemoryFile),
	agentMindRuntimeSoul:   filepath.Join("..", agentMindDirName, agentMindSoulFile),
	agentMindToolsFile:     filepath.Join("..", agentMindDirName, agentMindToolsFile),
	agentMindUserFile:      filepath.Join("..", agentMindDirName, agentMindUserFile),
}

type AgentMindSeed struct {
	DisplayName string
	Slug        string
	Template    string
	Model       string
}

func EnsureAgentMindLayout(sharedDir string, seed AgentMindSeed) error {
	if err := EnsureAgentHomeLayout(sharedDir); err != nil {
		return err
	}

	slug := strings.TrimSpace(seed.Slug)
	if slug == "" {
		slug = filepath.Base(filepath.Clean(sharedDir))
	}
	displayName := strings.TrimSpace(seed.DisplayName)
	if displayName == "" {
		displayName = slug
	}
	template := strings.TrimSpace(seed.Template)
	if template == "" {
		template = "lima"
	}
	modelProvider, modelName := parseAgentMindModel(seed.Model)

	mindDir := filepath.Join(sharedDir, agentMindDirName)
	if err := writeFileIfMissing(filepath.Join(mindDir, agentMindSoulFile), agentMindSoulTemplate(displayName, template), 0o644); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(mindDir, agentMindMemoryFile), agentMindMemoryTemplate(), 0o644); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(mindDir, agentMindContractFile), agentMindContractTemplate(displayName, slug, template, modelProvider, modelName), 0o644); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(mindDir, agentMindAgentsFile), agentMindAgentsTemplate(), 0o644); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(mindDir, agentMindIdentityFile), agentMindIdentityTemplate(displayName, slug, template), 0o644); err != nil {
		return err
	}
	for _, name := range []string{agentMindBootstrapFile, agentMindToolsFile, agentMindUserFile} {
		if err := writeFileIfMissing(filepath.Join(mindDir, name), "", 0o644); err != nil {
			return err
		}
	}

	workspaceDir := filepath.Join(sharedDir, agentWorkspaceDirName)
	for name, target := range agentMindWorkspaceLinks {
		if err := ensureRelativeSymlink(filepath.Join(workspaceDir, name), target); err != nil {
			return err
		}
	}

	return nil
}

func parseAgentMindModel(model string) (string, string) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "anthropic/claude-sonnet-4-6"
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "custom", model
}

func agentMindSoulTemplate(displayName, template string) string {
	return strings.TrimSpace(fmt.Sprintf(`
# Soul

This file defines the durable identity for %s.

## Role

Describe who this agent is and what it should optimize for in the %s runtime.

## Tone

Describe how the agent should communicate.

## Boundaries

Describe what the agent should avoid, when it should escalate, and what humans own.
`, displayName, template)) + "\n"
}

func agentMindMemoryTemplate() string {
	return strings.TrimSpace(`
# Memory

Use this file for durable facts that should survive model, runtime, and machine changes.

- Project conventions worth carrying forward
- Recurring tasks or preferences
- Useful environment facts
`) + "\n"
}

func agentMindContractTemplate(displayName, slug, template, modelProvider, modelName string) string {
	return fmt.Sprintf(`---
schema: sky10-agent/v1
profile_id: %s
display_name: %s
runtime:
  family: %s
model:
  provider: %s
  name: %s
bootstrap:
  working_dir: "workspace"
  prompt_refs:
    - soul.md
    - memory.md
    - AGENTS.md
field_ownership:
  human:
    - display_name
    - bootstrap
  runtime:
    - model
    - runtime
  daemon: []
---

# %s

Portable mind contract for the sky10 %s sandbox backed by mind/.
`, yamlQuote(slug), yamlQuote(displayName), yamlQuote(template), yamlQuote(modelProvider), yamlQuote(modelName), displayName, template)
}

func agentMindAgentsTemplate() string {
	return strings.TrimSpace(`
# Agent Mind

Treat the files in this directory as the portable source of truth for this agent:

- `+"`soul.md`"+`: durable identity, tone, and boundaries. Humans own this file.
- `+"`memory.md`"+`: durable portable memory worth carrying across runtime or machine changes.
- `+"`sky10.md`"+`: runtime and migration contract for this sandbox.

If you learn something durable, update `+"`memory.md`"+`.
If runtime or model details change, update `+"`sky10.md`"+`.
Do not silently rewrite `+"`soul.md`"+`; propose edits instead.
`) + "\n"
}

func agentMindIdentityTemplate(displayName, slug, template string) string {
	return strings.TrimSpace(fmt.Sprintf(`
# Identity

- Name: %s
- Slug: %s
- Runtime: sky10 %s Lima sandbox
`, displayName, slug, template)) + "\n"
}

func writeFileIfMissing(path, content string, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("expected file at %q, found directory", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %q: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("writing %q: %w", path, err)
	}
	return nil
}

func ensureRelativeSymlink(path, target string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			existing, readErr := os.Readlink(path)
			if readErr != nil {
				return fmt.Errorf("reading symlink %q: %w", path, readErr)
			}
			if existing == target {
				return nil
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing stale symlink %q: %w", path, err)
			}
		} else {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %q: %w", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("creating symlink %q -> %q: %w", path, target, err)
	}
	return nil
}

func yamlQuote(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}
