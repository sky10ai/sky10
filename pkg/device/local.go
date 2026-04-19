package device

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

type localMetadata struct {
	Name     string
	Platform string
	IP       string
	Location string
}

type localCollector struct {
	client   *http.Client
	hostname func() (string, error)
	goos     string
}

func newLocalCollector() localCollector {
	return localCollector{
		client:   &http.Client{Timeout: 3 * time.Second},
		hostname: os.Hostname,
		goos:     runtime.GOOS,
	}
}

func (c localCollector) current() localMetadata {
	ip, location := c.ipLocation()
	return localMetadata{
		Name:     c.deviceName(),
		Platform: platformForGOOS(c.goos),
		IP:       ip,
		Location: location,
	}
}

func (c localCollector) deviceName() string {
	hostname, err := c.hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "Unknown Device"
	}
	return hostname
}

func (c localCollector) ipLocation() (ip string, location string) {
	resp, err := c.client.Get("http://ip-api.com/json/?fields=query,city,regionName,country")
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
		if loc != "" {
			loc += ", "
		}
		loc += result.RegionName
	}
	if result.Country != "" {
		if loc != "" {
			loc += ", "
		}
		loc += result.Country
	}

	return result.Query, loc
}

func platformForGOOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return "unknown"
	}
}

// DeviceName returns a human-readable name for this device.
func DeviceName() string {
	return newLocalCollector().deviceName()
}
