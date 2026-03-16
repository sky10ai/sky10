// Package fs provides encrypted file storage primitives.
//
// Every file is encrypted with AES-256-GCM before it leaves the device.
// DeviceKey is an Ed25519 keypair managed by the skykey package.
// The key hierarchy (user → namespace → file) makes rotation cheap
// and scoped access possible.
package fs

import (
	skykey "github.com/sky10/sky10/pkg/key"
)

// DeviceKey is a type alias for skykey.Key.
type DeviceKey = skykey.Key

// GenerateKey creates a new random Ed25519 keypair.
func GenerateDeviceKey() (*DeviceKey, error) {
	return skykey.Generate()
}

// SaveKey writes the identity to path with restricted permissions.
func SaveKey(id *DeviceKey, path string) error {
	return skykey.Save(id, path)
}

// SaveKeyWithDescription writes the identity with a description field.
func SaveKeyWithDescription(id *DeviceKey, path string, description string) error {
	return skykey.SaveWithDescription(id, path, description)
}

// LoadKey reads an identity from path.
func LoadKey(path string) (*DeviceKey, error) {
	return skykey.Load(path)
}
