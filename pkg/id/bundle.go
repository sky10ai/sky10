// Package id separates user identity from device transport keys.
//
// A Bundle pairs an identity key (the user's external sky10q... address,
// used for encryption) with a device key (unique per device, used for
// libp2p transport). The identity key signs a DeviceManifest that claims
// which device keys belong to this identity.
package id

import (
	"crypto/ed25519"
	"fmt"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Bundle holds a user's identity key and this device's transport key,
// plus the signed manifest binding devices to the identity.
type Bundle struct {
	Identity *skykey.Key
	Device   *skykey.Key
	Manifest *DeviceManifest
}

// New creates a Bundle after validating both keys and the manifest signature.
func New(identity, device *skykey.Key, manifest *DeviceManifest) (*Bundle, error) {
	if !identity.IsPrivate() {
		return nil, fmt.Errorf("identity key must have private component")
	}
	if !device.IsPrivate() {
		return nil, fmt.Errorf("device key must have private component")
	}
	if !manifest.Verify(identity.PublicKey) {
		return nil, fmt.Errorf("manifest signature invalid")
	}
	if !manifest.HasDevice(device.PublicKey) {
		return nil, fmt.Errorf("device key not in manifest")
	}
	return &Bundle{
		Identity: identity,
		Device:   device,
		Manifest: manifest,
	}, nil
}

// Address returns this identity's sky10q... address. This is the external
// address the world sees — stable across all devices.
func (b *Bundle) Address() string {
	return b.Identity.Address()
}

// DeviceAddress returns this device's sky10q... address. Used for device
// identification (e.g. S3 device registry, ops log attribution).
func (b *Bundle) DeviceAddress() string {
	return b.Device.Address()
}

// IsDeviceAuthorized checks whether a device public key is listed in the
// manifest.
func (b *Bundle) IsDeviceAuthorized(pub ed25519.PublicKey) bool {
	return b.Manifest.HasDevice(pub)
}
