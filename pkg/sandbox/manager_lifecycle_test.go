package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
	skyid "github.com/sky10/sky10/pkg/id"
)

func TestFinishReadyOpenClawJoinsGuestSky10Identity(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-m6"] = Record{
		Name:          "openclaw-m6",
		Slug:          "openclaw-m6",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		Status:        "starting",
		SharedDir:     filepath.Join(t.TempDir(), "shared"),
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var steps []string
	guestSky10Checks := 0
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) >= 6 && args[0] == "shell" {
			script := args[len(args)-1]
			switch {
			case strings.Contains(script, openClawReadyURL):
				steps = append(steps, "gateway-health")
				return []byte("ok"), nil
			case strings.Contains(script, guestSky10ReadyURL):
				guestSky10Checks++
				steps = append(steps, fmt.Sprintf("guest-health-%d", guestSky10Checks))
				return []byte("ok"), nil
			case strings.Contains(script, `"method":"agent.list"`):
				steps = append(steps, "agent-list")
				return []byte("ok"), nil
			case strings.Contains(script, "route get 1.1.1.1"):
				steps = append(steps, "lookup-ip")
				return []byte("192.168.64.14\n"), nil
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
				"agents": []map[string]string{{"name": "openclaw-m6"}},
			})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
	}

	var joinParams map[string]string
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		steps = append(steps, method)
		if address != "http://127.0.0.1:39101" {
			t.Fatalf("guest RPC address = %q, want forwarded URL", address)
		}
		switch method {
		case "identity.show":
			body, err := json.Marshal(map[string]interface{}{
				"address":       "guest-solo",
				"device_count":  1,
				"device_id":     "D-guest123",
				"device_pubkey": "abcdef1234567890",
			})
			if err != nil {
				t.Fatalf("marshal guest identity show: %v", err)
			}
			return json.Unmarshal(body, out)
		case "identity.join":
			var ok bool
			joinParams, ok = params.(map[string]string)
			if !ok {
				t.Fatalf("join params type = %T, want map[string]string", params)
			}
			body, err := json.Marshal(map[string]string{
				"device_id":     "D-guest123",
				"device_pubkey": "abcdef1234567890",
			})
			if err != nil {
				t.Fatalf("marshal guest identity join result: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
			return nil
		}
	}

	if err := m.finishReady(context.Background(), "openclaw-m6", "/tmp/fake/limactl"); err != nil {
		t.Fatalf("finishReady() error: %v", err)
	}

	if guestSky10Checks != 2 {
		t.Fatalf("guest sky10 health checks = %d, want 2", guestSky10Checks)
	}
	if joinParams["code"] != "invite-code" {
		t.Fatalf("join code = %q, want invite-code", joinParams["code"])
	}
	if joinParams["role"] != skyid.DeviceRoleSandbox {
		t.Fatalf("join role = %q, want %q", joinParams["role"], skyid.DeviceRoleSandbox)
	}

	want := []string{
		"gateway-health",
		"guest-health-1",
		"host-identity",
		"identity.show",
		"issue-invite",
		"identity.join",
		"guest-health-2",
		"agent-list",
		"host.skylink.connect",
		"host.agent.list",
		"lookup-ip",
	}
	if strings.Join(steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps = %v, want %v", steps, want)
	}

	got, err := m.Get(context.Background(), "openclaw-m6")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Status != "ready" {
		t.Fatalf("status = %q, want ready", got.Status)
	}
	if got.IPAddress != "192.168.64.14" {
		t.Fatalf("ip address = %q, want 192.168.64.14", got.IPAddress)
	}
	if got.GuestDeviceID != "D-guest123" {
		t.Fatalf("guest device id = %q, want D-guest123", got.GuestDeviceID)
	}
	if got.GuestDevicePubKey != "abcdef1234567890" {
		t.Fatalf("guest device pubkey = %q, want abcdef1234567890", got.GuestDevicePubKey)
	}
}

func TestFinishReadyOpenClawSkipsJoinWhenGuestAlreadyJoined(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-m6"] = Record{
		Name:          "openclaw-m6",
		Slug:          "openclaw-m6",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		Status:        "starting",
		SharedDir:     filepath.Join(t.TempDir(), "shared"),
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) >= 6 && args[0] == "shell" {
			script := args[len(args)-1]
			switch {
			case strings.Contains(script, openClawReadyURL):
				return []byte("ok"), nil
			case strings.Contains(script, guestSky10ReadyURL):
				return []byte("ok"), nil
			case strings.Contains(script, `"method":"agent.list"`):
				return []byte("ok"), nil
			case strings.Contains(script, "route get 1.1.1.1"):
				return []byte("192.168.64.14\n"), nil
			}
		}
		return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
	}

	m.hostIdentity = func(context.Context) (string, error) {
		return "sky10-host", nil
	}
	m.issueIdentityInvite = func(context.Context) (*IdentityInvite, error) {
		t.Fatal("issueIdentityInvite should not be called when the guest already matches the host identity")
		return nil, nil
	}
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		switch method {
		case "skylink.connect":
			return nil
		case "agent.list":
			body, err := json.Marshal(map[string]interface{}{
				"agents": []map[string]string{{"name": "openclaw-m6"}},
			})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		switch method {
		case "identity.show":
			body, err := json.Marshal(map[string]interface{}{
				"address":       "sky10-host",
				"device_count":  2,
				"device_id":     "D-guest456",
				"device_pubkey": "abcdef9999999999",
			})
			if err != nil {
				t.Fatalf("marshal guest identity show: %v", err)
			}
			return json.Unmarshal(body, out)
		case "identity.join":
			t.Fatal("identity.join should not be called when the guest already matches the host identity")
			return nil
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
			return nil
		}
	}

	if err := m.finishReady(context.Background(), "openclaw-m6", "/tmp/fake/limactl"); err != nil {
		t.Fatalf("finishReady() error: %v", err)
	}

	got, err := m.Get(context.Background(), "openclaw-m6")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.GuestDeviceID != "D-guest456" {
		t.Fatalf("guest device id = %q, want D-guest456", got.GuestDeviceID)
	}
	if got.GuestDevicePubKey != "abcdef9999999999" {
		t.Fatalf("guest device pubkey = %q, want abcdef9999999999", got.GuestDevicePubKey)
	}
}

func TestReconnectRunningOpenClawSandboxes(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-m8"] = Record{
		Name:          "openclaw-m8",
		Slug:          "openclaw-m8",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		Status:        "ready",
		VMStatus:      "Running",
		IPAddress:     "192.168.64.99",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		SharedDir:     filepath.Join(t.TempDir(), "openclaw-m8"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.records["ubuntu-devbox"] = Record{
		Name:      "ubuntu-devbox",
		Slug:      "ubuntu-devbox",
		Provider:  providerLima,
		Template:  templateUbuntu,
		Status:    "ready",
		VMStatus:  "Running",
		SharedDir: filepath.Join(t.TempDir(), "ubuntu-devbox"),
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.records["openclaw-stopped"] = Record{
		Name:      "openclaw-stopped",
		Slug:      "openclaw-stopped",
		Provider:  providerLima,
		Template:  templateOpenClaw,
		Status:    "stopped",
		VMStatus:  "Stopped",
		SharedDir: filepath.Join(t.TempDir(), "openclaw-stopped"),
		CreatedAt: now,
		UpdatedAt: now,
	}

	var steps []string
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list" && args[1] == "--json":
			return []byte(
				`{"name":"openclaw-m8","status":"Running"}` + "\n" +
					`{"name":"ubuntu-devbox","status":"Running"}` + "\n" +
					`{"name":"openclaw-stopped","status":"Stopped"}` + "\n",
			), nil
		default:
			return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
		}
	}
	m.hostIdentity = func(context.Context) (string, error) {
		steps = append(steps, "host-identity")
		return "sky10-host", nil
	}
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		steps = append(steps, "host."+method)
		switch method {
		case "skylink.connect":
			return nil
		case "agent.list":
			body, err := json.Marshal(map[string]interface{}{
				"agents": []map[string]string{{"name": "openclaw-m8"}},
			})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
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
			return nil
		}
	}

	if err := m.ReconnectRunningOpenClawSandboxes(context.Background()); err != nil {
		t.Fatalf("ReconnectRunningOpenClawSandboxes() error: %v", err)
	}

	want := []string{
		"host-identity",
		"identity.show",
		"host.skylink.connect",
		"host.agent.list",
	}
	if strings.Join(steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps = %v, want %v", steps, want)
	}

	got, err := m.Get(context.Background(), "openclaw-m8")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.IPAddress != "192.168.64.99" {
		t.Fatalf("ip address = %q, want unchanged 192.168.64.99", got.IPAddress)
	}
}

func TestRefreshRuntimeRemovesMissingSandboxAndGuestDevice(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-missing"] = Record{
		Name:              "openclaw-missing",
		Slug:              "openclaw-missing",
		Provider:          providerLima,
		Template:          templateOpenClaw,
		Status:            "ready",
		VMStatus:          "Running",
		GuestDeviceID:     "D-guest789",
		GuestDevicePubKey: "deadbeefcafefeed",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "list" && args[1] == "--json" {
			return []byte(`{"name":"other-sandbox","status":"Running"}` + "\n"), nil
		}
		return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
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

	listed, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(listed.Sandboxes) != 0 {
		t.Fatalf("List() sandboxes len = %d, want 0", len(listed.Sandboxes))
	}
	if removedPubKey != "deadbeefcafefeed" {
		t.Fatalf("removed pubkey = %q, want deadbeefcafefeed", removedPubKey)
	}
	if _, err := m.Get(context.Background(), "openclaw-missing"); err == nil {
		t.Fatalf("sandbox record still present after refresh cleanup")
	}
}

func TestReconnectRunningOpenClawSandboxesIncludesHermes(t *testing.T) {
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
		Status:        "error",
		VMStatus:      "Running",
		LastError:     "waiting for guest Hermes agent registration: signal: killed",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39103,
		SharedDir:     filepath.Join(t.TempDir(), "hermes-dev"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var steps []string
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list" && args[1] == "--json":
			return []byte(`{"name":"hermes-dev","status":"Running"}` + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
		}
	}
	m.hostIdentity = func(context.Context) (string, error) {
		steps = append(steps, "host-identity")
		return "sky10-host", nil
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
			return nil
		}
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		steps = append(steps, method)
		if address != "http://127.0.0.1:39103" {
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
			return nil
		}
	}

	if err := m.ReconnectRunningOpenClawSandboxes(context.Background()); err != nil {
		t.Fatalf("ReconnectRunningOpenClawSandboxes() error: %v", err)
	}

	want := []string{
		"host-identity",
		"identity.show",
		"host.skylink.connect",
		"host.agent.list",
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
	if got.LastError != "" {
		t.Fatalf("last error = %q, want empty", got.LastError)
	}
}

func TestRunManagedReconnectLoopRetriesAfterLaterGuestRecovery(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["openclaw-m8"] = Record{
		Name:          "openclaw-m8",
		Slug:          "openclaw-m8",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		Status:        "ready",
		VMStatus:      "Running",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		SharedDir:     filepath.Join(t.TempDir(), "openclaw-m8"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.reconnectSweepTimeout = 20 * time.Millisecond
	m.reconnectInterval = 10 * time.Millisecond

	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list" && args[1] == "--json":
			return []byte(`{"name":"openclaw-m8","status":"Running"}` + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
		}
	}
	m.hostIdentity = func(context.Context) (string, error) {
		return "sky10-host", nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		identityCalls int
		connectCalls  int
	)
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		switch method {
		case "skylink.connect":
			connectCalls++
			return nil
		case "agent.list":
			agents := []map[string]string{}
			if connectCalls > 0 {
				agents = append(agents, map[string]string{"name": "openclaw-m8"})
				cancel()
			}
			body, err := json.Marshal(map[string]interface{}{"agents": agents})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		if address != "http://127.0.0.1:39101" {
			t.Fatalf("guest RPC address = %q, want forwarded URL", address)
		}
		switch method {
		case "identity.show":
			identityCalls++
			if identityCalls == 1 {
				return fmt.Errorf("guest not ready yet")
			}
			body, err := json.Marshal(map[string]interface{}{"address": "sky10-host"})
			if err != nil {
				t.Fatalf("marshal guest identity show: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
			return nil
		}
	}

	done := make(chan struct{})
	go func() {
		m.RunManagedReconnectLoop(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("managed reconnect loop did not stop after successful retry")
	}

	if identityCalls < 2 {
		t.Fatalf("identity.show calls = %d, want at least 2", identityCalls)
	}
	if connectCalls == 0 {
		t.Fatalf("skylink.connect calls = %d, want at least 1", connectCalls)
	}
}

func TestReconnectRunningOpenClawSandboxesDoesNotBlockBehindSlowGuest(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	m.records["slow-openclaw"] = Record{
		Name:          "slow-openclaw",
		Slug:          "slow-openclaw",
		Provider:      providerLima,
		Template:      templateOpenClaw,
		Status:        "error",
		VMStatus:      "Running",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
		SharedDir:     filepath.Join(t.TempDir(), "slow-openclaw"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.records["fast-hermes"] = Record{
		Name:          "fast-hermes",
		Slug:          "fast-hermes",
		Provider:      providerLima,
		Template:      templateHermes,
		Status:        "error",
		VMStatus:      "Running",
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39103,
		SharedDir:     filepath.Join(t.TempDir(), "fast-hermes"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/" + string(id)}, nil
	}
	m.outputCmd = func(ctx context.Context, bin string, args []string) ([]byte, error) {
		switch {
		case len(args) >= 2 && args[0] == "list" && args[1] == "--json":
			return []byte(
				`{"name":"slow-openclaw","status":"Running"}` + "\n" +
					`{"name":"fast-hermes","status":"Running"}` + "\n",
			), nil
		}
		return nil, fmt.Errorf("unexpected outputCmd args: %v", args)
	}
	m.hostIdentity = func(context.Context) (string, error) {
		return "sky10-host", nil
	}

	connected := make(chan struct{})
	var closeConnected sync.Once
	m.hostRPC = func(ctx context.Context, method string, params interface{}, out interface{}) error {
		switch method {
		case "skylink.connect":
			closeConnected.Do(func() { close(connected) })
			return nil
		case "agent.list":
			agents := []map[string]string{}
			select {
			case <-connected:
				agents = append(agents, map[string]string{"name": "fast-hermes"})
			default:
			}
			body, err := json.Marshal(map[string]interface{}{"agents": agents})
			if err != nil {
				t.Fatalf("marshal host agent list: %v", err)
			}
			return json.Unmarshal(body, out)
		default:
			t.Fatalf("unexpected host RPC method %q", method)
			return nil
		}
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		switch address {
		case "http://127.0.0.1:39101":
			if method == "identity.show" {
				<-ctx.Done()
				return ctx.Err()
			}
			return fmt.Errorf("unexpected slow guest RPC method %q", method)
		case "http://127.0.0.1:39103":
			switch method {
			case "identity.show":
				body, err := json.Marshal(map[string]interface{}{"address": "sky10-host"})
				if err != nil {
					t.Fatalf("marshal guest identity show: %v", err)
				}
				return json.Unmarshal(body, out)
			default:
				return fmt.Errorf("unexpected fast guest RPC method %q", method)
			}
		default:
			return fmt.Errorf("guest RPC address = %q", address)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := m.ReconnectRunningOpenClawSandboxes(ctx); err != nil {
		t.Fatalf("ReconnectRunningOpenClawSandboxes() error: %v", err)
	}

	fast, err := m.Get(context.Background(), "fast-hermes")
	if err != nil {
		t.Fatalf("Get(fast-hermes) error: %v", err)
	}
	if fast.Status != "ready" {
		t.Fatalf("fast status = %q, want ready", fast.Status)
	}
	slow, err := m.Get(context.Background(), "slow-openclaw")
	if err != nil {
		t.Fatalf("Get(slow-openclaw) error: %v", err)
	}
	if slow.Status != "error" {
		t.Fatalf("slow status = %q, want error", slow.Status)
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
