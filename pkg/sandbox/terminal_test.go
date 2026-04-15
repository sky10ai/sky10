package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

func TestTerminalCommandHermesLaunchesHermesShared(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/limactl"}, nil
	}

	args, err := m.terminalCommand(context.Background(), &Record{
		Provider: providerLima,
		Template: templateHermes,
		Slug:     "hermes-dev",
	})
	if err != nil {
		t.Fatalf("terminalCommand() error: %v", err)
	}

	want := []string{
		"/tmp/fake/limactl",
		"shell",
		"hermes-dev",
		"--",
		"bash",
		"-lc",
		"hermes-shared",
	}
	if len(args) != len(want) {
		t.Fatalf("terminalCommand() len = %d, want %d (%q)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("terminalCommand()[%d] = %q, want %q (%q)", i, args[i], want[i], args)
		}
	}
}

func TestLoadUsesTemplateSpecificShellCommand(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	body, err := json.Marshal(stateFile{
		Sandboxes: []Record{
			{
				Name:      "Hermes Dev",
				Slug:      "hermes-dev",
				Provider:  providerLima,
				Template:  templateHermes,
				Shell:     "limactl shell hermes-dev",
				CreatedAt: "2026-04-15T00:00:00Z",
				UpdatedAt: "2026-04-15T00:00:00Z",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if err := os.WriteFile(m.statePath(), body, 0o644); err != nil {
		t.Fatalf("WriteFile(state) error: %v", err)
	}

	m.records = map[string]Record{}
	if err := m.load(); err != nil {
		t.Fatalf("load() error: %v", err)
	}

	got, err := m.Get(context.Background(), "hermes-dev")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	want := defaultShellCommand("hermes-dev", templateHermes)
	if got.Shell != want {
		t.Fatalf("shell = %q, want %q", got.Shell, want)
	}
}
