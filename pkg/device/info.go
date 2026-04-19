package device

import (
	"bytes"
	"fmt"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Info is the current device snapshot stored in the registry.
type Info struct {
	ID         string   `json:"id"`              // 16-char device identifier
	PubKey     string   `json:"pubkey"`          // hex-encoded Ed25519 device public key
	Name       string   `json:"name"`            // hostname
	Alias      string   `json:"alias,omitempty"` // user-chosen display name
	Joined     string   `json:"joined"`
	Platform   string   `json:"platform,omitempty"`
	IP         string   `json:"ip,omitempty"`
	Location   string   `json:"location,omitempty"`
	Version    string   `json:"version,omitempty"`
	LastSeen   string   `json:"last_seen,omitempty"`
	Multiaddrs []string `json:"multiaddrs,omitempty"` // libp2p listen addresses
}

// ShortPubkeyID extracts a device ID from a sky10q... address.
// Format: "D-" + 8 chars.
func ShortPubkeyID(pubkey string) string {
	return "D-" + skykey.ShortIDFromAddress(pubkey)
}

// FormatList returns a formatted string of all devices.
func FormatList(devices []Info) string {
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
