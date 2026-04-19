package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sky10/sky10/pkg/adapter"
	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skydevice "github.com/sky10/sky10/pkg/device"
	"github.com/sky10/sky10/pkg/join"
)

func TestRPCInviteUsesConfiguredRelaysWithoutStorage(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	wantRelays := []string{"wss://relay.one", "wss://relay.two"}
	if err := config.Save(&config.Config{NostrRelays: wantRelays}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	handler := newTestInviteHandler(t, nil)

	raw, err := handler.rpcInvite(context.Background())
	if err != nil {
		t.Fatalf("rpcInvite: %v", err)
	}

	result, ok := raw.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", raw)
	}

	invite, err := join.DecodeP2PInvite(result["code"])
	if err != nil {
		t.Fatalf("DecodeP2PInvite: %v", err)
	}
	if invite.Address != handler.store.identity.Address() {
		t.Fatalf("invite address = %q, want %q", invite.Address, handler.store.identity.Address())
	}
	if !reflect.DeepEqual(invite.NostrRelays, wantRelays) {
		t.Fatalf("invite relays = %v, want %v", invite.NostrRelays, wantRelays)
	}
}

func TestRPCInviteWithStorageUsesConfigAndWritesWaitingMarker(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv("S3_ACCESS_KEY_ID", "test-access")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")
	if err := config.Save(&config.Config{
		Bucket:         "bucket-a",
		Region:         "us-east-1",
		Endpoint:       "https://s3.example.com",
		ForcePathStyle: true,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	backend := s3adapter.NewMemory()
	handler := newTestInviteHandler(t, backend)

	raw, err := handler.rpcInvite(context.Background())
	if err != nil {
		t.Fatalf("rpcInvite: %v", err)
	}

	result, ok := raw.(map[string]string)
	if !ok {
		t.Fatalf("result type = %T, want map[string]string", raw)
	}

	invite, err := join.DecodeS3Invite(result["code"])
	if err != nil {
		t.Fatalf("DecodeS3Invite: %v", err)
	}
	if invite.Endpoint != "https://s3.example.com" {
		t.Fatalf("endpoint = %q, want https://s3.example.com", invite.Endpoint)
	}
	if invite.Bucket != "bucket-a" {
		t.Fatalf("bucket = %q, want bucket-a", invite.Bucket)
	}
	if invite.Region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", invite.Region)
	}
	if invite.AccessKey != "test-access" {
		t.Fatalf("access key = %q, want test-access", invite.AccessKey)
	}
	if invite.SecretKey != "test-secret" {
		t.Fatalf("secret key = %q, want test-secret", invite.SecretKey)
	}
	if !invite.ForcePathStyle {
		t.Fatal("force_path_style = false, want true")
	}
	if invite.DevicePubKey != handler.store.identity.Address() {
		t.Fatalf("device pubkey = %q, want %q", invite.DevicePubKey, handler.store.identity.Address())
	}

	waitingKey := "invites/" + invite.InviteID + "/waiting"
	if _, err := backend.Head(context.Background(), waitingKey); err != nil {
		t.Fatalf("waiting marker %q missing: %v", waitingKey, err)
	}
}

func TestRPCJoinWithoutStorageReturnsP2PHint(t *testing.T) {
	t.Parallel()

	handler := newTestInviteHandler(t, nil)

	_, err := handler.rpcJoin(context.Background(), json.RawMessage(`{"invite_id":"abc123"}`))
	if err == nil {
		t.Fatal("rpcJoin error = nil, want missing storage error")
	}
	if got, want := err.Error(), "S3 join requires storage — use 'sky10 join' for P2P"; got != want {
		t.Fatalf("rpcJoin error = %q, want %q", got, want)
	}
}

func TestRPCDeviceListUsesStoredDeviceEntry(t *testing.T) {
	t.Parallel()

	backend := s3adapter.NewMemory()
	handler := newTestInviteHandler(t, backend)
	info := skydevice.Info{
		ID:       handler.store.deviceID,
		PubKey:   handler.store.identity.Address(),
		Name:     "stored-device",
		Platform: "Linux",
		Version:  "v-test",
		Joined:   "2026-04-18T12:00:00Z",
		LastSeen: "2026-04-18T12:00:00Z",
	}
	payload, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal info: %v", err)
	}
	key := "devices/" + handler.store.deviceID + ".json"
	if err := backend.Put(context.Background(), key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Put device entry: %v", err)
	}

	raw, err := handler.rpcDeviceList(context.Background())
	if err != nil {
		t.Fatalf("rpcDeviceList: %v", err)
	}

	result, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map[string]interface{}", raw)
	}
	if got := result["this_device"]; got != handler.store.deviceID {
		t.Fatalf("this_device = %v, want %q", got, handler.store.deviceID)
	}

	devices, ok := result["devices"].([]DeviceInfo)
	if !ok {
		t.Fatalf("devices type = %T, want []DeviceInfo", result["devices"])
	}
	if len(devices) != 1 {
		t.Fatalf("devices len = %d, want 1", len(devices))
	}
	if devices[0].ID != handler.store.deviceID {
		t.Fatalf("device ID = %q, want %q", devices[0].ID, handler.store.deviceID)
	}
	if devices[0].Name != "stored-device" {
		t.Fatalf("device name = %q, want stored-device", devices[0].Name)
	}
}

func newTestInviteHandler(t *testing.T, backend adapter.Backend) *FSHandler {
	t.Helper()

	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateDeviceKey: %v", err)
	}
	store := NewWithDevice(backend, id, "D-testdev1")
	store.SetDevicePubKey("device-pubkey-hex")

	return &FSHandler{
		store:   store,
		version: "test-version",
	}
}
