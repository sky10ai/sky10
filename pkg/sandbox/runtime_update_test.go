package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestRuntimeStatusUsesGuestRPC(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.records["hermes-dev"] = Record{
		Name:          "Hermes Dev",
		Slug:          "hermes-dev",
		Provider:      providerLima,
		Template:      templateHermes,
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	}

	var methods []string
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		if address != "http://127.0.0.1:39101" {
			t.Fatalf("guest RPC address = %q, want forwarded URL", address)
		}
		methods = append(methods, method)
		switch method {
		case "system.health":
			return marshalInto(out, map[string]interface{}{
				"status":  "ok",
				"version": "v0.64.0",
				"uptime":  "12s",
			})
		case "system.update.status":
			return marshalInto(out, map[string]interface{}{
				"current":     "v0.64.0",
				"latest":      "v0.65.0",
				"ready":       true,
				"cli_staged":  true,
				"menu_staged": false,
			})
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
		}
		return nil
	}

	got, err := m.RuntimeStatus(context.Background(), "hermes-dev")
	if err != nil {
		t.Fatalf("RuntimeStatus() error: %v", err)
	}
	if !got.Reachable {
		t.Fatalf("Reachable = false, want true: %#v", got)
	}
	if got.Version != "v0.64.0" || got.HealthStatus != "ok" || got.Uptime != "12s" {
		t.Fatalf("health fields = %#v", got)
	}
	if got.UpdateStatus["latest"] != "v0.65.0" {
		t.Fatalf("update status = %#v", got.UpdateStatus)
	}
	wantMethods := []string{"system.health", "system.update.status"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("methods = %v, want %v", methods, wantMethods)
	}
}

func TestRuntimeStatusFallsBackToLegacySkyFSHealth(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.records["hermes-dev"] = Record{
		Name:          "Hermes Dev",
		Slug:          "hermes-dev",
		Provider:      providerLima,
		Template:      templateHermes,
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	}

	var methods []string
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		methods = append(methods, method)
		switch method {
		case "system.health":
			return errors.New("unknown method: system.health")
		case "skyfs.health":
			return marshalInto(out, map[string]string{"status": "ok", "version": "v0.63.0"})
		case "system.update.status":
			return marshalInto(out, map[string]interface{}{"current": "v0.63.0"})
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
		}
		return nil
	}

	got, err := m.RuntimeStatus(context.Background(), "hermes-dev")
	if err != nil {
		t.Fatalf("RuntimeStatus() error: %v", err)
	}
	if got.Version != "v0.63.0" {
		t.Fatalf("Version = %q, want v0.63.0", got.Version)
	}
	wantMethods := []string{"system.health", "skyfs.health", "system.update.status"}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("methods = %v, want %v", methods, wantMethods)
	}
}

func TestRuntimeStatusReportsUnreachableGuest(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.records["devbox"] = Record{
		Name:     "Devbox",
		Slug:     "devbox",
		Provider: providerLima,
		Template: templateUbuntu,
	}

	got, err := m.RuntimeStatus(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("RuntimeStatus() error: %v", err)
	}
	if got.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if got.Error != "guest RPC endpoint unavailable" {
		t.Fatalf("Error = %q, want guest RPC endpoint unavailable", got.Error)
	}
}

func TestRuntimeUpgradeTriggersGuestUpdate(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.records["hermes-dev"] = Record{
		Name:          "Hermes Dev",
		Slug:          "hermes-dev",
		Provider:      providerLima,
		Template:      templateHermes,
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	}

	var calls []string
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		if address != "http://127.0.0.1:39101" {
			t.Fatalf("guest RPC address = %q, want forwarded URL", address)
		}
		calls = append(calls, method)
		if method != "system.update" {
			t.Fatalf("unexpected guest RPC method %q", method)
		}
		return marshalInto(out, map[string]interface{}{"status": "checking"})
	}

	got, err := m.RuntimeUpgrade(context.Background(), "Hermes Dev")
	if err != nil {
		t.Fatalf("RuntimeUpgrade() error: %v", err)
	}
	if got.Status != "checking" {
		t.Fatalf("Status = %q, want checking", got.Status)
	}
	if got.Result["status"] != "checking" {
		t.Fatalf("Result = %#v", got.Result)
	}
	if !reflect.DeepEqual(calls, []string{"system.update"}) {
		t.Fatalf("calls = %v, want [system.update]", calls)
	}
}

func TestRPCDispatchesSandboxRuntimeMethods(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.records["hermes-dev"] = Record{
		Name:          "Hermes Dev",
		Slug:          "hermes-dev",
		Provider:      providerLima,
		Template:      templateHermes,
		ForwardedHost: "127.0.0.1",
		ForwardedPort: 39101,
	}
	m.guestRPC = func(ctx context.Context, address, method string, params interface{}, out interface{}) error {
		switch method {
		case "system.health":
			return marshalInto(out, map[string]string{"status": "ok", "version": "v0.64.0"})
		case "system.update.status":
			return marshalInto(out, map[string]interface{}{"current": "v0.64.0"})
		case "system.update":
			return marshalInto(out, map[string]string{"status": "checking"})
		default:
			t.Fatalf("unexpected guest RPC method %q", method)
		}
		return nil
	}

	h := NewRPCHandler(m)
	params := json.RawMessage(`{"slug":"hermes-dev"}`)
	statusResult, err, ok := h.Dispatch(context.Background(), "sandbox.runtime.status", params)
	if !ok || err != nil {
		t.Fatalf("status dispatch: ok=%v err=%v", ok, err)
	}
	if statusResult.(*RuntimeStatusResult).Version != "v0.64.0" {
		t.Fatalf("status result = %#v", statusResult)
	}

	upgradeResult, err, ok := h.Dispatch(context.Background(), "sandbox.runtime.upgrade", params)
	if !ok || err != nil {
		t.Fatalf("upgrade dispatch: ok=%v err=%v", ok, err)
	}
	if upgradeResult.(*RuntimeUpgradeResult).Status != "checking" {
		t.Fatalf("upgrade result = %#v", upgradeResult)
	}
}

func marshalInto(out interface{}, value interface{}) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}
