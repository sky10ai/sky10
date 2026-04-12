package link

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// RelayBootstrapSnapshot is the operator-facing summary of the cached live
// relay bootstrap set.
type RelayBootstrapSnapshot struct {
	PeerIDs   []string  `json:"peer_ids,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type relayBootstrapFile struct {
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Relays    []string  `json:"relays,omitempty"`
}

// LoadRelayBootstrapPeers reads the cached static relay set from disk.
func LoadRelayBootstrapPeers(path string) ([]peer.AddrInfo, RelayBootstrapSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return nil, RelayBootstrapSnapshot{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, RelayBootstrapSnapshot{}, nil
		}
		return nil, RelayBootstrapSnapshot{}, fmt.Errorf("reading relay bootstrap cache: %w", err)
	}

	var payload relayBootstrapFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, RelayBootstrapSnapshot{}, fmt.Errorf("parsing relay bootstrap cache: %w", err)
	}

	peers, err := parsePeerInfos(payload.Relays)
	if err != nil {
		return nil, RelayBootstrapSnapshot{}, fmt.Errorf("parsing relay bootstrap peers: %w", err)
	}
	return peers, RelayBootstrapSnapshot{
		PeerIDs:   peerIDsFromInfos(peers),
		UpdatedAt: payload.UpdatedAt.UTC(),
	}, nil
}

// SaveRelayBootstrapPeers stores the static relay set to disk so live relay
// bootstrap can survive restarts and temporary coordinator loss.
func SaveRelayBootstrapPeers(path string, peers []peer.AddrInfo) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	payload := relayBootstrapFile{
		UpdatedAt: time.Now().UTC(),
		Relays:    peerInfosToStrings(peers),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling relay bootstrap cache: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating relay bootstrap cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "relay-bootstrap-*.json")
	if err != nil {
		return fmt.Errorf("creating relay bootstrap temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing relay bootstrap temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting relay bootstrap cache permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing relay bootstrap temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("installing relay bootstrap cache: %w", err)
	}
	return nil
}

// RelayBootstrapPeersFromHostAddrs extracts relay peer infos from active
// autorelay addresses such as /.../p2p/<relay>/p2p-circuit.
func RelayBootstrapPeersFromHostAddrs(addrs []ma.Multiaddr) []peer.AddrInfo {
	seen := make(map[peer.ID]peer.AddrInfo)
	for _, addr := range addrs {
		if addr == nil || !isRelayAddr(addr) {
			continue
		}
		prefix, ok := relayPrefixAddr(addr)
		if !ok {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(prefix)
		if err != nil || info == nil || info.ID == "" {
			continue
		}
		existing, ok := seen[info.ID]
		if !ok {
			seen[info.ID] = *info
			continue
		}
		existing.Addrs = append(existing.Addrs, info.Addrs...)
		seen[info.ID] = existing
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]peer.AddrInfo, 0, len(seen))
	for _, info := range seen {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID.String() < out[j].ID.String()
	})
	return out
}

// LiveRelayHealth summarizes the managed relay tier used for live skylink
// transport.
type LiveRelayHealth struct {
	ConfiguredPeers int        `json:"configured_peers"`
	CachedPeers     int        `json:"cached_peers"`
	ActivePeers     int        `json:"active_peers"`
	CurrentPeerID   string     `json:"current_peer_id,omitempty"`
	ActivePeerIDs   []string   `json:"active_peer_ids,omitempty"`
	ActiveAddrs     []string   `json:"active_addrs,omitempty"`
	LastBootstrapAt *time.Time `json:"last_bootstrap_at,omitempty"`
}

// LiveRelayHealthFromHost returns the current live relay status derived from
// host addresses plus the configured and cached relay sets.
func LiveRelayHealthFromHost(hostAddrs []ma.Multiaddr, configured []peer.AddrInfo, cached RelayBootstrapSnapshot) LiveRelayHealth {
	activeRelayInfos := RelayBootstrapPeersFromHostAddrs(hostAddrs)
	activeAddrs := make([]string, 0, len(hostAddrs))
	for _, addr := range hostAddrs {
		if addr == nil || !isRelayAddr(addr) {
			continue
		}
		activeAddrs = append(activeAddrs, addr.String())
	}
	sort.Strings(activeAddrs)

	peerIDs := peerIDsFromInfos(activeRelayInfos)
	current := ""
	if len(peerIDs) > 0 {
		current = peerIDs[0]
	}

	return LiveRelayHealth{
		ConfiguredPeers: len(configured),
		CachedPeers:     len(cached.PeerIDs),
		ActivePeers:     len(peerIDs),
		CurrentPeerID:   current,
		ActivePeerIDs:   peerIDs,
		ActiveAddrs:     activeAddrs,
		LastBootstrapAt: timePtr(cached.UpdatedAt),
	}
}

func parsePeerInfos(values []string) ([]peer.AddrInfo, error) {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return nil, nil
	}

	infos := make([]peer.AddrInfo, 0, len(cleaned))
	for _, raw := range cleaned {
		addr, err := parseP2PMultiaddr(raw)
		if err != nil {
			return nil, err
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, err
		}
		infos = append(infos, *info)
	}
	return infos, nil
}

func peerInfosToStrings(peers []peer.AddrInfo) []string {
	if len(peers) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(peers))
	for _, info := range peers {
		candidate := info
		addrs, err := peer.AddrInfoToP2pAddrs(&candidate)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			raw := addr.String()
			if _, ok := seen[raw]; ok {
				continue
			}
			seen[raw] = struct{}{}
			out = append(out, raw)
		}
	}
	sort.Strings(out)
	return out
}

func peerIDsFromInfos(peers []peer.AddrInfo) []string {
	if len(peers) == 0 {
		return nil
	}
	out := make([]string, 0, len(peers))
	for _, info := range peers {
		if info.ID == "" {
			continue
		}
		out = append(out, info.ID.String())
	}
	sort.Strings(out)
	return out
}

func relayPrefixAddr(addr ma.Multiaddr) (ma.Multiaddr, bool) {
	if addr == nil {
		return nil, false
	}
	raw := addr.String()
	idx := strings.Index(raw, "/p2p-circuit")
	if idx <= 0 {
		return nil, false
	}
	prefix, err := ma.NewMultiaddr(raw[:idx])
	if err != nil {
		return nil, false
	}
	return prefix, true
}
