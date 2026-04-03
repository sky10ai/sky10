package fs

import (
	"context"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// Regression: RegisterDevice used to derive the S3 filename via
// shortPubkeyID on the devicePubKey parameter. After the
// DeviceAddress→DevicePubKeyHex refactor, callers passed raw hex
// instead of sky10q addresses, producing a different device ID for
// the same key and creating duplicate device entries.
func TestRegisterDeviceUsesExplicitDeviceID(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	ctx := context.Background()

	deviceID := "q6tvmewavwezlzjq"
	hexPubkey := "d2d9bcbbac7645f14809268473a77e81f55f4b29384d38803603f8ff4fae64c1"

	err := RegisterDevice(ctx, backend, deviceID, hexPubkey, "test-device", "v0.30.1")
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	wantKey := "devices/" + deviceID + ".json"
	keys, _ := backend.List(ctx, "devices/")
	if len(keys) != 1 {
		t.Fatalf("expected 1 device file, got %d: %v", len(keys), keys)
	}
	if keys[0] != wantKey {
		t.Errorf("device stored at %q, want %q", keys[0], wantKey)
	}

	dev, err := readDevice(ctx, backend, wantKey)
	if err != nil {
		t.Fatalf("readDevice: %v", err)
	}
	if dev.PubKey != hexPubkey {
		t.Errorf("PubKey = %q, want %q", dev.PubKey, hexPubkey)
	}
	if dev.ID != deviceID {
		t.Errorf("ID = %q, want %q", dev.ID, deviceID)
	}
}
