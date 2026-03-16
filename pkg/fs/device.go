package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	IP       string `json:"ip,omitempty"`
	Location string `json:"location,omitempty"`
}

// RegisterDevice writes this device's info to the S3 registry.
// On re-registration (daemon restart), it preserves the original join date
// but refreshes the IP and location.
func RegisterDevice(ctx context.Context, backend adapter.Backend, pubkey string, name string) error {
	id := shortPubkeyID(pubkey)
	key := "devices/" + id + ".json"

	// Preserve original join date if device already registered
	joined := time.Now().UTC().Format(time.RFC3339)
	if existing, err := readDevice(ctx, backend, key); err == nil && existing.Joined != "" {
		joined = existing.Joined
	}

	ip, location := fetchIPLocation()

	info := DeviceInfo{
		PubKey:   pubkey,
		Name:     name,
		Joined:   joined,
		Platform: detectPlatform(),
		IP:       ip,
		Location: location,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	r := bytes.NewReader(data)
	return backend.Put(ctx, key, r, int64(len(data)))
}

func readDevice(ctx context.Context, backend adapter.Backend, key string) (*DeviceInfo, error) {
	rc, err := backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var d DeviceInfo
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDevices returns all registered devices from the S3 registry.
func ListDevices(ctx context.Context, backend adapter.Backend) ([]DeviceInfo, error) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		return nil, err
	}

	var devices []DeviceInfo
	for _, k := range keys {
		d, err := readDevice(ctx, backend, k)
		if err != nil {
			continue
		}
		devices = append(devices, *d)
	}

	return devices, nil
}

// fetchIPLocation calls ip-api.com to get the public IP and city/country.
// Returns empty strings on failure — never blocks registration.
func fetchIPLocation() (ip string, location string) {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/?fields=query,city,regionName,country")
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var result struct {
		Query      string `json:"query"`
		City       string `json:"city"`
		RegionName string `json:"regionName"`
		Country    string `json:"country"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", ""
	}

	loc := result.City
	if result.RegionName != "" && result.RegionName != result.City {
		loc += ", " + result.RegionName
	}
	if result.Country != "" {
		loc += ", " + result.Country
	}

	return result.Query, loc
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
		line := fmt.Sprintf("  %s (%s) — joined %s", d.Name, d.PubKey[:20]+"...", d.Joined[:10])
		if d.Location != "" {
			line += " — " + d.Location
		}
		buf.WriteString(line + "\n")
	}
	return buf.String()
}
