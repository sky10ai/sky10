package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

func TestManagerCreateTransitionsToReady(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.appUpgr = func(id skyapps.ID, _ skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error) {
		return &skyapps.ReleaseInfo{ID: id, Latest: "v1.0.0"}, nil
	}
	m.runCmd = func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
		onLine("stderr", "booting vm")
		onLine("stdout", "provision complete")
		return nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "shell" {
			return []byte("192.168.64.10\n"), nil
		}
		if len(args) > 0 && args[0] == "list" {
			return []byte(`{"name":"devbox","status":"Running"}` + "\n"), nil
		}
		return nil, nil
	}

	rec, err := m.Create(context.Background(), CreateParams{
		Name:     "devbox",
		Provider: providerLima,
		Template: templateUbuntu,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if rec.Status != "creating" {
		t.Fatalf("initial status = %q, want creating", rec.Status)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := m.Get(context.Background(), "devbox")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.Status == "ready" && got.IPAddress == "192.168.64.10" {
			logs, err := m.Logs("devbox", 10)
			if err != nil {
				t.Fatalf("Logs() error: %v", err)
			}
			if len(logs.Entries) < 2 {
				t.Fatalf("log entries = %d, want >= 2", len(logs.Entries))
			}
			waitForCreateToFinish(t, m, "devbox")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, _ := m.Get(context.Background(), "devbox")
	t.Fatalf("sandbox did not reach ready state, final status=%q", got.Status)
}

func waitForCreateToFinish(t *testing.T, m *Manager, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		running := m.running[name]
		m.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sandbox %q create job did not finish", name)
}

func TestLogsMissingSandboxReturnsEmpty(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	result, err := m.Logs("missing", 10)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	if result.Name != "missing" {
		t.Fatalf("name = %q, want missing", result.Name)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(result.Entries))
	}
}

func TestDefaultSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := defaultSharedDir("devbox")
	if err != nil {
		t.Fatalf("defaultSharedDir() error: %v", err)
	}
	want := filepath.Join(home, "sky10", "sandboxes", "devbox")
	if got != want {
		t.Fatalf("defaultSharedDir() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Dir(got)); !os.IsNotExist(err) {
		t.Fatalf("shared dir parent should not be created eagerly")
	}
}

func TestRenderSandboxTemplate(t *testing.T) {
	t.Parallel()

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__`)
	got := string(renderSandboxTemplate(body, "devbox", "/Users/bf/sky10/sandboxes/devbox"))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) {
		t.Fatalf("renderSandboxTemplate() left placeholder tokens behind: %q", got)
	}
	if !strings.Contains(got, "devbox") {
		t.Fatalf("renderSandboxTemplate() missing sandbox name: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/sky10/sandboxes/devbox") {
		t.Fatalf("renderSandboxTemplate() missing shared dir: %q", got)
	}
}
