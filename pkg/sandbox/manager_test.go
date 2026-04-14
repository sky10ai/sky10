package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if rec.Slug != "devbox" {
		t.Fatalf("slug = %q, want devbox", rec.Slug)
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

func TestManagerCreateSlugifiesDisplayName(t *testing.T) {
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
		onLine("stdout", "booting vm")
		return nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "shell" {
			return []byte("192.168.64.11\n"), nil
		}
		if len(args) > 0 && args[0] == "list" {
			return []byte(`{"name":"bob-the-fish","status":"Running"}` + "\n"), nil
		}
		return nil, nil
	}

	rec, err := m.Create(context.Background(), CreateParams{
		Name:     "Bob The Fish",
		Provider: providerLima,
		Template: templateUbuntu,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if rec.Name != "Bob The Fish" {
		t.Fatalf("name = %q, want %q", rec.Name, "Bob The Fish")
	}
	if rec.Slug != "bob-the-fish" {
		t.Fatalf("slug = %q, want bob-the-fish", rec.Slug)
	}
	if !strings.HasSuffix(rec.SharedDir, filepath.Join("sky10", "sandboxes", "bob-the-fish")) {
		t.Fatalf("shared dir = %q, want slugified suffix", rec.SharedDir)
	}
	if rec.Shell != "limactl shell bob-the-fish" {
		t.Fatalf("shell = %q, want slugified shell command", rec.Shell)
	}

	got, err := m.Get(context.Background(), "Bob The Fish")
	if err != nil {
		t.Fatalf("Get(display name) error: %v", err)
	}
	if got.Slug != "bob-the-fish" {
		t.Fatalf("Get(display name) slug = %q, want bob-the-fish", got.Slug)
	}

	got, err = m.Get(context.Background(), "bob-the-fish")
	if err != nil {
		t.Fatalf("Get(slug) error: %v", err)
	}
	if got.Name != "Bob The Fish" {
		t.Fatalf("Get(slug) name = %q, want %q", got.Name, "Bob The Fish")
	}

	waitForCreateToFinish(t, m, "bob-the-fish")
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

func TestLogsMissingSandboxReturnsNotFound(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	if _, err := m.Logs("missing", 10); err == nil {
		t.Fatalf("Logs() error = nil, want not found")
	}
}

func TestRefreshRuntimeDoesNotPromoteCreatingSandbox(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-m1"] = Record{
		Name:      "openclaw-m1",
		Slug:      "openclaw-m1",
		Provider:  providerLima,
		Template:  templateOpenClaw,
		Status:    "creating",
		VMStatus:  "Stopped",
		SharedDir: filepath.Join(t.TempDir(), "shared"),
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "list" {
			return []byte(`{"name":"openclaw-m1","status":"Running"}` + "\n"), nil
		}
		return nil, nil
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}

	got, err := m.Get(context.Background(), "openclaw-m1")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Status != "creating" {
		t.Fatalf("status = %q, want creating", got.Status)
	}
	if got.VMStatus != "Running" {
		t.Fatalf("vm status = %q, want Running", got.VMStatus)
	}
}

func TestBundledOpenClawUserScriptLoadsOpenClawEnvFile(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateOpenClawUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateOpenClawUser, err)
	}
	if !strings.Contains(string(body), "EnvironmentFile=-%h/.openclaw/.env") {
		t.Fatalf("bundled user script missing systemd env file import: %q", string(body))
	}
	if !strings.Contains(string(body), "bootstrap_local_cli_pairing") {
		t.Fatalf("bundled user script missing CLI pairing bootstrap: %q", string(body))
	}
	if !strings.Contains(string(body), `"skills": ["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled user script missing browser skill registration: %q", string(body))
	}
	if !strings.Contains(string(body), `browser["ssrfPolicy"] = {"dangerouslyAllowPrivateNetwork": True}`) {
		t.Fatalf("bundled user script missing relaxed browser SSRF policy: %q", string(body))
	}
	if !strings.Contains(string(body), `channels.setdefault("sky10", {})["healthMonitor"] = {"enabled": False}`) {
		t.Fatalf("bundled user script missing sky10 health monitor disable: %q", string(body))
	}
}

func TestBundledOpenClawDependencyScriptPersistsRouteMetrics(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateOpenClawDep)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateOpenClawDep, err)
	}
	script := string(body)
	if !strings.Contains(script, "99-openclaw-route-metrics.yaml") {
		t.Fatalf("bundled dependency script missing netplan route override: %q", script)
	}
	if !strings.Contains(script, "route-metric: 100") || !strings.Contains(script, "route-metric: 200") {
		t.Fatalf("bundled dependency script missing persistent route metrics: %q", script)
	}
	if !strings.Contains(script, "netplan apply") {
		t.Fatalf("bundled dependency script missing netplan apply: %q", script)
	}
}

func TestBundledOpenClawPluginDefaultsAdvertiseBrowserSkill(t *testing.T) {
	t.Parallel()

	manifestBody, err := readBundledTemplateAsset(filepath.Join(templateOpenClawPluginDir, "openclaw.plugin.json"))
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(plugin manifest) error: %v", err)
	}
	if !strings.Contains(string(manifestBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled plugin manifest missing browser skill default: %q", string(manifestBody))
	}

	indexBody, err := readBundledTemplateAsset(filepath.Join(templateOpenClawPluginDir, "src", "index.js"))
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(plugin index) error: %v", err)
	}
	if !strings.Contains(string(indexBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled plugin index missing browser skill default: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `listAccountIds: () => [cfg.agentName]`) {
		t.Fatalf("bundled plugin index missing stable account IDs: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `defaultAccountId: () => cfg.agentName`) {
		t.Fatalf("bundled plugin index missing stable default account ID: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `isConfigured: () => true`) {
		t.Fatalf("bundled plugin index missing configured account state: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `api.registerService({`) {
		t.Fatalf("bundled plugin index missing bridge service registration: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `Symbol.for("sky10.openclaw.bridge")`) {
		t.Fatalf("bundled plugin index missing global bridge singleton: %q", string(indexBody))
	}
	if strings.Contains(string(indexBody), `startAccount: async`) {
		t.Fatalf("bundled plugin index should not register a gateway account runtime: %q", string(indexBody))
	}
}

func TestStopMissingInstanceMarksSandboxStopped(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["devbox"] = Record{
		Name:      "devbox",
		Slug:      "devbox",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "error",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		return nil, nil
	}
	m.runCmd = func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
		t.Fatalf("runCmd should not be called when the instance is missing")
		return nil
	}

	rec, err := m.Stop(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if rec.Status != "stopped" {
		t.Fatalf("Stop() status = %q, want stopped", rec.Status)
	}
	if rec.VMStatus != "Stopped" {
		t.Fatalf("Stop() vm status = %q, want Stopped", rec.VMStatus)
	}
}

func TestDeleteMissingInstanceRemovesRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", home)

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["devbox"] = Record{
		Name:      "devbox",
		Slug:      "devbox",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "error",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		return nil, nil
	}
	m.runCmd = func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
		t.Fatalf("runCmd should not be called when the instance is missing")
		return nil
	}
	orphanDir := filepath.Join(home, ".lima", "devbox")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "ha.stderr.log"), []byte("orphan"), 0o644); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	rec, err := m.Delete(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if rec.Slug != "devbox" {
		t.Fatalf("Delete() slug = %q, want devbox", rec.Slug)
	}
	if _, err := m.Get(context.Background(), "devbox"); err == nil {
		t.Fatalf("sandbox record still present after Delete()")
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphan Lima dir still present after Delete(): %v", err)
	}
}

func TestLogsMissingFileReturnsEmptyEntries(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["devbox"] = Record{
		Name:      "devbox",
		Slug:      "devbox",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "creating",
		CreatedAt: now,
		UpdatedAt: now,
	}

	logs, err := m.Logs("devbox", 10)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	if logs.Entries == nil {
		t.Fatalf("Logs() entries = nil, want empty slice")
	}
	if len(logs.Entries) != 0 {
		t.Fatalf("Logs() entries len = %d, want 0", len(logs.Entries))
	}

	data, err := json.Marshal(logs)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if bytes.Contains(data, []byte(`"entries":null`)) {
		t.Fatalf("logs JSON = %s, want entries array", data)
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

func TestReadBundledTemplate(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateUbuntuAsset)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset() error: %v", err)
	}
	if !strings.Contains(string(body), templateNameToken) {
		t.Fatalf("readBundledTemplateAsset() missing sandbox token")
	}
	if !strings.Contains(string(body), templateSharedToken) {
		t.Fatalf("readBundledTemplateAsset() missing shared-dir token")
	}
}

func TestReadBundledOpenClawTemplateProbeUsesHealthChecks(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateOpenClawYAML)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset() error: %v", err)
	}
	text := string(body)
	if strings.Contains(text, "command -v openclaw") {
		t.Fatalf("openclaw template probe should not require openclaw on PATH")
	}
	if strings.Contains(text, "command -v sky10") {
		t.Fatalf("openclaw template probe should not require sky10 on PATH")
	}
	if !strings.Contains(text, "http://127.0.0.1:9101/health") {
		t.Fatalf("openclaw template probe missing guest sky10 health check")
	}
	if !strings.Contains(text, "http://127.0.0.1:18789/health") {
		t.Fatalf("openclaw template probe missing OpenClaw health check")
	}
	if !strings.Contains(text, "portForwards:") || !strings.Contains(text, "ignore: true") {
		t.Fatalf("openclaw template should disable Lima host port forwarding")
	}
}

func TestPrepareOpenClawSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	helper := []byte("#!/bin/sh\n")
	pluginAssets := map[string][]byte{
		templateOpenClawPluginManifest: []byte(`{"id":"sky10"}` + "\n"),
		templateOpenClawPluginIndex:    []byte("export default function register() {}\n"),
	}
	if err := prepareOpenClawSharedDir(sharedDir, helper, pluginAssets, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}); err != nil {
		t.Fatalf("prepareOpenClawSharedDir() error: %v", err)
	}

	envPath := filepath.Join(sharedDir, ".env")
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	if !strings.Contains(string(envData), "ANTHROPIC_API_KEY=") {
		t.Fatalf(".env = %q, want provider key stub", string(envData))
	}
	if !strings.Contains(string(envData), "OPENAI_API_KEY=openai-key") {
		t.Fatalf(".env = %q, want resolved openai key", string(envData))
	}

	helperPath := filepath.Join(sharedDir, templateHostsHelper)
	helperData, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("ReadFile(hosts helper) error: %v", err)
	}
	if string(helperData) != string(helper) {
		t.Fatalf("hosts helper = %q, want %q", string(helperData), string(helper))
	}

	pluginManifestPath := filepath.Join(sharedDir, templateOpenClawPluginManifest)
	pluginManifestData, err := os.ReadFile(pluginManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(plugin manifest) error: %v", err)
	}
	if string(pluginManifestData) != string(pluginAssets[templateOpenClawPluginManifest]) {
		t.Fatalf("plugin manifest = %q, want %q", string(pluginManifestData), string(pluginAssets[templateOpenClawPluginManifest]))
	}
}

func TestBuildStartArgsOpenClaw(t *testing.T) {
	t.Parallel()

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	args, err := m.buildStartArgs(context.Background(), Record{
		Name:     "agent-123",
		Slug:     "agent-123",
		Provider: providerLima,
		Template: templateOpenClaw,
	}, "/tmp/openclaw.yaml")
	if err != nil {
		t.Fatalf("buildStartArgs() error: %v", err)
	}

	wantArgs := []string{"start", "--tty=false", "--progress", "--name", "agent-123", "/tmp/openclaw.yaml"}
	if strings.Join(args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("buildStartArgs() = %v, want %v", args, wantArgs)
	}
}

func TestBuildStartArgsOpenClawWithModel(t *testing.T) {
	t.Parallel()

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	args, err := m.buildStartArgs(context.Background(), Record{
		Name:     "agent-123",
		Slug:     "agent-123",
		Provider: providerLima,
		Template: templateOpenClaw,
		Model:    "anthropic/claude-opus-4-6",
	}, "/tmp/openclaw.yaml")
	if err != nil {
		t.Fatalf("buildStartArgs() error: %v", err)
	}

	wantArgs := []string{
		"start",
		"--tty=false",
		"--progress",
		"--name", "agent-123",
		"--set", `.param.model = "anthropic/claude-opus-4-6"`,
		"/tmp/openclaw.yaml",
	}
	if strings.Join(args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("buildStartArgs() = %v, want %v", args, wantArgs)
	}
}

func TestManagerEnsureAdoptsRunningInstanceWithoutRecord(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list" && args[1] == "--json":
			return []byte(`{"name":"openclaw-m6","status":"Running"}` + "\n"), nil
		case len(args) >= 2 && args[0] == "shell":
			return []byte("192.168.64.14\n"), nil
		default:
			return nil, nil
		}
	}
	m.runCmd = func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
		t.Fatalf("runCmd should not be called when Ensure adopts an already-running instance")
		return nil
	}

	rec, err := m.Ensure(context.Background(), CreateParams{
		Name:     "openclaw-m6",
		Provider: providerLima,
		Template: templateOpenClaw,
		Model:    "anthropic/claude-opus-4-6",
	})
	if err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	if rec.Status != "ready" {
		t.Fatalf("Ensure() status = %q, want ready", rec.Status)
	}
	if rec.VMStatus != "Running" {
		t.Fatalf("Ensure() vm status = %q, want Running", rec.VMStatus)
	}
	if rec.IPAddress != "192.168.64.14" {
		t.Fatalf("Ensure() ip address = %q, want 192.168.64.14", rec.IPAddress)
	}
	if rec.Model != "anthropic/claude-opus-4-6" {
		t.Fatalf("Ensure() model = %q, want anthropic/claude-opus-4-6", rec.Model)
	}

	got, err := m.Get(context.Background(), "openclaw-m6")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Status != "ready" {
		t.Fatalf("Get() status = %q, want ready", got.Status)
	}
}

func TestWaitForOpenClawGateway(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := waitForOpenClawGateway(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("not ready")
		}
		return []byte("ok"), nil
	}, "/tmp/fake/limactl", "agent-123", 5*time.Second)
	if err != nil {
		t.Fatalf("waitForOpenClawGateway() error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("waitForOpenClawGateway() attempts = %d, want 3", attempts)
	}
}

func TestWaitForGuestSky10(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := waitForGuestSky10(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		attempts++
		if attempts < 2 {
			return nil, fmt.Errorf("not ready")
		}
		return []byte("ok"), nil
	}, "/tmp/fake/limactl", "agent-123", 5*time.Second)
	if err != nil {
		t.Fatalf("waitForGuestSky10() error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("waitForGuestSky10() attempts = %d, want 2", attempts)
	}
}

func TestWaitForGuestOpenClawAgent(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := waitForGuestOpenClawAgent(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		attempts++
		if attempts < 4 {
			return nil, fmt.Errorf("not ready")
		}
		return []byte("ok"), nil
	}, "/tmp/fake/limactl", "agent-123", 8*time.Second)
	if err != nil {
		t.Fatalf("waitForGuestOpenClawAgent() error: %v", err)
	}
	if attempts != 4 {
		t.Fatalf("waitForGuestOpenClawAgent() attempts = %d, want 4", attempts)
	}
}

func TestLimaInstanceDirPathUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LIMA_HOME", "")

	got, err := limaInstanceDirPath("devbox")
	if err != nil {
		t.Fatalf("limaInstanceDirPath() error: %v", err)
	}
	want := filepath.Join(home, ".lima", "devbox")
	if got != want {
		t.Fatalf("limaInstanceDirPath() = %q, want %q", got, want)
	}
}
