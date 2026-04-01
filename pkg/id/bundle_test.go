package id

import (
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

func generateTestBundle(t *testing.T) *Bundle {
	t.Helper()
	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	device, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "test-device")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	b, err := New(identity, device, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBundleAddress(t *testing.T) {
	t.Parallel()
	b := generateTestBundle(t)

	if b.Address() != b.Identity.Address() {
		t.Errorf("Address() = %s, want identity address %s", b.Address(), b.Identity.Address())
	}
	if b.Address() == b.DeviceID() {
		t.Error("identity and device addresses should differ")
	}
}

func TestBundleDeviceID(t *testing.T) {
	t.Parallel()
	b := generateTestBundle(t)

	// DeviceID is 16 chars derived from device key's sky10q address.
	id := b.DeviceID()
	if len(id) != 16 {
		t.Errorf("DeviceID() length = %d, want 16", len(id))
	}
}

func TestBundleDevicePubKeyHex(t *testing.T) {
	t.Parallel()
	b := generateTestBundle(t)

	hex := b.DevicePubKeyHex()
	if len(hex) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("DevicePubKeyHex() length = %d, want 64", len(hex))
	}
}

func TestBundleIsDeviceAuthorized(t *testing.T) {
	t.Parallel()
	b := generateTestBundle(t)

	if !b.IsDeviceAuthorized(b.Device.PublicKey) {
		t.Error("device should be authorized")
	}

	other, _ := skykey.Generate()
	if b.IsDeviceAuthorized(other.PublicKey) {
		t.Error("unknown device should not be authorized")
	}
}

func TestNewBundleRejectsPublicOnlyIdentity(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()
	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "test")
	manifest.Sign(identity.PrivateKey)

	pubOnly := skykey.FromPublicKey(identity.PublicKey)
	_, err := New(pubOnly, device, manifest)
	if err == nil {
		t.Error("expected error for public-only identity key")
	}
}

func TestNewBundleRejectsPublicOnlyDevice(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()
	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "test")
	manifest.Sign(identity.PrivateKey)

	pubOnly := skykey.FromPublicKey(device.PublicKey)
	_, err := New(identity, pubOnly, manifest)
	if err == nil {
		t.Error("expected error for public-only device key")
	}
}

func TestNewBundleRejectsBadSignature(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	other, _ := skykey.Generate()
	device, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "test")
	// Sign with wrong key.
	manifest.Sign(other.PrivateKey)

	_, err := New(identity, device, manifest)
	if err == nil {
		t.Error("expected error for invalid manifest signature")
	}
}

func TestNewBundleRejectsDeviceNotInManifest(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()
	otherDevice, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(otherDevice.PublicKey, "other")
	manifest.Sign(identity.PrivateKey)

	_, err := New(identity, device, manifest)
	if err == nil {
		t.Error("expected error when device key not in manifest")
	}
}

func TestBundleAddressDeterministic(t *testing.T) {
	t.Parallel()
	b := generateTestBundle(t)

	// Same bundle should produce identical address on every call.
	a1 := b.Address()
	a2 := b.Address()
	if a1 != a2 {
		t.Errorf("Address() not deterministic: %s != %s", a1, a2)
	}
}

func TestBundleAddressMatchesKeyAddress(t *testing.T) {
	t.Parallel()
	// Generate a known identity key, create bundle, verify address
	// matches what pkg/key would produce.
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "test")
	manifest.Sign(identity.PrivateKey)

	b, err := New(identity, device, manifest)
	if err != nil {
		t.Fatal(err)
	}

	// Address from bundle must match address from raw key.
	keyAddr := identity.Address()
	bundleAddr := b.Address()
	if keyAddr != bundleAddr {
		t.Errorf("bundle address %s != key address %s", bundleAddr, keyAddr)
	}

	// Verify it round-trips through ParseAddress.
	parsed, err := skykey.ParseAddress(bundleAddr)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.PublicKey.Equal(identity.PublicKey) {
		t.Error("parsed public key doesn't match identity")
	}
}

func TestTwoDevicesSameIdentity(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	deviceA, _ := skykey.Generate()
	deviceB, _ := skykey.Generate()

	manifest := NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "laptop")
	manifest.AddDevice(deviceB.PublicKey, "phone")
	manifest.Sign(identity.PrivateKey)

	bundleA, err := New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}

	// Same identity address.
	if bundleA.Address() != bundleB.Address() {
		t.Error("two devices with same identity should have same address")
	}

	// Different device addresses.
	if bundleA.DeviceID() == bundleB.DeviceID() {
		t.Error("two devices should have different device addresses")
	}

	// Each bundle authorizes the other's device.
	if !bundleA.IsDeviceAuthorized(deviceB.PublicKey) {
		t.Error("bundleA should authorize deviceB")
	}
	if !bundleB.IsDeviceAuthorized(deviceA.PublicKey) {
		t.Error("bundleB should authorize deviceA")
	}
}

func TestBundleRejectsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func() (*skykey.Key, *skykey.Key, *DeviceManifest)
	}{
		{
			name: "public-only identity",
			make: func() (*skykey.Key, *skykey.Key, *DeviceManifest) {
				id, _ := skykey.Generate()
				dev, _ := skykey.Generate()
				m := NewManifest(id)
				m.AddDevice(dev.PublicKey, "x")
				m.Sign(id.PrivateKey)
				return skykey.FromPublicKey(id.PublicKey), dev, m
			},
		},
		{
			name: "public-only device",
			make: func() (*skykey.Key, *skykey.Key, *DeviceManifest) {
				id, _ := skykey.Generate()
				dev, _ := skykey.Generate()
				m := NewManifest(id)
				m.AddDevice(dev.PublicKey, "x")
				m.Sign(id.PrivateKey)
				return id, skykey.FromPublicKey(dev.PublicKey), m
			},
		},
		{
			name: "wrong signer",
			make: func() (*skykey.Key, *skykey.Key, *DeviceManifest) {
				id, _ := skykey.Generate()
				dev, _ := skykey.Generate()
				other, _ := skykey.Generate()
				m := NewManifest(id)
				m.AddDevice(dev.PublicKey, "x")
				m.Sign(other.PrivateKey)
				return id, dev, m
			},
		},
		{
			name: "device not in manifest",
			make: func() (*skykey.Key, *skykey.Key, *DeviceManifest) {
				id, _ := skykey.Generate()
				dev, _ := skykey.Generate()
				other, _ := skykey.Generate()
				m := NewManifest(id)
				m.AddDevice(other.PublicKey, "other")
				m.Sign(id.PrivateKey)
				return id, dev, m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, dev, m := tt.make()
			_, err := New(id, dev, m)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestManifestSignVerify(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(device.PublicKey, "laptop")
	if err := m.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	if !m.Verify(identity.PublicKey) {
		t.Error("valid signature should verify")
	}
}

func TestManifestVerifyUnsigned(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	m := NewManifest(identity)

	if m.Verify(identity.PublicKey) {
		t.Error("unsigned manifest should not verify")
	}
}

func TestManifestAddRemoveDevice(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	d1, _ := skykey.Generate()
	d2, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(d1.PublicKey, "laptop")
	m.AddDevice(d2.PublicKey, "phone")

	if !m.HasDevice(d1.PublicKey) {
		t.Error("d1 should be present")
	}
	if !m.HasDevice(d2.PublicKey) {
		t.Error("d2 should be present")
	}

	m.RemoveDevice(d1.PublicKey)
	if m.HasDevice(d1.PublicKey) {
		t.Error("d1 should be removed")
	}
	if !m.HasDevice(d2.PublicKey) {
		t.Error("d2 should still be present")
	}
}

func TestManifestVerifyTampered(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(device.PublicKey, "laptop")
	m.Sign(identity.PrivateKey)

	// Tamper: add another device after signing.
	extra, _ := skykey.Generate()
	m.Devices = append(m.Devices, DeviceEntry{
		PublicKey: extra.PublicKey,
		Name:      "injected",
	})

	if m.Verify(identity.PublicKey) {
		t.Error("tampered manifest should not verify")
	}
}

func TestManifestVerifyWrongKey(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	other, _ := skykey.Generate()
	device, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(device.PublicKey, "laptop")
	m.Sign(identity.PrivateKey)

	if m.Verify(other.PublicKey) {
		t.Error("wrong key should not verify")
	}
}

func TestManifestMultipleDevices(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	d1, _ := skykey.Generate()
	d2, _ := skykey.Generate()
	d3, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(d1.PublicKey, "laptop")
	m.AddDevice(d2.PublicKey, "phone")
	m.AddDevice(d3.PublicKey, "server")
	m.Sign(identity.PrivateKey)

	if len(m.Devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(m.Devices))
	}

	// Remove middle device, re-sign, verify.
	m.RemoveDevice(d2.PublicKey)
	m.Sign(identity.PrivateKey)

	if len(m.Devices) != 2 {
		t.Fatalf("expected 2 devices after remove, got %d", len(m.Devices))
	}
	if m.HasDevice(d2.PublicKey) {
		t.Error("d2 should be removed")
	}
	if !m.Verify(identity.PublicKey) {
		t.Error("re-signed manifest should verify")
	}
}
