package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

// DeviceInfo represents a registered device in the S3 registry.
type DeviceInfo struct {
	PubKey   string `json:"pubkey"`
	Name     string `json:"name"`
	Joined   string `json:"joined"`
	Platform string `json:"platform,omitempty"`
}

// RegisterDevice writes this device's info to the S3 registry.
func RegisterDevice(ctx context.Context, backend adapter.Backend, pubkey string, name string) error {
	info := DeviceInfo{
		PubKey:   pubkey,
		Name:     name,
		Joined:   time.Now().UTC().Format(time.RFC3339),
		Platform: detectPlatform(),
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	// Use first 16 chars of pubkey as ID (enough to be unique)
	id := shortPubkeyID(pubkey)
	key := "devices/" + id + ".json"

	r := bytes.NewReader(data)
	return backend.Put(ctx, key, r, int64(len(data)))
}

// ListDevices returns all registered devices from the S3 registry.
func ListDevices(ctx context.Context, backend adapter.Backend) ([]DeviceInfo, error) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		return nil, err
	}

	var devices []DeviceInfo
	for _, k := range keys {
		rc, err := backend.Get(ctx, k)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		var d DeviceInfo
		if json.Unmarshal(data, &d) == nil {
			devices = append(devices, d)
		}
	}

	return devices, nil
}

func shortPubkeyID(pubkey string) string {
	// sky10q<data> — take first 16 chars after the prefix
	if len(pubkey) > 21 {
		return pubkey[5:21] // skip "sky10" prefix, take 16
	}
	return pubkey
}

func detectPlatform() string {
	switch {
	case fileExists("/Applications"):
		return "macOS"
	case fileExists("/proc"):
		return "Linux"
	default:
		return "unknown"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetDeviceName returns a human-readable name for this device.
func GetDeviceName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "Unknown Device"
	}
	return hostname
}

// FormatDeviceList returns a formatted string of all devices.
func FormatDeviceList(devices []DeviceInfo) string {
	var buf bytes.Buffer
	for _, d := range devices {
		fmt.Fprintf(&buf, "  %s (%s) — joined %s\n", d.Name, d.PubKey[:20]+"...", d.Joined[:10])
	}
	return buf.String()
}
