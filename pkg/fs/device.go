package fs

import (
	"context"

	"github.com/sky10/sky10/pkg/adapter"
	skydevice "github.com/sky10/sky10/pkg/device"
)

// DeviceInfo is the legacy fs alias for the device registry snapshot.
type DeviceInfo = skydevice.Info

// RegisterDevice writes this device's info to the registry.
func RegisterDevice(ctx context.Context, backend adapter.Backend, deviceID string, pubKeyHex string, name string, version string) error {
	return skydevice.Register(ctx, backend, deviceID, pubKeyHex, name, version)
}

// UpdateDeviceMultiaddrs updates the multiaddrs field on an existing device entry.
func UpdateDeviceMultiaddrs(ctx context.Context, backend adapter.Backend, deviceID string, addrs []string) error {
	return skydevice.UpdateMultiaddrs(ctx, backend, deviceID, addrs)
}

// ListDevices returns all registered devices from the registry.
func ListDevices(ctx context.Context, backend adapter.Backend) ([]DeviceInfo, error) {
	return skydevice.List(ctx, backend)
}

// ShortPubkeyID extracts a device ID from a sky10q... address.
func ShortPubkeyID(pubkey string) string {
	return skydevice.ShortPubkeyID(pubkey)
}

// shortPubkeyID is the unexported version for internal use.
func shortPubkeyID(pubkey string) string {
	return ShortPubkeyID(pubkey)
}

// GetDeviceName returns a human-readable name for this device.
func GetDeviceName() string {
	return skydevice.DeviceName()
}

// FormatDeviceList returns a formatted string of all devices.
func FormatDeviceList(devices []DeviceInfo) string {
	return skydevice.FormatList(devices)
}
