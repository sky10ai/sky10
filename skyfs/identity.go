// Package skyfs provides encrypted file storage primitives.
//
// Every file is encrypted with AES-256-GCM before it leaves the device.
// Identity is an Ed25519 keypair managed by the skykey package.
// The key hierarchy (user → namespace → file) makes rotation cheap
// and scoped access possible.
package skyfs

import (
	"github.com/sky10/sky10/skykey"
)

// Identity is a type alias for skykey.Key. Kept for backward compatibility
// within skyfs. New code should use *skykey.Key directly.
type Identity = skykey.Key

// GenerateIdentity creates a new random Ed25519 keypair.
func GenerateIdentity() (*Identity, error) {
	return skykey.Generate()
}

// SaveIdentity writes the identity to path with restricted permissions.
func SaveIdentity(id *Identity, path string) error {
	return skykey.Save(id, path)
}

// LoadIdentity reads an identity from path.
func LoadIdentity(path string) (*Identity, error) {
	return skykey.Load(path)
}
