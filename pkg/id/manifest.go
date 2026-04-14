package id

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

const (
	DeviceRoleTrusted = "trusted"
	DeviceRoleSandbox = "sandbox"
)

// DeviceEntry is one authorized device in the manifest.
type DeviceEntry struct {
	PublicKey []byte    `json:"public_key"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	AddedAt   time.Time `json:"added_at"`
}

// DeviceManifest lists device public keys authorized for an identity.
// Signed by the identity key.
type DeviceManifest struct {
	Identity  string        `json:"identity"`
	Devices   []DeviceEntry `json:"devices"`
	UpdatedAt time.Time     `json:"updated_at"`
	Signature []byte        `json:"signature"`
}

// NewManifest creates an empty manifest for the given identity key.
func NewManifest(identity *skykey.Key) *DeviceManifest {
	return &DeviceManifest{
		Identity:  identity.Address(),
		UpdatedAt: time.Now().UTC(),
	}
}

// CanonicalDeviceRole stores trusted as the zero/default role to preserve
// compatibility with manifests created before roles existed.
func CanonicalDeviceRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", DeviceRoleTrusted:
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(role))
	}
}

// NormalizeDeviceRole returns the effective role for policy and UI use.
func NormalizeDeviceRole(role string) string {
	if canonical := CanonicalDeviceRole(role); canonical != "" {
		return canonical
	}
	return DeviceRoleTrusted
}

// AddDevice appends a device entry. Does not re-sign — call Sign after.
func (m *DeviceManifest) AddDevice(pub ed25519.PublicKey, name string) {
	m.AddDeviceWithRole(pub, name, "")
}

// AddDeviceWithRole appends a device entry with an optional role. Does not
// re-sign — call Sign after.
func (m *DeviceManifest) AddDeviceWithRole(pub ed25519.PublicKey, name, role string) {
	m.Devices = append(m.Devices, DeviceEntry{
		PublicKey: []byte(pub),
		Name:      name,
		Role:      CanonicalDeviceRole(role),
		AddedAt:   time.Now().UTC(),
	})
	m.UpdatedAt = time.Now().UTC()
}

// RemoveDevice removes a device by public key. Does not re-sign.
func (m *DeviceManifest) RemoveDevice(pub ed25519.PublicKey) {
	filtered := m.Devices[:0]
	for _, d := range m.Devices {
		if !bytes.Equal(d.PublicKey, pub) {
			filtered = append(filtered, d)
		}
	}
	m.Devices = filtered
	m.UpdatedAt = time.Now().UTC()
}

// HasDevice checks whether a device public key is in the manifest.
func (m *DeviceManifest) HasDevice(pub ed25519.PublicKey) bool {
	for _, d := range m.Devices {
		if bytes.Equal(d.PublicKey, pub) {
			return true
		}
	}
	return false
}

// Sign computes the Ed25519 signature over the canonical manifest payload.
func (m *DeviceManifest) Sign(priv ed25519.PrivateKey) error {
	payload, err := m.signPayload()
	if err != nil {
		return fmt.Errorf("computing sign payload: %w", err)
	}
	m.Signature = skykey.Sign(payload, priv)
	return nil
}

// Verify checks the manifest signature against the identity's public key.
func (m *DeviceManifest) Verify(pub ed25519.PublicKey) bool {
	if len(m.Signature) == 0 {
		return false
	}
	payload, err := m.signPayload()
	if err != nil {
		return false
	}
	return skykey.Verify(payload, m.Signature, pub)
}

// signPayload produces the canonical bytes that are signed. It marshals
// all fields except Signature in a deterministic order.
func (m *DeviceManifest) signPayload() ([]byte, error) {
	// Sort devices by public key for deterministic output.
	sorted := make([]DeviceEntry, len(m.Devices))
	copy(sorted, m.Devices)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].PublicKey, sorted[j].PublicKey) < 0
	})

	payload := struct {
		Identity  string        `json:"identity"`
		Devices   []DeviceEntry `json:"devices"`
		UpdatedAt time.Time     `json:"updated_at"`
	}{
		Identity:  m.Identity,
		Devices:   sorted,
		UpdatedAt: m.UpdatedAt,
	}
	return json.Marshal(payload)
}
