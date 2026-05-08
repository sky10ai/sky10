package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
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

func TestManagerCreateAllocatesForwardedEndpoint(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	allowTemplateOnCurrentOSForTest(t, templateOpenClaw)

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	m.hostRPC = nil
	m.forwardedPortStart = defaultForwardedGuestPortStart
	m.localPortAvailable = func(host string, port int) bool {
		return host == defaultForwardedGuestHost &&
			(port == defaultForwardedGuestPortStart || port == defaultForwardedGuestPortStart+1)
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.runCmd = func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
		return nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "shell" {
			script := args[len(args)-1]
			if strings.Contains(script, "ip -4") {
				return []byte("192.168.64.10\n"), nil
			}
			return []byte("ok"), nil
		}
		if len(args) > 0 && args[0] == "list" {
			return []byte(`{"name":"openclaw-m1","status":"Running"}` + "\n"), nil
		}
		return nil, nil
	}

	rec, err := m.Create(context.Background(), CreateParams{
		Name:     "openclaw-m1",
		Provider: providerLima,
		Template: templateOpenClaw,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if rec.ForwardedHost != defaultForwardedGuestHost {
		t.Fatalf("forwarded host = %q, want %q", rec.ForwardedHost, defaultForwardedGuestHost)
	}
	if rec.ForwardedPort != defaultForwardedGuestPortStart {
		t.Fatalf("forwarded port = %d, want %d", rec.ForwardedPort, defaultForwardedGuestPortStart)
	}
	assertRecordForwardedEndpoints(t, *rec, []ForwardedEndpoint{
		{
			Name:      ForwardedEndpointSky10,
			Host:      defaultForwardedGuestHost,
			HostPort:  defaultForwardedGuestPortStart,
			GuestHost: defaultForwardedGuestHost,
			GuestPort: guestSky10Port,
			Offset:    0,
			Protocol:  "tcp",
		},
		{
			Name:      ForwardedEndpointOpenClawGateway,
			Host:      defaultForwardedGuestHost,
			HostPort:  defaultForwardedGuestPortStart + 1,
			GuestHost: defaultForwardedGuestHost,
			GuestPort: openClawGatewayGuestPort,
			Offset:    1,
			Protocol:  "tcp",
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := m.Get(context.Background(), "openclaw-m1")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitForCreateToFinish(t, m, "openclaw-m1")

	data, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatalf("ReadFile(state) error: %v", err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(state) error: %v", err)
	}
	if len(state.Sandboxes) != 1 {
		t.Fatalf("state sandboxes = %d, want 1", len(state.Sandboxes))
	}
	persisted := state.Sandboxes[0]
	if persisted.ForwardedHost != defaultForwardedGuestHost || persisted.ForwardedPort != defaultForwardedGuestPortStart {
		t.Fatalf("persisted forwarded endpoint = %s:%d, want %s:%d",
			persisted.ForwardedHost, persisted.ForwardedPort, defaultForwardedGuestHost, defaultForwardedGuestPortStart)
	}
	assertRecordForwardedEndpoints(t, persisted, rec.ForwardedEndpoints)
}

func allowTemplateOnCurrentOSForTest(t *testing.T, template string) {
	t.Helper()
	spec, ok := sandboxTemplateSpecs[template]
	if !ok {
		t.Fatalf("sandbox template %q not found", template)
	}
	original := spec
	spec.macOSOnly = false
	sandboxTemplateSpecs[template] = spec
	t.Cleanup(func() {
		sandboxTemplateSpecs[template] = original
	})
}

func TestRPCHandlerRejectsReconnectGuest(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	_, err, handled := NewRPCHandler(m).Dispatch(context.Background(), "sandbox.reconnectGuest", []byte(`{}`))
	if !handled {
		t.Fatal("sandbox.reconnectGuest handled = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("sandbox.reconnectGuest error = %v, want unknown method", err)
	}
}

func TestAssignForwardedEndpointSkipsAssignedAndUnavailablePorts(t *testing.T) {
	t.Parallel()

	m := &Manager{
		records: map[string]Record{
			"existing": {
				Slug:          "existing",
				Provider:      providerLima,
				Template:      templateHermes,
				ForwardedHost: defaultForwardedGuestHost,
				ForwardedPort: defaultForwardedGuestPortStart,
			},
		},
		forwardedPortStart: defaultForwardedGuestPortStart,
		localPortAvailable: func(host string, port int) bool {
			if host != defaultForwardedGuestHost {
				t.Fatalf("localPortAvailable host = %q, want %q", host, defaultForwardedGuestHost)
			}
			return port != defaultForwardedGuestPortStart+1
		},
	}
	rec := Record{
		Slug:     "hermes-m1",
		Provider: providerLima,
		Template: templateHermes,
	}

	changed, err := m.assignForwardedEndpointLocked(&rec)
	if err != nil {
		t.Fatalf("assignForwardedEndpointLocked() error: %v", err)
	}
	if !changed {
		t.Fatal("assignForwardedEndpointLocked() changed = false, want true")
	}
	wantPort := defaultForwardedGuestPortStart + 2
	if rec.ForwardedHost != defaultForwardedGuestHost || rec.ForwardedPort != wantPort {
		t.Fatalf("forwarded endpoint = %s:%d, want %s:%d",
			rec.ForwardedHost, rec.ForwardedPort, defaultForwardedGuestHost, wantPort)
	}
}

func TestAssignForwardedEndpointAllocatesContiguousOpenClawBlock(t *testing.T) {
	t.Parallel()

	m := &Manager{
		records:            map[string]Record{},
		forwardedPortStart: defaultForwardedGuestPortStart,
		localPortAvailable: func(host string, port int) bool {
			if host != defaultForwardedGuestHost {
				t.Fatalf("localPortAvailable host = %q, want %q", host, defaultForwardedGuestHost)
			}
			return port != defaultForwardedGuestPortStart+1
		},
	}
	rec := Record{
		Slug:     "openclaw-m1",
		Provider: providerLima,
		Template: templateOpenClaw,
	}

	changed, err := m.assignForwardedEndpointLocked(&rec)
	if err != nil {
		t.Fatalf("assignForwardedEndpointLocked() error: %v", err)
	}
	if !changed {
		t.Fatal("assignForwardedEndpointLocked() changed = false, want true")
	}
	wantPort := defaultForwardedGuestPortStart + 2
	if rec.ForwardedPort != wantPort {
		t.Fatalf("forwarded port = %d, want %d", rec.ForwardedPort, wantPort)
	}
	assertRecordForwardedEndpoints(t, rec, []ForwardedEndpoint{
		{
			Name:      ForwardedEndpointSky10,
			Host:      defaultForwardedGuestHost,
			HostPort:  wantPort,
			GuestHost: defaultForwardedGuestHost,
			GuestPort: guestSky10Port,
			Offset:    0,
			Protocol:  "tcp",
		},
		{
			Name:      ForwardedEndpointOpenClawGateway,
			Host:      defaultForwardedGuestHost,
			HostPort:  wantPort + 1,
			GuestHost: defaultForwardedGuestHost,
			GuestPort: openClawGatewayGuestPort,
			Offset:    1,
			Protocol:  "tcp",
		},
	})
}

func TestEnsureForwardedEndpointPreservesExistingPort(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.localPortAvailable = func(host string, port int) bool {
		t.Fatalf("localPortAvailable should not be called for existing forwarded port %s:%d", host, port)
		return false
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["hermes-m1"] = Record{
		Name:          "Hermes M1",
		Slug:          "hermes-m1",
		Provider:      providerLima,
		Template:      templateHermes,
		Status:        "stopped",
		ForwardedPort: 40123,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := m.saveLocked(); err != nil {
		t.Fatalf("saveLocked() error: %v", err)
	}

	rec, err := m.ensureForwardedEndpoint("hermes-m1")
	if err != nil {
		t.Fatalf("ensureForwardedEndpoint() error: %v", err)
	}
	if rec.ForwardedHost != defaultForwardedGuestHost {
		t.Fatalf("forwarded host = %q, want %q", rec.ForwardedHost, defaultForwardedGuestHost)
	}
	if rec.ForwardedPort != 40123 {
		t.Fatalf("forwarded port = %d, want 40123", rec.ForwardedPort)
	}
	assertRecordForwardedEndpoints(t, *rec, []ForwardedEndpoint{
		{
			Name:      ForwardedEndpointSky10,
			Host:      defaultForwardedGuestHost,
			HostPort:  40123,
			GuestHost: defaultForwardedGuestHost,
			GuestPort: guestSky10Port,
			Offset:    0,
			Protocol:  "tcp",
		},
	})
}

func assertRecordForwardedEndpoints(t *testing.T, rec Record, want []ForwardedEndpoint) {
	t.Helper()

	if len(rec.ForwardedEndpoints) != len(want) {
		t.Fatalf("forwarded endpoints = %#v, want %#v", rec.ForwardedEndpoints, want)
	}
	for i := range want {
		if rec.ForwardedEndpoints[i] != want[i] {
			t.Fatalf("forwarded endpoint %d = %#v, want %#v", i, rec.ForwardedEndpoints[i], want[i])
		}
	}
}

func TestAppendLogProgressMarkerUpdatesRecordAndSuppressesMarker(t *testing.T) {
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
	if err := m.resetProgress("devbox"); err != nil {
		t.Fatalf("resetProgress() error: %v", err)
	}

	m.appendLog("devbox", "stdout", `SKY10_PROGRESS {"event":"end","id":"sandbox.prepare","summary":"Sandbox prepared."}`)

	got, err := m.Get(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Progress == nil {
		t.Fatal("progress = nil, want populated progress state")
	}
	if got.Progress.Percent != 50 {
		t.Fatalf("progress percent = %d, want 50", got.Progress.Percent)
	}
	if got.Progress.Summary != "Sandbox prepared." {
		t.Fatalf("progress summary = %q, want %q", got.Progress.Summary, "Sandbox prepared.")
	}

	m.appendLog("devbox", "stdout", "booting vm")
	logs, err := m.Logs("devbox", 10)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	if len(logs.Entries) != 1 || logs.Entries[0].Line != "booting vm" {
		t.Fatalf("logs = %#v, want only raw non-marker output", logs.Entries)
	}

	m.appendLog("devbox", "stdout", `SKY10_PROGRESS {"event":"begin","id":"vm.start","summary":"Booting device..."}`)
	m.appendLog("devbox", "stdout", `SKY10_PROGRESS {"event":"end","id":"vm.start","summary":"Device booted."}`)

	got, err = m.Get(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Progress == nil {
		t.Fatal("progress = nil after vm.start, want populated progress state")
	}
	if got.Progress.Percent != 100 {
		t.Fatalf("progress percent = %d, want 100", got.Progress.Percent)
	}
	if got.Progress.Summary != "Device booted." {
		t.Fatalf("progress summary = %q, want %q", got.Progress.Summary, "Device booted.")
	}
}

func TestAppendLogProgressMarkerWrappedByCloudInitUpdatesRecord(t *testing.T) {
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
		Template:  templateOpenClaw,
		Status:    "creating",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.resetProgress("devbox"); err != nil {
		t.Fatalf("resetProgress() error: %v", err)
	}
	if err := m.updateProgress("devbox", progressEvent{
		Event:   "end",
		ID:      "sandbox.prepare",
		Summary: "Sandbox prepared.",
	}); err != nil {
		t.Fatalf("updateProgress(sandbox.prepare) error: %v", err)
	}
	if err := m.updateProgress("devbox", progressEvent{
		Event:   "begin",
		ID:      "vm.start",
		Summary: "Booting device...",
	}); err != nil {
		t.Fatalf("updateProgress(vm.start begin) error: %v", err)
	}
	if err := m.updateProgress("devbox", progressEvent{
		Event:   "end",
		ID:      "vm.start",
		Summary: "Device booted.",
	}); err != nil {
		t.Fatalf("updateProgress(vm.start) error: %v", err)
	}

	m.appendLog("devbox", "stderr", `time="2026-04-17T06:42:01-05:00" level=info msg="[cloud-init] SKY10_PROGRESS {\"event\":\"begin\",\"id\":\"guest.system.packages\",\"summary\":\"Installing system packages...\"}"`)
	m.appendLog("devbox", "stderr", `time="2026-04-17T06:42:12-05:00" level=info msg="[cloud-init] SKY10_PROGRESS {\"event\":\"end\",\"id\":\"guest.system.packages\",\"summary\":\"System packages installed.\"}"`)
	m.appendLog("devbox", "stderr", `time="2026-04-17T06:42:12-05:00" level=info msg="[cloud-init] SKY10_PROGRESS {\"event\":\"begin\",\"id\":\"guest.node.install\",\"summary\":\"Installing Node.js...\"}"`)
	m.appendLog("devbox", "stderr", `time="2026-04-17T06:42:20-05:00" level=info msg="[cloud-init] SKY10_PROGRESS {\"event\":\"end\",\"id\":\"guest.node.install\",\"summary\":\"Node.js installed.\"}"`)
	m.appendLog("devbox", "stderr", `time="2026-04-17T06:42:20-05:00" level=info msg="[cloud-init] + printf 'SKY10_PROGRESS {\"event\":\"%s\",\"id\":\"%s\",\"summary\":\"%s\"}\n' begin guest.openclaw.install 'Installing OpenClaw...'"`)

	got, err := m.Get(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Progress == nil {
		t.Fatal("progress = nil, want populated progress state")
	}
	if got.Progress.StepID != "guest.node.install" {
		t.Fatalf("progress step_id = %q, want %q", got.Progress.StepID, "guest.node.install")
	}
	if got.Progress.Summary != "Node.js installed." {
		t.Fatalf("progress summary = %q, want %q", got.Progress.Summary, "Node.js installed.")
	}
	if got.Progress.Percent != 25 {
		t.Fatalf("progress percent = %d, want 25", got.Progress.Percent)
	}

	logs, err := m.Logs("devbox", 10)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	if len(logs.Entries) != 1 {
		t.Fatalf("log entries = %d, want 1 xtrace entry", len(logs.Entries))
	}
	if !strings.Contains(logs.Entries[0].Line, `+ printf 'SKY10_PROGRESS`) {
		t.Fatalf("log line = %q, want xtrace printf marker line", logs.Entries[0].Line)
	}
}

func TestManagerEnsureManagedApp_UsesManagedLimaInstall(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{
			Managed:    true,
			ActivePath: "/Users/test/.sky10/bin/limactl",
		}, nil
	}

	got, err := m.ensureManagedApp(context.Background(), skyapps.AppLima, true)
	if err != nil {
		t.Fatalf("ensureManagedApp() error = %v", err)
	}
	if got != "/Users/test/.sky10/bin/limactl" {
		t.Fatalf("ensureManagedApp() path = %q, want managed limactl", got)
	}
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
	if !strings.HasSuffix(rec.SharedDir, filepath.Join("Sky10", "Drives", "Agents", "bob-the-fish")) {
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

func TestDefaultShellCommandHermes(t *testing.T) {
	t.Parallel()

	got := defaultShellCommand("hermes-dev", templateHermes)
	want := "limactl shell hermes-dev -- bash -lc 'hermes-shared'"
	if got != want {
		t.Fatalf("defaultShellCommand() = %q, want %q", got, want)
	}
}

func TestDefaultShellCommandHermesDocker(t *testing.T) {
	t.Parallel()

	got := defaultShellCommand("hermes-dev", templateHermesDocker)
	want := "limactl shell hermes-dev -- bash -lc 'hermes-shared'"
	if got != want {
		t.Fatalf("defaultShellCommand() = %q, want %q", got, want)
	}
}

func TestBuildStartArgsTemplateModelOverride(t *testing.T) {
	t.Parallel()

	m := &Manager{}

	hermesArgs, err := m.buildStartArgs(context.Background(), Record{
		Slug:     "hermes-dev",
		Template: templateHermes,
		Model:    "gpt-5.4",
	}, "/tmp/hermes.yaml")
	if err != nil {
		t.Fatalf("buildStartArgs(hermes) error: %v", err)
	}
	if !strings.Contains(strings.Join(hermesArgs, "\n"), `.param.model = "gpt-5.4"`) {
		t.Fatalf("buildStartArgs(hermes) = %v, want model override", hermesArgs)
	}

	ubuntuArgs, err := m.buildStartArgs(context.Background(), Record{
		Slug:     "ubuntu-dev",
		Template: templateUbuntu,
		Model:    "gpt-5.4",
	}, "/tmp/ubuntu.yaml")
	if err != nil {
		t.Fatalf("buildStartArgs(ubuntu) error: %v", err)
	}
	if strings.Contains(strings.Join(ubuntuArgs, "\n"), `.param.model = "gpt-5.4"`) {
		t.Fatalf("buildStartArgs(ubuntu) = %v, want no model override", ubuntuArgs)
	}
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
	if !strings.Contains(string(body), `SKY10_INVITE_PATH="/sandbox-state/join.json"`) {
		t.Fatalf("bundled user script missing sandbox invite path: %q", string(body))
	}
	if strings.Contains(string(body), "sky10 join --role sandbox") {
		t.Fatalf("bundled user script should not join the host identity during boot: %q", string(body))
	}
	if !strings.Contains(string(body), "cat > \"${UNIT_DIR}/sky10.service\" <<EOF") {
		t.Fatalf("bundled user script missing guest sky10 systemd unit: %q", string(body))
	}
	if !strings.Contains(string(body), "Environment=SKY10_SANDBOX_GUEST=1") {
		t.Fatalf("bundled user script missing guest x402 bridge-only marker: %q", string(body))
	}
	if strings.Contains(string(body), "ExecStartPost=%h/.bin/sky10-managed-reconnect") {
		t.Fatalf("bundled user script should not install guest-to-host reconnect hook: %q", string(body))
	}
	if !strings.Contains(string(body), "systemctl --user enable sky10.service") {
		t.Fatalf("bundled user script missing guest sky10 systemd enable: %q", string(body))
	}
	if strings.Contains(string(body), "install_guest_reconnect_helper") {
		t.Fatalf("bundled user script should not install guest reconnect helper: %q", string(body))
	}
	if !strings.Contains(string(body), `emit_progress begin guest.openclaw.configure`) {
		t.Fatalf("bundled user script missing OpenClaw progress markers: %q", string(body))
	}
	if strings.Contains(string(body), `"method": "sandbox.reconnectGuest"`) {
		t.Fatalf("bundled user script should not call sandbox.reconnectGuest: %q", string(body))
	}
	if strings.Contains(string(body), `payload.get("host_rpc_url")`) {
		t.Fatalf("bundled user script should not parse host_rpc_url: %q", string(body))
	}
	if strings.Contains(string(body), "nohup sky10 serve") {
		t.Fatalf("bundled user script should not rely on nohup sky10 serve fallback: %q", string(body))
	}
	if !strings.Contains(string(body), "bootstrap_local_cli_pairing") {
		t.Fatalf("bundled user script missing CLI pairing bootstrap: %q", string(body))
	}
	if strings.Contains(string(body), "IDENTITY.md") {
		t.Fatalf("bundled user script should not seed identity files into the shared workspace: %q", string(body))
	}
	if !strings.Contains(string(body), `"skills": ["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled user script missing browser skill registration: %q", string(body))
	}
	if !strings.Contains(string(body), `defaults["workspace"] = "/shared/workspace"`) {
		t.Fatalf("bundled user script missing shared workspace config: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_channel["defaultAccount"] = "default"`) {
		t.Fatalf("bundled user script missing sky10 default account config: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_channel["healthMonitor"] = {"enabled": False}`) {
		t.Fatalf("bundled user script missing sky10 health monitor config: %q", string(body))
	}
	if !strings.Contains(string(body), `entries.pop("acpx", None)`) {
		t.Fatalf("bundled user script should remove stale acpx config in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `plugins["allow"] = ["sky10", "anthropic", "browser"]`) {
		t.Fatalf("bundled user script should restrict bundled OpenClaw plugins in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `plugins.setdefault("slots", {})["memory"] = "none"`) {
		t.Fatalf("bundled user script should disable bundled memory plugin in managed runtime: %q", string(body))
	}
	if !strings.Contains(string(body), `OPENCLAW_BUNDLED_PLUGINS_DIR`) || !strings.Contains(string(body), `OPENCLAW_NO_RESPAWN=1`) {
		t.Fatalf("bundled user script should wrap OpenClaw gateway with managed runtime environment: %q", string(body))
	}
	if !strings.Contains(string(body), `prime_managed_openclaw_runtime_deps`) {
		t.Fatalf("bundled user script should seed managed OpenClaw runtime deps: %q", string(body))
	}
	if !strings.Contains(string(body), `sky10_accounts["default"] = {`) {
		t.Fatalf("bundled user script missing sky10 default account entry: %q", string(body))
	}
	if !strings.Contains(string(body), `browser["ssrfPolicy"] = {"dangerouslyAllowPrivateNetwork": True}`) {
		t.Fatalf("bundled user script missing relaxed browser SSRF policy: %q", string(body))
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

func TestBundledOpenClawSystemScriptPinsOpenClawVersion(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateOpenClawSys)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateOpenClawSys, err)
	}
	script := string(body)
	if !strings.Contains(script, `OPENCLAW_VERSION=2026.5.7`) {
		t.Fatalf("bundled system script missing pinned openclaw version: %q", script)
	}
	if !strings.Contains(script, `emit_progress begin guest.openclaw.install`) {
		t.Fatalf("bundled system script missing OpenClaw progress markers: %q", script)
	}
	if !strings.Contains(script, `npm install -g "openclaw@${OPENCLAW_VERSION}"`) {
		t.Fatalf("bundled system script missing pinned openclaw install command: %q", script)
	}
	if !strings.Contains(script, "configure_managed_openclaw_bundled_plugins") {
		t.Fatalf("bundled system script missing managed OpenClaw bundled plugin tree setup: %q", script)
	}
	if !strings.Contains(script, "managed-runtime-deps") {
		t.Fatalf("bundled system script missing managed OpenClaw runtime dependency seed: %q", script)
	}
	if !strings.Contains(script, `speech-core memory-core image-generation-core media-understanding-core video-generation-core`) {
		t.Fatalf("bundled system script missing managed OpenClaw public-surface plugin tree copy: %q", script)
	}
	if !strings.Contains(script, `openclaw-system-v2`) {
		t.Fatalf("bundled system script missing bumped sentinel version: %q", script)
	}
	if strings.Contains(script, `openclaw@latest`) {
		t.Fatalf("bundled system script should not install latest openclaw: %q", script)
	}
}

func TestBundledOpenClawPluginDefaultsAdvertiseBrowserSkill(t *testing.T) {
	t.Parallel()

	manifestBody, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginManifest)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(plugin manifest) error: %v", err)
	}
	if !strings.Contains(string(manifestBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled plugin manifest missing browser skill default: %q", string(manifestBody))
	}
	if !strings.Contains(string(manifestBody), `"channels"`) || !strings.Contains(string(manifestBody), `"sky10"`) {
		t.Fatalf("bundled plugin manifest missing sky10 channel declaration: %q", string(manifestBody))
	}

	indexBody, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginIndex)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(plugin index) error: %v", err)
	}
	if !strings.Contains(string(indexBody), `["code", "shell", "browser", "web-search", "file-ops"]`) {
		t.Fatalf("bundled plugin index missing browser skill default: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `createChatChannelPlugin`) {
		t.Fatalf("bundled plugin index missing OpenClaw chat channel registration: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `createChannelReplyPipeline`) {
		t.Fatalf("bundled plugin index missing channel reply pipeline: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `api.registerChannel({ plugin: sky10ChannelPlugin })`) {
		t.Fatalf("bundled plugin index missing channel registration: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `fs.openSync(claimPathFor(msgId), "wx")`) {
		t.Fatalf("bundled plugin index missing cross-process claim guard: %q", string(indexBody))
	}
	if !strings.Contains(string(indexBody), `Symbol.for("sky10.openclaw.bridge")`) {
		t.Fatalf("bundled plugin index missing global bridge singleton: %q", string(indexBody))
	}
	if strings.Contains(string(indexBody), `/v1/responses`) {
		t.Fatalf("bundled plugin index should not self-call the gateway responses API: %q", string(indexBody))
	}
}

func TestBundledOpenClawBridgeAssetStreamsReplies(t *testing.T) {
	t.Parallel()

	indexBody, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginIndex)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleOpenClawPluginIndex, err)
	}
	indexScript := string(indexBody)
	if !strings.Contains(indexScript, "createChannelReplyPipeline") {
		t.Fatalf("bundled plugin index missing reply pipeline creation: %q", indexScript)
	}
	if !strings.Contains(indexScript, "dispatchReplyWithBufferedBlockDispatcher") {
		t.Fatalf("bundled plugin index missing buffered block dispatcher: %q", indexScript)
	}
	if !strings.Contains(indexScript, "state.client.sendDelta(") {
		t.Fatalf("bundled plugin index missing delta send path: %q", indexScript)
	}
	if !strings.Contains(indexScript, "onPartialReply: async (payload)") {
		t.Fatalf("bundled plugin index missing partial reply stream hook: %q", indexScript)
	}
	if !strings.Contains(indexScript, "resolveIncrementalReplyText") {
		t.Fatalf("bundled plugin index missing incremental reply helper: %q", indexScript)
	}
	if !strings.Contains(indexScript, "state.client.sendContent(") {
		t.Fatalf("bundled plugin index missing final content send path: %q", indexScript)
	}
	if !strings.Contains(indexScript, "stream_id: streamId") {
		t.Fatalf("bundled plugin index missing stream_id propagation: %q", indexScript)
	}
	if !strings.Contains(indexScript, "extractClientRequestID") {
		t.Fatalf("bundled plugin index missing client_request_id propagation helper: %q", indexScript)
	}

	clientBody, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginClient)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleOpenClawPluginClient, err)
	}
	clientScript := string(clientBody)
	if !strings.Contains(clientScript, "async sendContent(") {
		t.Fatalf("bundled plugin client missing sendContent helper: %q", clientScript)
	}
	if !strings.Contains(clientScript, "async sendDelta(") {
		t.Fatalf("bundled plugin client missing sendDelta helper: %q", clientScript)
	}
	if !strings.Contains(clientScript, "stream_id: streamId") {
		t.Fatalf("bundled plugin client missing stream_id propagation: %q", clientScript)
	}
}

func TestBundledHermesTemplateProbeUsesHermesCLI(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateHermesYAML)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset() error: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `command -v hermes`) {
		t.Fatalf("hermes template probe missing Hermes CLI check")
	}
	if !strings.Contains(text, `hermes-shared`) {
		t.Fatalf("hermes template message missing helper command")
	}
	assertManagedLimaTemplateForwardsGuestSky10(t, text)
}

func TestBundledHermesScriptsEmitProgressMarkers(t *testing.T) {
	t.Parallel()

	systemBody, err := readBundledTemplateAsset(templateHermesSys)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateHermesSys, err)
	}
	systemScript := string(systemBody)
	if !strings.Contains(systemScript, `emit_progress begin guest.system.packages`) {
		t.Fatalf("bundled hermes system script missing progress markers: %q", systemScript)
	}

	userBody, err := readBundledTemplateAsset(templateHermesUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateHermesUser, err)
	}
	userScript := string(userBody)
	if !strings.Contains(userScript, `emit_progress begin guest.hermes.install`) {
		t.Fatalf("bundled hermes user script missing install progress markers: %q", userScript)
	}
	if !strings.Contains(userScript, `HERMES_RELEASE_REF=v2026.5.7`) {
		t.Fatalf("bundled hermes user script missing pinned release ref: %q", userScript)
	}
	if !strings.Contains(userScript, `--branch "${HERMES_RELEASE_REF}"`) {
		t.Fatalf("bundled hermes user script missing pinned install branch: %q", userScript)
	}
	if !strings.Contains(userScript, `emit_progress begin guest.hermes.bridge.start`) {
		t.Fatalf("bundled hermes user script missing bridge progress markers: %q", userScript)
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

func TestDeleteMissingInstanceRemovesRecordedGuestDevice(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", home)

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["devbox"] = Record{
		Name:              "devbox",
		Slug:              "devbox",
		Provider:          providerLima,
		Template:          templateOpenClaw,
		Status:            "ready",
		VMStatus:          "Running",
		GuestDeviceID:     "D-guest123",
		GuestDevicePubKey: "abcdef1234567890",
		CreatedAt:         now,
		UpdatedAt:         now,
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

	var removedPubKey string
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		if method != "identity.deviceRemove" {
			t.Fatalf("unexpected host RPC method %q", method)
		}
		values, ok := params.(map[string]string)
		if !ok {
			t.Fatalf("device remove params type = %T, want map[string]string", params)
		}
		removedPubKey = values["pubkey"]
		return nil
	}

	rec, err := m.Delete(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if rec.Slug != "devbox" {
		t.Fatalf("Delete() slug = %q, want devbox", rec.Slug)
	}
	if removedPubKey != "abcdef1234567890" {
		t.Fatalf("removed pubkey = %q, want abcdef1234567890", removedPubKey)
	}
	if _, err := m.Get(context.Background(), "devbox"); err == nil {
		t.Fatalf("sandbox record still present after Delete()")
	}
}

func TestGuardDeletePathRejectsManagedHomeAndAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", home)

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	agentsRoot := filepath.Join(home, "Sky10", "Drives", "Agents")
	now := time.Now().UTC().Format(time.RFC3339)
	m.records["hermes-2tpd"] = Record{
		Name:      "hermes-2tpd",
		Slug:      "hermes-2tpd",
		Provider:  providerLima,
		Template:  templateHermes,
		Status:    "ready",
		SharedDir: filepath.Join(agentsRoot, "hermes-2tpd"),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.GuardDeletePath(filepath.Join(agentsRoot, "hermes-2tpd")); err == nil {
		t.Fatal("GuardDeletePath() unexpectedly allowed deleting managed sandbox home")
	}
	if err := m.GuardDeletePath(agentsRoot); err == nil {
		t.Fatal("GuardDeletePath() unexpectedly allowed deleting ancestor of managed sandbox home")
	}
}

func TestGuardDeletePathAllowsDescendantAndUnrelatedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", home)

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	agentsRoot := filepath.Join(home, "Sky10", "Drives", "Agents")
	now := time.Now().UTC().Format(time.RFC3339)
	m.records["hermes-2tpd"] = Record{
		Name:      "hermes-2tpd",
		Slug:      "hermes-2tpd",
		Provider:  providerLima,
		Template:  templateHermes,
		Status:    "ready",
		SharedDir: filepath.Join(agentsRoot, "hermes-2tpd"),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.GuardDeletePath(filepath.Join(agentsRoot, "hermes-2tpd", "workspace")); err != nil {
		t.Fatalf("GuardDeletePath() blocked descendant path: %v", err)
	}
	if err := m.GuardDeletePath(filepath.Join(home, "tmp", "other")); err != nil {
		t.Fatalf("GuardDeletePath() blocked unrelated path: %v", err)
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
	want := filepath.Join(home, "Sky10", "Drives", "Agents", "devbox")
	if got != want {
		t.Fatalf("defaultSharedDir() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Dir(got)); !os.IsNotExist(err) {
		t.Fatalf("shared dir parent should not be created eagerly")
	}
}

func TestEnsureLocalAgentDriveConfigUsesAgentsDriveRoot(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	driveHome := t.TempDir()
	sharedDir := filepath.Join(driveHome, "Sky10", "Drives", "Agents", "devbox")
	if err := ensureLocalAgentDriveConfig("devbox", sharedDir); err != nil {
		t.Fatalf("ensureLocalAgentDriveConfig() error: %v", err)
	}

	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error: %v", err)
	}
	manager := skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	drives := manager.ListDrives()
	if len(drives) != 1 {
		t.Fatalf("drive count = %d, want 1", len(drives))
	}
	if drives[0].Name != agentDriveRootName {
		t.Fatalf("drive name = %q, want %q", drives[0].Name, agentDriveRootName)
	}
	if drives[0].Namespace != agentDriveRootName {
		t.Fatalf("drive namespace = %q, want %q", drives[0].Namespace, agentDriveRootName)
	}
	if drives[0].LocalPath != filepath.Join(driveHome, "Sky10", "Drives", "Agents") {
		t.Fatalf("drive path = %q, want root agent drive", drives[0].LocalPath)
	}
}

func TestEnsureLocalAgentDriveConfigReplacesLegacyPerAgentDrive(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	driveHome := t.TempDir()
	sharedDir := filepath.Join(driveHome, "Sky10", "Drives", "Agents", "devbox")

	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error: %v", err)
	}
	manager := skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	if _, err := manager.CreateDrive(legacyAgentDriveName("devbox"), sharedDir, legacyAgentDriveName("devbox")); err != nil {
		t.Fatalf("CreateDrive(legacy) error: %v", err)
	}

	if err := ensureLocalAgentDriveConfig("devbox", sharedDir); err != nil {
		t.Fatalf("ensureLocalAgentDriveConfig() error: %v", err)
	}

	manager = skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json"))
	drives := manager.ListDrives()
	if len(drives) != 1 {
		t.Fatalf("drive count = %d, want 1", len(drives))
	}
	if drives[0].Name != agentDriveRootName {
		t.Fatalf("drive name = %q, want %q", drives[0].Name, agentDriveRootName)
	}
	if drives[0].LocalPath != filepath.Join(driveHome, "Sky10", "Drives", "Agents") {
		t.Fatalf("drive path = %q, want root agent drive", drives[0].LocalPath)
	}
}

func TestEnsureAgentHomeUsesHostDriveRoot(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	driveHome := t.TempDir()
	sharedDir := filepath.Join(driveHome, "Sky10", "Drives", "Agents", "devbox")

	var calls []string
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		switch method {
		case "skyfs.driveList":
			calls = append(calls, "list")
			body, err := json.Marshal(map[string]interface{}{
				"drives": []map[string]string{{
					"id":         "legacy-devbox",
					"name":       legacyAgentDriveName("devbox"),
					"local_path": sharedDir,
				}},
			})
			if err != nil {
				t.Fatalf("json.Marshal(driveList) error: %v", err)
			}
			return json.Unmarshal(body, out)
		case "skyfs.driveRemove":
			calls = append(calls, "remove")
			removeParams, ok := params.(map[string]string)
			if !ok {
				t.Fatalf("driveRemove params type = %T, want map[string]string", params)
			}
			if removeParams["id"] != "legacy-devbox" {
				t.Fatalf("driveRemove id = %q, want legacy-devbox", removeParams["id"])
			}
			return nil
		case "skyfs.driveCreate":
			calls = append(calls, "create")
			createParams, ok := params.(map[string]string)
			if !ok {
				t.Fatalf("driveCreate params type = %T, want map[string]string", params)
			}
			if createParams["name"] != agentDriveRootName {
				t.Fatalf("driveCreate name = %q, want %q", createParams["name"], agentDriveRootName)
			}
			if createParams["namespace"] != agentDriveRootName {
				t.Fatalf("driveCreate namespace = %q, want %q", createParams["namespace"], agentDriveRootName)
			}
			if createParams["path"] != filepath.Join(driveHome, "Sky10", "Drives", "Agents") {
				t.Fatalf("driveCreate path = %q, want root agent drive", createParams["path"])
			}
			return nil
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
	}

	if err := m.ensureAgentHome(context.Background(), "devbox", sharedDir); err != nil {
		t.Fatalf("ensureAgentHome() error: %v", err)
	}

	if strings.Join(calls, "\n") != strings.Join([]string{"list", "remove", "create"}, "\n") {
		t.Fatalf("calls = %v, want [list remove create]", calls)
	}
}

func TestRenderSandboxTemplate(t *testing.T) {
	t.Parallel()

	body := []byte(`name=__SKY10_SANDBOX_NAME__ path=__SKY10_SHARED_DIR__ state=__SKY10_STATE_DIR__ port=__SKY10_GUEST_FORWARD_PORT__ gateway=__SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__`)
	got := string(renderSandboxTemplate(body, "devbox", "/Users/bf/Sky10/Drives/Agents/devbox", "/Users/bf/.sky10/sandboxes/devbox/state", 39123))

	if strings.Contains(got, templateNameToken) || strings.Contains(got, templateSharedToken) || strings.Contains(got, templateStateToken) || strings.Contains(got, templateForwardedGuestPortToken) || strings.Contains(got, templateOpenClawGatewayPortToken) {
		t.Fatalf("renderSandboxTemplate() left placeholder tokens behind: %q", got)
	}
	if !strings.Contains(got, "devbox") {
		t.Fatalf("renderSandboxTemplate() missing sandbox name: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/Sky10/Drives/Agents/devbox") {
		t.Fatalf("renderSandboxTemplate() missing shared dir: %q", got)
	}
	if !strings.Contains(got, "/Users/bf/.sky10/sandboxes/devbox/state") {
		t.Fatalf("renderSandboxTemplate() missing state dir: %q", got)
	}
	if !strings.Contains(got, "39123") {
		t.Fatalf("renderSandboxTemplate() missing forwarded port: %q", got)
	}
	if !strings.Contains(got, "39124") {
		t.Fatalf("renderSandboxTemplate() missing OpenClaw gateway forwarded port: %q", got)
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
	assertManagedLimaTemplateForwardsGuestSky10(t, text)
	assertManagedLimaTemplateForwardsOpenClawGateway(t, text)
}

func TestBundledManagedLimaTemplatesForwardGuestEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		asset               string
		wantOpenClawGateway bool
	}{
		{asset: templateOpenClawYAML, wantOpenClawGateway: true},
		{asset: templateOpenClawDockerYAML, wantOpenClawGateway: true},
		{asset: templateHermesYAML},
		{asset: templateHermesDockerYAML},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.asset, func(t *testing.T) {
			t.Parallel()

			body, err := readBundledTemplateAsset(tc.asset)
			if err != nil {
				t.Fatalf("readBundledTemplateAsset(%q) error: %v", tc.asset, err)
			}
			text := string(body)
			assertManagedLimaTemplateForwardsGuestSky10(t, text)
			if tc.wantOpenClawGateway {
				assertManagedLimaTemplateForwardsOpenClawGateway(t, text)
			} else if strings.Contains(text, templateOpenClawGatewayPortToken) {
				t.Fatalf("managed Lima template unexpectedly forwards OpenClaw gateway")
			}
		})
	}
}

func TestBundledAgentTemplatesDefaultToOpus(t *testing.T) {
	t.Parallel()

	for _, asset := range []string{
		templateOpenClawYAML,
		templateOpenClawDockerYAML,
		templateHermesYAML,
		templateHermesDockerYAML,
	} {
		asset := asset
		t.Run(asset, func(t *testing.T) {
			t.Parallel()

			body, err := readBundledTemplateAsset(asset)
			if err != nil {
				t.Fatalf("readBundledTemplateAsset(%q) error: %v", asset, err)
			}
			text := string(body)
			if !strings.Contains(text, "model: anthropic/claude-opus-4-6") {
				t.Fatalf("%s default model = %q, want Opus 4.6", asset, text)
			}
			if strings.Contains(text, "claude-sonnet-4-6") {
				t.Fatalf("%s still references Sonnet 4.6: %q", asset, text)
			}
		})
	}
}

func assertManagedLimaTemplateForwardsGuestSky10(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"portForwards:",
		"- lima: user-v2",
		`guestIP: "127.0.0.1"`,
		"guestPort: 9101",
		`hostIP: "127.0.0.1"`,
		"hostPort: __SKY10_GUEST_FORWARD_PORT__",
		"proto: tcp",
		"guestIP: \"127.0.0.1\"\n  guestPortRange: [1, 65535]",
		"guestIP: \"0.0.0.0\"\n  guestPortRange: [1, 65535]",
		"proto: any",
		"ignore: true",
		"http://127.0.0.1:__SKY10_GUEST_FORWARD_PORT__",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed Lima template missing %q", want)
		}
	}
	for _, disallowed := range []string{
		"vzNAT: true",
		"http://<guest-ip>",
		"ip -4 addr show dev lima0",
	} {
		if strings.Contains(text, disallowed) {
			t.Fatalf("managed Lima template still contains %q", disallowed)
		}
	}
}

func assertManagedLimaTemplateForwardsOpenClawGateway(t *testing.T, text string) {
	t.Helper()

	for _, want := range []string{
		"guestPort: 18789",
		"hostPort: __SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__",
		"http://127.0.0.1:__SKY10_OPENCLAW_GATEWAY_FORWARD_PORT__",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("managed Lima template missing %q", want)
		}
	}
}

func TestPrepareOpenClawSharedDir(t *testing.T) {
	t.Parallel()

	sharedDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	helper := []byte("#!/bin/sh\n")
	pluginAssets := map[string][]byte{
		runtimeBundleOpenClawPluginManifest: []byte(`{"id":"sky10"}` + "\n"),
		runtimeBundleOpenClawPluginIndex:    []byte("export default function register() {}\n"),
		runtimeBundleOpenClawPluginMedia:    []byte("export function helper() {}\n"),
	}
	if err := prepareOpenClawSharedDir(sharedDir, stateDir, helper, pluginAssets, map[string]string{
		"OPENAI_API_KEY": "openai-key",
	}, &IdentityInvite{HostIdentity: "sky10-host", Code: "invite-code"}, AgentProfileSeed{
		DisplayName: "OpenClaw M8",
		Slug:        "openclaw-m8",
		Template:    templateOpenClaw,
	}); err != nil {
		t.Fatalf("prepareOpenClawSharedDir() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sharedDir, "workspace")); err != nil {
		t.Fatalf("Stat(agent workspace) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "sky10.md")); err != nil {
		t.Fatalf("Stat(sky10.md) error: %v", err)
	}
	assertSymlinkTarget(t, filepath.Join(sharedDir, "workspace", "SOUL.md"), filepath.Join("..", "SOUL.md"))

	envPath := filepath.Join(stateDir, ".env")
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

	helperPath := filepath.Join(stateDir, templateHostsHelper)
	helperData, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("ReadFile(hosts helper) error: %v", err)
	}
	if string(helperData) != string(helper) {
		t.Fatalf("hosts helper = %q, want %q", string(helperData), string(helper))
	}

	pluginManifestPath := filepath.Join(stateDir, "plugins", templateOpenClawPluginManifest)
	pluginManifestData, err := os.ReadFile(pluginManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(plugin manifest) error: %v", err)
	}
	if string(pluginManifestData) != string(pluginAssets[runtimeBundleOpenClawPluginManifest]) {
		t.Fatalf("plugin manifest = %q, want %q", string(pluginManifestData), string(pluginAssets[runtimeBundleOpenClawPluginManifest]))
	}

	pluginMediaPath := filepath.Join(stateDir, "plugins", templateOpenClawPluginMedia)
	pluginMediaData, err := os.ReadFile(pluginMediaPath)
	if err != nil {
		t.Fatalf("ReadFile(plugin media helper) error: %v", err)
	}
	if string(pluginMediaData) != string(pluginAssets[runtimeBundleOpenClawPluginMedia]) {
		t.Fatalf("plugin media helper = %q, want %q", string(pluginMediaData), string(pluginAssets[runtimeBundleOpenClawPluginMedia]))
	}

	invitePath := filepath.Join(stateDir, templateOpenClawInviteFile)
	inviteData, err := os.ReadFile(invitePath)
	if err != nil {
		t.Fatalf("ReadFile(join invite) error: %v", err)
	}
	var invite openClawJoinPayload
	if err := json.Unmarshal(inviteData, &invite); err != nil {
		t.Fatalf("json.Unmarshal(join invite) error: %v", err)
	}
	if invite.HostIdentity != "sky10-host" {
		t.Fatalf("invite host identity = %q, want sky10-host", invite.HostIdentity)
	}
	if invite.Code != "invite-code" {
		t.Fatalf("invite code = %q, want invite-code", invite.Code)
	}
	if strings.Contains(string(inviteData), "host_rpc_url") {
		t.Fatalf("join invite should not include host_rpc_url: %q", string(inviteData))
	}
	if strings.Contains(string(inviteData), "sandbox_slug") {
		t.Fatalf("join invite should not include sandbox_slug: %q", string(inviteData))
	}
}

func TestSandboxSecretBindingMaterializesEnvWithoutPersistingValue(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.SetSecretLookup(func(ctx context.Context, idOrName string) ([]byte, error) {
		switch idOrName {
		case "elevenlabs":
			return []byte("elevenlabs-key\n"), nil
		default:
			return nil, ErrProviderSecretNotFound
		}
	})

	now := time.Now().UTC().Format(time.RFC3339)
	rec := Record{
		Name:      "media-agent",
		Slug:      "media-agent",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "ready",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.records[rec.Slug] = rec
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		t.Fatalf("saveLocked() error: %v", err)
	}
	m.mu.Unlock()

	stateDir := m.sandboxStateDir(rec.Slug)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(stateDir) error: %v", err)
	}
	envPath := filepath.Join(stateDir, ".env")
	if err := os.WriteFile(envPath, []byte("MANUAL_FLAG=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env) error: %v", err)
	}

	updated, err := m.AttachSecret(context.Background(), "media-agent", SecretAttachParams{
		Env:    "ELEVENLABS_API_KEY",
		Secret: "elevenlabs",
	})
	if err != nil {
		t.Fatalf("AttachSecret() error: %v", err)
	}
	if len(updated.SecretBindings) != 1 {
		t.Fatalf("secret bindings len = %d, want 1", len(updated.SecretBindings))
	}
	if updated.SecretBindings[0].Secret != "elevenlabs" {
		t.Fatalf("secret binding secret = %q, want elevenlabs", updated.SecretBindings[0].Secret)
	}

	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile(.env) error: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"MANUAL_FLAG=1",
		managedSecretEnvBlockStart,
		"ELEVENLABS_API_KEY=elevenlabs-key",
		managedSecretEnvBlockEnd,
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env = %q, missing %q", envText, want)
		}
	}

	stateData, err := os.ReadFile(m.statePath())
	if err != nil {
		t.Fatalf("ReadFile(state) error: %v", err)
	}
	if strings.Contains(string(stateData), "elevenlabs-key") {
		t.Fatalf("sandbox state persisted secret payload: %q", string(stateData))
	}

	updated, err = m.DetachSecret(context.Background(), "media-agent", SecretDetachParams{
		Env: "ELEVENLABS_API_KEY",
	})
	if err != nil {
		t.Fatalf("DetachSecret() error: %v", err)
	}
	if len(updated.SecretBindings) != 0 {
		t.Fatalf("secret bindings len after detach = %d, want 0", len(updated.SecretBindings))
	}
	envData, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("ReadFile(.env after detach) error: %v", err)
	}
	envText = string(envData)
	if !strings.Contains(envText, "MANUAL_FLAG=1") {
		t.Fatalf(".env after detach dropped manual flag: %q", envText)
	}
	if strings.Contains(envText, "ELEVENLABS_API_KEY") || strings.Contains(envText, managedSecretEnvBlockStart) {
		t.Fatalf(".env after detach still has managed secret block: %q", envText)
	}
}

func TestBundledHermesUserScriptKeepsSharedEnv(t *testing.T) {
	t.Parallel()

	body, err := readBundledTemplateAsset(templateHermesUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateHermesUser, err)
	}

	script := string(body)
	if !strings.Contains(script, "merge_guest_env_into_shared") {
		t.Fatalf("bundled Hermes user script missing guest-env merge helper: %q", script)
	}
	if !strings.Contains(script, "shared_agent_file_is_seed") {
		t.Fatalf("bundled Hermes user script missing seeded-profile detection helper: %q", script)
	}
	if !strings.Contains(script, `preserve_guest_agent_path "${source}" "${target}"`) {
		t.Fatalf("bundled Hermes user script missing guest profile preservation before relink: %q", script)
	}
	if !strings.Contains(script, "guest-profile-backup") {
		t.Fatalf("bundled Hermes user script missing guest profile backup path: %q", script)
	}
	if !strings.Contains(script, ".env.example") {
		t.Fatalf("bundled Hermes user script missing example env comparison: %q", script)
	}
	if !strings.Contains(script, `ln -sfn "${SANDBOX_STATE_DIR}/.env" "${HERMES_HOME}/.env"`) {
		t.Fatalf("bundled Hermes user script missing sandbox env symlink: %q", script)
	}
	if !strings.Contains(script, "hermes-sky10-bridge.py") {
		t.Fatalf("bundled Hermes user script missing bridge asset install: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-gateway.service") {
		t.Fatalf("bundled Hermes user script missing gateway service unit: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-bridge.service") {
		t.Fatalf("bundled Hermes user script missing bridge service unit: %q", script)
	}
	if strings.Contains(script, "sky10-managed-reconnect") {
		t.Fatalf("bundled Hermes user script should not install guest reconnect helper: %q", script)
	}
	if !strings.Contains(script, `mkdir -p "${HOME}/.bin"`) {
		t.Fatalf("bundled Hermes user script missing ~/.bin bootstrap dir creation: %q", script)
	}
	if !strings.Contains(script, "sky10.service") {
		t.Fatalf("bundled Hermes user script missing guest sky10 service unit: %q", script)
	}
	if !strings.Contains(script, "Environment=SKY10_SANDBOX_GUEST=1") {
		t.Fatalf("bundled Hermes user script missing guest x402 bridge-only marker: %q", script)
	}
	if !strings.Contains(script, "hermes config set auxiliary.vision.provider main") {
		t.Fatalf("bundled Hermes user script missing auxiliary vision config: %q", script)
	}
	if strings.Contains(script, "sky10 join --role sandbox") {
		t.Fatalf("bundled Hermes user script should not join the host identity during boot: %q", script)
	}
	if !strings.Contains(script, "hermes gateway run") {
		t.Fatalf("bundled Hermes user script missing gateway foreground command: %q", script)
	}
	if !strings.Contains(script, "API_SERVER_ENABLED=true") {
		t.Fatalf("bundled Hermes user script missing API server env bootstrap: %q", script)
	}
	if !strings.Contains(script, `Environment=MESSAGING_CWD=/shared/workspace`) {
		t.Fatalf("bundled Hermes user script missing messaging cwd override: %q", script)
	}
	if !strings.Contains(script, "SKY10_BRIDGE_CONFIG_PATH=/sandbox-state/bridge.json") {
		t.Fatalf("bundled Hermes user script missing bridge config path: %q", script)
	}
	if !strings.Contains(script, `link_agent_file "${SHARED_DIR}/SOUL.md" "${HERMES_HOME}/SOUL.md"`) {
		t.Fatalf("bundled Hermes user script missing SOUL.md root link: %q", script)
	}
	if !strings.Contains(script, `link_agent_file "${SHARED_DIR}/MEMORY.md" "${HERMES_HOME}/memories/MEMORY.md"`) {
		t.Fatalf("bundled Hermes user script missing MEMORY.md root link: %q", script)
	}
	if !strings.Contains(script, "hermes config set terminal.cwd /shared/workspace") {
		t.Fatalf("bundled Hermes user script missing shared workspace cwd config: %q", script)
	}
	if strings.Contains(script, "HERMES.md") {
		t.Fatalf("bundled Hermes user script should not seed welcome docs into the shared workspace: %q", script)
	}
	if got := strings.Count(script, "link_hermes_env"); got < 3 {
		t.Fatalf("bundled Hermes user script should relink shared env after Hermes config writes, count=%d: %q", got, script)
	}
	if strings.Contains(script, `cp "${HERMES_HOME}/.env" "${SHARED_DIR}/.env"`) {
		t.Fatalf("bundled Hermes user script should not clobber shared env with guest env: %q", script)
	}
}

func TestBundledHermesUserScriptRelinksExistingGuestProfileFiles(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Hermes Lima template shell helpers require bash")
	}

	body, err := readBundledTemplateAsset(templateHermesUser)
	if err != nil {
		t.Fatalf("readBundledTemplateAsset(%q) error: %v", templateHermesUser, err)
	}

	script := string(body)
	start := strings.Index(script, "shared_agent_file_is_seed() {")
	if start == -1 {
		t.Fatal("shared_agent_file_is_seed() not found in bundled Hermes user script")
	}
	end := strings.Index(script, "\nwrite_helper() {")
	if end == -1 || end <= start {
		t.Fatal("write_helper() marker not found after Hermes profile helpers")
	}
	helperScript := script[start:end]

	root := t.TempDir()
	sharedDir := filepath.Join(root, "shared")
	stateDir := filepath.Join(root, "state")
	hermesHome := filepath.Join(root, "home", ".hermes")

	if err := os.MkdirAll(filepath.Join(hermesHome, "memories"), 0o755); err != nil {
		t.Fatalf("MkdirAll(hermes memories) error: %v", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(stateDir) error: %v", err)
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(sharedDir) error: %v", err)
	}

	seedSoul := strings.TrimSpace(`
# Soul

This file defines the durable identity for Hermes Dev.

## Role

Describe who this agent is and what it should optimize for in the hermes runtime.

## Tone

Describe how the agent should communicate.

## Boundaries

Describe what the agent should avoid, when it should escalate, and what humans own.
`) + "\n"
	seedMemory := strings.TrimSpace(`
# Memory

Use this file for durable facts that should survive model, runtime, and machine changes.

- Project conventions worth carrying forward
- Recurring tasks or preferences
- Useful environment facts
`) + "\n"

	if err := os.WriteFile(filepath.Join(sharedDir, "SOUL.md"), []byte(seedSoul), 0o644); err != nil {
		t.Fatalf("WriteFile(shared soul) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "MEMORY.md"), []byte(seedMemory), 0o644); err != nil {
		t.Fatalf("WriteFile(shared memory) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "USER.md"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile(shared USER) error: %v", err)
	}

	wantSoul := "# Soul\n\nKeep replies terse and biased toward shipping.\n"
	wantMemory := "# Memory\n\n- Remember the deployment bucket is west-2.\n"
	wantUser := "# User\n\n- Prefers direct answers without filler.\n"

	if err := os.WriteFile(filepath.Join(hermesHome, "SOUL.md"), []byte(wantSoul), 0o644); err != nil {
		t.Fatalf("WriteFile(guest SOUL) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hermesHome, "memories", "MEMORY.md"), []byte(wantMemory), 0o644); err != nil {
		t.Fatalf("WriteFile(guest MEMORY) error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hermesHome, "memories", "USER.md"), []byte(wantUser), 0o644); err != nil {
		t.Fatalf("WriteFile(guest USER) error: %v", err)
	}

	cmd := exec.Command("bash", "-lc", "set -euo pipefail\nSTATE_DIR=\"${HERMES_HOME}/.sky10-lima\"\nmkdir -p \"${STATE_DIR}\"\n"+helperScript+"\nlink_hermes_profile\n")
	cmd.Env = append(os.Environ(),
		"SHARED_DIR="+sharedDir,
		"SANDBOX_STATE_DIR="+stateDir,
		"HERMES_HOME="+hermesHome,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash relink helpers failed: %v\n%s", err, string(out))
	}

	for _, tc := range []struct {
		target string
		source string
		want   string
	}{
		{
			target: filepath.Join(hermesHome, "SOUL.md"),
			source: filepath.Join(sharedDir, "SOUL.md"),
			want:   wantSoul,
		},
		{
			target: filepath.Join(hermesHome, "memories", "MEMORY.md"),
			source: filepath.Join(sharedDir, "MEMORY.md"),
			want:   wantMemory,
		},
		{
			target: filepath.Join(hermesHome, "memories", "USER.md"),
			source: filepath.Join(sharedDir, "USER.md"),
			want:   wantUser,
		},
	} {
		linkTarget, err := os.Readlink(tc.target)
		if err != nil {
			t.Fatalf("Readlink(%q) error: %v", tc.target, err)
		}
		if linkTarget != tc.source {
			t.Fatalf("%q -> %q, want %q", tc.target, linkTarget, tc.source)
		}
		body, err := os.ReadFile(tc.source)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", tc.source, err)
		}
		if string(body) != tc.want {
			t.Fatalf("%q = %q, want %q", tc.source, string(body), tc.want)
		}
	}

	if _, err := os.Stat(filepath.Join(stateDir, "guest-profile-backup")); !os.IsNotExist(err) {
		t.Fatalf("guest profile backup dir = %v, want not created for seeded profile migration", err)
	}
}

func TestBundledHermesBridgeAssetRegistersWithSky10(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleHermesBridgeAsset)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(%q) error: %v", runtimeBundleHermesBridgeAsset, err)
	}

	script := string(body)
	if !strings.Contains(script, `"agent.register"`) {
		t.Fatalf("bundled Hermes bridge missing sky10 registration call: %q", script)
	}
	if !strings.Contains(script, "/rpc/events") {
		t.Fatalf("bundled Hermes bridge missing SSE subscription: %q", script)
	}
	if strings.Contains(script, "host_rpc_url") {
		t.Fatalf("bundled Hermes bridge should not accept legacy host_rpc_url config: %q", script)
	}
	if !strings.Contains(script, `DEFAULT_X402_ENDPOINT_PATH = "/bridge/metered-services/ws"`) {
		t.Fatalf("bundled Hermes bridge missing guest-local x402 bridge endpoint: %q", script)
	}
	if !strings.Contains(script, `"name": "sky10.x402"`) {
		t.Fatalf("bundled Hermes bridge missing x402 tool registration: %q", script)
	}
	if !strings.Contains(script, "install_x402_helper") {
		t.Fatalf("bundled Hermes bridge missing x402 helper install path: %q", script)
	}
	if !strings.Contains(script, "/responses") {
		t.Fatalf("bundled Hermes bridge missing Hermes Responses API call: %q", script)
	}
	if !strings.Contains(script, "/chat/completions") {
		t.Fatalf("bundled Hermes bridge missing chat completions fallback: %q", script)
	}
	if !strings.Contains(script, "def stream(self, session_id: str, content: Any, on_delta") {
		t.Fatalf("bundled Hermes bridge missing Hermes streaming entrypoint: %q", script)
	}
	if !strings.Contains(script, "self.sky10.send_delta(") {
		t.Fatalf("bundled Hermes bridge missing delta send path: %q", script)
	}
	if !strings.Contains(script, "self.sky10.send_done(") {
		t.Fatalf("bundled Hermes bridge missing done send path: %q", script)
	}
	if !strings.Contains(script, `"stream_id": stream_id`) {
		t.Fatalf("bundled Hermes bridge missing stream_id propagation: %q", script)
	}
	if !strings.Contains(script, "build_inbound_body") {
		t.Fatalf("bundled Hermes bridge missing structured inbound content builder: %q", script)
	}
	if !strings.Contains(script, "stage_base64_part") {
		t.Fatalf("bundled Hermes bridge missing attachment staging helper: %q", script)
	}
	if !strings.Contains(script, "sky10-hermes-media") {
		t.Fatalf("bundled Hermes bridge missing guest-local media staging root: %q", script)
	}
	if !strings.Contains(script, "build_outbound_content") {
		t.Fatalf("bundled Hermes bridge missing outbound content builder: %q", script)
	}
	if !strings.Contains(script, `trimmed.startswith("MEDIA:")`) {
		t.Fatalf("bundled Hermes bridge missing MEDIA artifact extraction: %q", script)
	}
	if !strings.Contains(script, "media_part_from_file") {
		t.Fatalf("bundled Hermes bridge missing local artifact file encoding: %q", script)
	}
}

func TestBundledOpenClawChannelAssetSupportsStructuredAttachments(t *testing.T) {
	t.Parallel()

	body, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginIndex)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(openclaw index) error: %v", err)
	}

	script := string(body)
	if !strings.Contains(script, `extractInboundMediaContext`) {
		t.Fatalf("bundled OpenClaw channel missing inbound media extraction: %q", script)
	}
	if !strings.Contains(script, `MediaPaths`) {
		t.Fatalf("bundled OpenClaw channel missing MediaPaths propagation: %q", script)
	}
	if !strings.Contains(script, `buildOutboundChatContent`) {
		t.Fatalf("bundled OpenClaw channel missing outbound chat content builder: %q", script)
	}
	if !strings.Contains(script, `hasMedia ? "chat" : "text"`) {
		t.Fatalf("bundled OpenClaw channel missing chat reply type selection: %q", script)
	}

	mediaBody, err := readBundledRuntimeBundleAsset(runtimeBundleOpenClawPluginMedia)
	if err != nil {
		t.Fatalf("readBundledRuntimeBundleAsset(openclaw media helper) error: %v", err)
	}
	mediaScript := string(mediaBody)
	if !strings.Contains(mediaScript, `trimmed.startsWith("MEDIA:")`) {
		t.Fatalf("bundled OpenClaw media helper missing MEDIA artifact handling: %q", mediaScript)
	}
	if !strings.Contains(mediaScript, `.openclaw", "media", "inbound", "sky10"`) {
		t.Fatalf("bundled OpenClaw media helper missing guest media staging root: %q", mediaScript)
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
		Template: templateUbuntu,
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

func TestWaitForGuestHermesCLI(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := waitForGuestHermesCLI(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("not ready")
		}
		return []byte("ok"), nil
	}, "/tmp/fake/limactl", "agent-123", 5*time.Second)
	if err != nil {
		t.Fatalf("waitForGuestHermesCLI() error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("waitForGuestHermesCLI() attempts = %d, want 3", attempts)
	}
}

func TestWaitForGuestHermesAgent(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := waitForGuestHermesAgent(context.Background(), func(ctx context.Context, bin string, args []string) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("not ready")
		}
		return []byte("ok"), nil
	}, "/tmp/fake/limactl", "agent-123", 5*time.Second)
	if err != nil {
		t.Fatalf("waitForGuestHermesAgent() error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("waitForGuestHermesAgent() attempts = %d, want 3", attempts)
	}
}

func TestFinishReadyHermesConnectsGuestSky10Agent(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["hermes-dev"] = Record{
		Name:          "Hermes Dev",
		Slug:          "hermes-dev",
		Provider:      providerLima,
		Template:      templateHermes,
		Status:        "starting",
		SharedDir:     filepath.Join(t.TempDir(), "shared"),
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var steps []string
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) >= 6 && args[0] == "shell" {
			script := args[len(args)-1]
			switch {
			case strings.Contains(script, `command -v hermes`):
				steps = append(steps, "guest-hermes-cli")
				return []byte("ok"), nil
			case strings.Contains(script, guestSky10ReadyURL):
				steps = append(steps, "guest-sky10-health")
				return []byte("ok"), nil
			case strings.Contains(script, `"method":"agent.list"`):
				steps = append(steps, "guest-agent-list")
				return []byte("ok"), nil
			case strings.Contains(script, "route get 1.1.1.1"):
				steps = append(steps, "lookup-ip")
				return []byte("192.168.64.24\n"), nil
			}
		}
		return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
	}
	m.hostIdentity = func(context.Context) (string, error) {
		steps = append(steps, "host-identity")
		return "sky10-host", nil
	}
	m.issueIdentityInvite = func(context.Context) (*IdentityInvite, error) {
		steps = append(steps, "issue-invite")
		return &IdentityInvite{HostIdentity: "sky10-host", Code: "invite-code"}, nil
	}
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		steps = append(steps, "host."+method)
		switch method {
		case "skylink.connect":
			return nil
		case "agent.list":
			body, err := json.Marshal(map[string]interface{}{
				"agents": []map[string]string{{"name": "Hermes Dev"}},
			})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
		}
		return nil
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		steps = append(steps, method)
		if address != "http://127.0.0.1:39101" {
			t.Fatalf("guest RPC address = %q, want forwarded URL", address)
		}
		switch method {
		case "identity.show":
			body, err := json.Marshal(map[string]interface{}{"address": "sky10-host"})
			if err != nil {
				t.Fatalf("marshal guest identity show: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
		}
		return nil
	}

	if err := m.finishReady(context.Background(), "hermes-dev", "/tmp/fake/limactl"); err != nil {
		t.Fatalf("finishReady() error: %v", err)
	}

	want := []string{
		"guest-hermes-cli",
		"guest-sky10-health",
		"host-identity",
		"identity.show",
		"guest-agent-list",
		"host.agent.list",
		"lookup-ip",
	}
	if strings.Join(steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps = %v, want %v", steps, want)
	}

	got, err := m.Get(context.Background(), "hermes-dev")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Status != "ready" {
		t.Fatalf("status = %q, want ready", got.Status)
	}
	if got.IPAddress != "192.168.64.24" {
		t.Fatalf("ip address = %q, want 192.168.64.24", got.IPAddress)
	}
}
