package id

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestRPCDeviceListUsesManifestAndMetadata(t *testing.T) {
	identity, _ := skykey.Generate()
	current, _ := skykey.Generate()
	other, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(current.PublicKey, "mac")
	manifest.AddDeviceWithRole(other.PublicKey, "linux", DeviceRoleSandbox)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("sign manifest: %v", err)
	}

	bundle, err := New(identity, current, manifest)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}

	handler := NewRPCHandler(bundle)
	handler.SetDeviceMetadataProvider(func(context.Context) (map[string]DeviceMetadata, error) {
		return map[string]DeviceMetadata{
			hex.EncodeToString(other.PublicKey): {
				Platform:   "Linux",
				LastSeen:   "2026-04-06T10:30:00Z",
				Multiaddrs: []string{"/ip4/203.0.113.10/tcp/4101/p2p/12D3KooWtest"},
			},
		}, nil
	})

	raw, err, handled := handler.Dispatch(context.Background(), "identity.deviceList", nil)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !handled {
		t.Fatal("identity.deviceList was not handled")
	}

	result, ok := raw.(deviceListResult)
	if !ok {
		t.Fatalf("result type = %T, want deviceListResult", raw)
	}
	if result.ThisDevice != bundle.DeviceID() {
		t.Fatalf("this_device = %q, want %q", result.ThisDevice, bundle.DeviceID())
	}
	if len(result.Devices) != 2 {
		t.Fatalf("device count = %d, want 2", len(result.Devices))
	}

	if !result.Devices[0].Current {
		t.Fatalf("first device should be current: %+v", result.Devices[0])
	}
	if result.Devices[0].Role != DeviceRoleTrusted {
		t.Fatalf("current device role = %q, want %q", result.Devices[0].Role, DeviceRoleTrusted)
	}
	if result.Devices[1].Role != DeviceRoleSandbox {
		t.Fatalf("other device role = %q, want %q", result.Devices[1].Role, DeviceRoleSandbox)
	}
	if result.Devices[1].Platform != "Linux" {
		t.Fatalf("platform = %q, want Linux", result.Devices[1].Platform)
	}
	if len(result.Devices[1].Multiaddrs) != 1 {
		t.Fatalf("multiaddrs = %v, want 1 address", result.Devices[1].Multiaddrs)
	}
}

func TestRPCDeviceRemoveParsesPubkey(t *testing.T) {
	identity, _ := skykey.Generate()
	current, _ := skykey.Generate()
	manifest := NewManifest(identity)
	manifest.AddDevice(current.PublicKey, "mac")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("sign manifest: %v", err)
	}

	bundle, err := New(identity, current, manifest)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}

	handler := NewRPCHandler(bundle)

	wantPubKey := hex.EncodeToString(current.PublicKey)
	called := false
	handler.SetDeviceRemoveHandler(func(_ context.Context, gotPubKey string) (interface{}, error) {
		called = true
		if gotPubKey != wantPubKey {
			t.Fatalf("pubkey = %q, want %q", gotPubKey, wantPubKey)
		}
		return map[string]string{"status": "ok"}, nil
	})

	params, _ := json.Marshal(map[string]string{"pubkey": wantPubKey})
	raw, err, handled := handler.Dispatch(context.Background(), "identity.deviceRemove", params)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !handled {
		t.Fatal("identity.deviceRemove was not handled")
	}
	if !called {
		t.Fatal("device remove handler was not called")
	}

	result, ok := raw.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", raw)
	}
	if result["status"] != "ok" {
		t.Fatalf("status = %q, want ok", result["status"])
	}
}

func TestRPCJoinParsesCode(t *testing.T) {
	identity, _ := skykey.Generate()
	current, _ := skykey.Generate()
	manifest := NewManifest(identity)
	manifest.AddDevice(current.PublicKey, "mac")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("sign manifest: %v", err)
	}

	bundle, err := New(identity, current, manifest)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}

	handler := NewRPCHandler(bundle)

	called := false
	handler.SetJoinHandler(func(_ context.Context, code, role string) (interface{}, error) {
		called = true
		if code != "sky10p2p_test" {
			t.Fatalf("code = %q, want sky10p2p_test", code)
		}
		if role != DeviceRoleSandbox {
			t.Fatalf("role = %q, want %q", role, DeviceRoleSandbox)
		}
		return map[string]string{"status": "joined"}, nil
	})

	params, _ := json.Marshal(map[string]string{"code": "  sky10p2p_test  ", "role": " sandbox "})
	raw, err, handled := handler.Dispatch(context.Background(), "identity.join", params)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !handled {
		t.Fatal("identity.join was not handled")
	}
	if !called {
		t.Fatal("join handler was not called")
	}

	result, ok := raw.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", raw)
	}
	if result["status"] != "joined" {
		t.Fatalf("status = %q, want joined", result["status"])
	}
}

func TestRPCInviteParsesMode(t *testing.T) {
	identity, _ := skykey.Generate()
	current, _ := skykey.Generate()
	manifest := NewManifest(identity)
	manifest.AddDevice(current.PublicKey, "mac")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("sign manifest: %v", err)
	}

	bundle, err := New(identity, current, manifest)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}

	handler := NewRPCHandler(bundle)

	called := false
	handler.SetInviteHandler(func(_ context.Context, options InviteOptions) (string, error) {
		called = true
		if options.Mode != InviteModeP2P {
			t.Fatalf("mode = %q, want %q", options.Mode, InviteModeP2P)
		}
		return "invite-code", nil
	})

	params, _ := json.Marshal(map[string]string{"mode": InviteModeP2P})
	raw, err, handled := handler.Dispatch(context.Background(), "identity.invite", params)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !handled {
		t.Fatal("identity.invite was not handled")
	}
	if !called {
		t.Fatal("invite handler was not called")
	}

	result, ok := raw.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", raw)
	}
	if result["code"] != "invite-code" {
		t.Fatalf("code = %q, want invite-code", result["code"])
	}
}

func TestRPCDevicesFormatsTimestampsUTC(t *testing.T) {
	identity, _ := skykey.Generate()
	current, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.Devices = []DeviceEntry{{
		PublicKey: current.PublicKey,
		Name:      "mac",
		Role:      DeviceRoleSandbox,
		AddedAt:   time.Date(2026, 4, 6, 15, 4, 5, 0, time.FixedZone("CDT", -5*60*60)),
	}}
	manifest.UpdatedAt = time.Now().UTC()
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("sign manifest: %v", err)
	}

	bundle, err := New(identity, current, manifest)
	if err != nil {
		t.Fatalf("new bundle: %v", err)
	}

	handler := NewRPCHandler(bundle)
	raw, err, handled := handler.Dispatch(context.Background(), "identity.devices", nil)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !handled {
		t.Fatal("identity.devices was not handled")
	}

	result := raw.(devicesResult)
	if result.Devices[0].AddedAt != "2026-04-06T20:04:05Z" {
		t.Fatalf("added_at = %q, want UTC timestamp", result.Devices[0].AddedAt)
	}
	if result.Devices[0].Role != DeviceRoleSandbox {
		t.Fatalf("role = %q, want %q", result.Devices[0].Role, DeviceRoleSandbox)
	}
}
