// Package id separates user identity from device transport keys.
//
// A Bundle pairs an identity key (the user's external sky10q... address,
// used for encryption) with a device key (unique per device, used for
// libp2p transport). The identity key signs a DeviceManifest that claims
// which device keys belong to this identity.
package id

import (
	"crypto/ed25519"
	"encoding/hex"
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

// DeviceID returns a short 16-char identifier for this device, derived
// from the device key's sky10q address. Used as the S3 filename key
// for the device registry (e.g. devices/<deviceID>.json).
func (b *Bundle) DeviceID() string {
	addr := b.Device.Address()
	if len(addr) > 21 {
		return addr[5:21]
	}
	return addr
}

// DevicePubKeyHex returns the device's raw Ed25519 public key as hex.
// This is the device identifier shown in the UI — distinct from the
// sky10q identity address.
func (b *Bundle) DevicePubKeyHex() string {
	return hex.EncodeToString(b.Device.PublicKey)
}

// IsDeviceAuthorized checks whether a device public key is listed in the
// manifest.
func (b *Bundle) IsDeviceAuthorized(pub ed25519.PublicKey) bool {
	return b.Manifest.HasDevice(pub)
}
