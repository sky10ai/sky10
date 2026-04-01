package id

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestManifestSignPayloadDeterministic(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	d1, _ := skykey.Generate()
	d2, _ := skykey.Generate()

	// Add devices in order d1, d2.
	m1 := NewManifest(identity)
	m1.AddDevice(d1.PublicKey, "laptop")
	m1.AddDevice(d2.PublicKey, "phone")
	// Add devices in reverse order d2, d1 with same timestamp.
	m2 := &DeviceManifest{
		Identity:  identity.Address(),
		UpdatedAt: m1.UpdatedAt,
	}
	m2.Devices = append(m2.Devices, m1.Devices[1]) // phone first
	m2.Devices = append(m2.Devices, m1.Devices[0]) // laptop second

	p1, _ := m1.signPayload()
	p2, _ := m2.signPayload()
	if !bytes.Equal(p1, p2) {
		t.Error("sign payload should be deterministic regardless of device order")
	}
}

func TestManifestSignatureCoversAllFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tamper func(m *DeviceManifest)
	}{
		{
			name:   "change identity",
			tamper: func(m *DeviceManifest) { m.Identity = "sky10qfakeaddress" },
		},
		{
			name: "change device name",
			tamper: func(m *DeviceManifest) {
				m.Devices[0].Name = "tampered"
			},
		},
		{
			name: "change device key",
			tamper: func(m *DeviceManifest) {
				fake, _ := skykey.Generate()
				m.Devices[0].PublicKey = fake.PublicKey
			},
		},
		{
			name: "add extra device",
			tamper: func(m *DeviceManifest) {
				extra, _ := skykey.Generate()
				m.Devices = append(m.Devices, DeviceEntry{
					PublicKey: extra.PublicKey,
					Name:      "injected",
				})
			},
		},
		{
			name: "remove device",
			tamper: func(m *DeviceManifest) {
				m.Devices = m.Devices[:0]
			},
		},
		{
			name: "swap signature from another manifest",
			tamper: func(m *DeviceManifest) {
				other, _ := skykey.Generate()
				otherM := NewManifest(other)
				d, _ := skykey.Generate()
				otherM.AddDevice(d.PublicKey, "x")
				otherM.Sign(other.PrivateKey)
				m.Signature = otherM.Signature
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			identity, _ := skykey.Generate()
			device, _ := skykey.Generate()

			m := NewManifest(identity)
			m.AddDevice(device.PublicKey, "laptop")
			m.Sign(identity.PrivateKey)

			if !m.Verify(identity.PublicKey) {
				t.Fatal("should verify before tampering")
			}

			tt.tamper(m)

			if m.Verify(identity.PublicKey) {
				t.Error("should fail verification after tampering")
			}
		})
	}
}

func TestManifestJSONRoundTrip(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	d1, _ := skykey.Generate()
	d2, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(d1.PublicKey, "laptop")
	m.AddDevice(d2.PublicKey, "phone")
	m.Sign(identity.PrivateKey)

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	var loaded DeviceManifest
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.Identity != m.Identity {
		t.Errorf("identity = %s, want %s", loaded.Identity, m.Identity)
	}
	if len(loaded.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(loaded.Devices))
	}
	if !loaded.Verify(identity.PublicKey) {
		t.Error("deserialized manifest should verify")
	}
}

func TestManifestEmptyDeviceList(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()

	m := NewManifest(identity)
	if err := m.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}
	if !m.Verify(identity.PublicKey) {
		t.Error("empty device list should still sign/verify")
	}
}

func TestManifestHasDeviceNoFalsePositives(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()

	// Generate 10 random keys, add 5 to manifest.
	keys := make([]*skykey.Key, 10)
	for i := range keys {
		keys[i], _ = skykey.Generate()
	}

	m := NewManifest(identity)
	for i := 0; i < 5; i++ {
		m.AddDevice(keys[i].PublicKey, "dev")
	}

	for i := 0; i < 5; i++ {
		if !m.HasDevice(keys[i].PublicKey) {
			t.Errorf("key %d should be in manifest", i)
		}
	}
	for i := 5; i < 10; i++ {
		if m.HasDevice(keys[i].PublicKey) {
			t.Errorf("key %d should NOT be in manifest", i)
		}
	}
}

func TestManifestRemoveNonexistent(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	d1, _ := skykey.Generate()
	d2, _ := skykey.Generate()

	m := NewManifest(identity)
	m.AddDevice(d1.PublicKey, "laptop")
	m.RemoveDevice(d2.PublicKey) // not present — should be a no-op

	if len(m.Devices) != 1 {
		t.Errorf("devices = %d, want 1", len(m.Devices))
	}
	if !m.HasDevice(d1.PublicKey) {
		t.Error("d1 should still be present")
	}
}

func TestManifestVerifyRejectsNilSignature(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	m := NewManifest(identity)
	m.Signature = nil

	if m.Verify(identity.PublicKey) {
		t.Error("nil signature should not verify")
	}
}

func TestManifestVerifyRejectsEmptySignature(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	m := NewManifest(identity)
	m.Signature = []byte{}

	if m.Verify(identity.PublicKey) {
		t.Error("empty signature should not verify")
	}
}

func TestManifestVerifyRejectsGarbageSignature(t *testing.T) {
	t.Parallel()
	identity, _ := skykey.Generate()
	m := NewManifest(identity)
	m.Signature = make([]byte, ed25519.SignatureSize)

	if m.Verify(identity.PublicKey) {
		t.Error("garbage signature should not verify")
	}
}
