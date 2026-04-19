package device

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skyid "github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

// Metadata is the best-effort per-device state composed for current device
// list views.
type Metadata struct {
	Alias      string
	Platform   string
	IP         string
	Location   string
	Version    string
	LastSeen   string
	Multiaddrs []string
}

// Service composes current device metadata from local state, registry state,
// and live link presence.
type Service struct {
	registry *Registry
	now      func() time.Time
}

var defaultService = NewService()

// NewService creates a device metadata service backed by the default registry.
func NewService() *Service {
	return &Service{
		registry: defaultRegistry,
		now:      time.Now,
	}
}

// PrivateNetworkMetadata returns best-effort per-device metadata keyed by
// canonical lower-case device public key hex.
func PrivateNetworkMetadata(
	ctx context.Context,
	bundle *skyid.Bundle,
	backend adapter.Backend,
	linkNode *link.Node,
) (map[string]Metadata, error) {
	return defaultService.PrivateNetworkMetadata(ctx, bundle, backend, linkNode)
}

// PrivateNetworkMetadata returns best-effort per-device metadata keyed by
// canonical lower-case device public key hex.
func (s *Service) PrivateNetworkMetadata(
	ctx context.Context,
	bundle *skyid.Bundle,
	backend adapter.Backend,
	linkNode *link.Node,
) (map[string]Metadata, error) {
	metadata := make(map[string]Metadata)

	if backend != nil {
		devices, err := s.registry.List(ctx, backend)
		if err == nil {
			for _, device := range devices {
				pubHex, err := canonicalDevicePubKey(device.PubKey)
				if err != nil {
					continue
				}
				meta := metadata[pubHex]
				meta.Alias = device.Alias
				meta.Platform = device.Platform
				meta.IP = device.IP
				meta.Location = device.Location
				meta.Version = device.Version
				meta.LastSeen = device.LastSeen
				meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, device.Multiaddrs...)
				metadata[pubHex] = meta
			}
		}
	}

	s.mergeLocalCurrentDevice(metadata, bundle, linkNode)
	s.mergeLinkPresence(metadata, bundle, linkNode)

	return metadata, nil
}

func (s *Service) mergeLocalCurrentDevice(
	metadata map[string]Metadata,
	bundle *skyid.Bundle,
	linkNode *link.Node,
) {
	if bundle == nil {
		return
	}

	pubHex := strings.ToLower(bundle.DevicePubKeyHex())
	meta := metadata[pubHex]
	needsLocalDetails := meta.Platform == "" || meta.IP == "" || meta.Location == ""
	if !needsLocalDetails {
		if meta.LastSeen == "" {
			meta.LastSeen = s.now().UTC().Format(time.RFC3339)
			metadata[pubHex] = meta
		}
		return
	}

	version := ""
	if linkNode != nil {
		rec, err := linkNode.CurrentPresenceRecord(0)
		if err == nil && strings.EqualFold(rec.DevicePubKey, bundle.DevicePubKeyHex()) {
			version = rec.Version
		}
	}

	info := s.registry.LocalInfo(
		bundle.DeviceID(),
		bundle.DevicePubKeyHex(),
		currentDeviceName(bundle),
		version,
	)
	if meta.Platform == "" {
		meta.Platform = info.Platform
	}
	if meta.IP == "" {
		meta.IP = info.IP
	}
	if meta.Location == "" {
		meta.Location = info.Location
	}
	if meta.Version == "" {
		meta.Version = info.Version
	}
	if meta.LastSeen == "" {
		meta.LastSeen = info.LastSeen
	}
	metadata[pubHex] = meta
}

func (s *Service) mergeLinkPresence(
	metadata map[string]Metadata,
	bundle *skyid.Bundle,
	linkNode *link.Node,
) {
	if linkNode == nil {
		return
	}

	rec, err := linkNode.CurrentPresenceRecord(0)
	if err == nil {
		pubHex, err := canonicalDevicePubKey(rec.DevicePubKey)
		if err == nil {
			meta := metadata[pubHex]
			if meta.Version == "" {
				meta.Version = rec.Version
			}
			if !rec.PublishedAt.IsZero() {
				meta.LastSeen = rec.PublishedAt.UTC().Format(time.RFC3339)
			}
			meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, rec.Multiaddrs...)
			metadata[pubHex] = meta
		}
	}

	if bundle == nil || bundle.Manifest == nil || linkNode.Host() == nil {
		return
	}

	deviceByPeerID := make(map[string]string, len(bundle.Manifest.Devices))
	for _, device := range bundle.Manifest.Devices {
		pid, err := link.PeerIDFromPubKey(device.PublicKey)
		if err != nil {
			continue
		}
		deviceByPeerID[pid.String()] = strings.ToLower(hex.EncodeToString(device.PublicKey))
	}

	for _, pid := range linkNode.ConnectedPrivateNetworkPeers() {
		pubHex, ok := deviceByPeerID[pid.String()]
		if !ok {
			continue
		}
		info := linkNode.Host().Peerstore().PeerInfo(pid)
		meta := metadata[pubHex]
		addrs := make([]string, 0, len(info.Addrs))
		for _, addr := range info.Addrs {
			addrs = append(addrs, addr.String()+"/p2p/"+pid.String())
		}
		meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, addrs...)
		if meta.LastSeen == "" {
			meta.LastSeen = s.now().UTC().Format(time.RFC3339)
		}
		metadata[pubHex] = meta
	}
}

func currentDeviceName(bundle *skyid.Bundle) string {
	if bundle == nil || bundle.Manifest == nil {
		return ""
	}
	for _, device := range bundle.Manifest.Devices {
		if bytes.Equal(device.PublicKey, bundle.Device.PublicKey) {
			return device.Name
		}
	}
	return ""
}

func canonicalDevicePubKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("device public key required")
	}

	lower := strings.ToLower(trimmed)
	if raw, err := hex.DecodeString(lower); err == nil && len(raw) == ed25519.PublicKeySize {
		return lower, nil
	}

	key, err := skykey.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid device public key: %q", value)
	}
	return hex.EncodeToString(key.PublicKey), nil
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	out := append([]string(nil), existing...)
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
