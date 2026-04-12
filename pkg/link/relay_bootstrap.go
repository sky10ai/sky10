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
	PeerIDs         []string  `json:"peer_ids,omitempty"`
	PreferredPeerID string    `json:"preferred_peer_id,omitempty"`
	PreferredAt     time.Time `json:"preferred_at,omitempty"`
	LastSwitchAt    time.Time `json:"last_switch_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type relayBootstrapFile struct {
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
	PreferredPeerID string    `json:"preferred_peer_id,omitempty"`
	PreferredAt     time.Time `json:"preferred_at,omitempty"`
	LastSwitchAt    time.Time `json:"last_switch_at,omitempty"`
	Relays          []string  `json:"relays,omitempty"`
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
		PeerIDs:         peerIDsFromInfos(peers),
		PreferredPeerID: strings.TrimSpace(payload.PreferredPeerID),
		PreferredAt:     payload.PreferredAt.UTC(),
		LastSwitchAt:    payload.LastSwitchAt.UTC(),
		UpdatedAt:       payload.UpdatedAt.UTC(),
	}, nil
}

// SaveRelayBootstrapPeers stores the static relay set to disk so live relay
// bootstrap can survive restarts and temporary coordinator loss.
func SaveRelayBootstrapPeers(path string, peers []peer.AddrInfo) error {
	return SaveRelayBootstrapState(path, peers, RelayBootstrapSnapshot{})
}

// SaveRelayBootstrapState stores the static relay set and home-relay
// preference to disk so live relay bootstrap can survive restarts and
// temporary coordinator loss.
func SaveRelayBootstrapState(path string, peers []peer.AddrInfo, snapshot RelayBootstrapSnapshot) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	payload := relayBootstrapFile{
		UpdatedAt:       time.Now().UTC(),
		PreferredPeerID: strings.TrimSpace(snapshot.PreferredPeerID),
		PreferredAt:     snapshot.PreferredAt.UTC(),
		LastSwitchAt:    snapshot.LastSwitchAt.UTC(),
		Relays:          peerInfosToStrings(peers),
	}
	if !snapshot.UpdatedAt.IsZero() {
		payload.UpdatedAt = snapshot.UpdatedAt.UTC()
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
	order := make([]peer.ID, 0, len(addrs))
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
			order = append(order, info.ID)
			continue
		}
		existing.Addrs = append(existing.Addrs, info.Addrs...)
		seen[info.ID] = existing
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]peer.AddrInfo, 0, len(order))
	for _, id := range order {
		out = append(out, seen[id])
	}
	return out
}

// LiveRelayHealth summarizes the managed relay tier used for live skylink
// transport.
type LiveRelayHealth struct {
	ConfiguredPeers int        `json:"configured_peers"`
	CachedPeers     int        `json:"cached_peers"`
	ActivePeers     int        `json:"active_peers"`
	CurrentPeerID   string     `json:"current_peer_id,omitempty"`
	PreferredPeerID string     `json:"preferred_peer_id,omitempty"`
	ActivePeerIDs   []string   `json:"active_peer_ids,omitempty"`
	ActiveAddrs     []string   `json:"active_addrs,omitempty"`
	PreferredAt     *time.Time `json:"preferred_at,omitempty"`
	LastSwitchAt    *time.Time `json:"last_switch_at,omitempty"`
	LastBootstrapAt *time.Time `json:"last_bootstrap_at,omitempty"`
}

// LiveRelayHealthFromHost returns the current live relay status derived from
// host addresses plus the configured and cached relay sets.
func LiveRelayHealthFromHost(hostAddrs []ma.Multiaddr, configured []peer.AddrInfo, cached RelayBootstrapSnapshot) LiveRelayHealth {
	activeRelayInfos := orderRelayInfos(
		RelayBootstrapPeersFromHostAddrs(hostAddrs),
		currentRelayPeerID(RelayBootstrapPeersFromHostAddrs(hostAddrs), cached, configured),
		cached.PeerIDs,
		peerIDsFromInfos(configured),
	)
	activeAddrs := make([]string, 0, len(hostAddrs))
	for _, addr := range hostAddrs {
		if addr == nil || !isRelayAddr(addr) {
			continue
		}
		activeAddrs = append(activeAddrs, addr.String())
	}
	sort.Strings(activeAddrs)

	peerIDs := peerIDsFromInfos(activeRelayInfos)
	current := currentRelayPeerID(activeRelayInfos, cached, configured)

	return LiveRelayHealth{
		ConfiguredPeers: len(configured),
		CachedPeers:     len(cached.PeerIDs),
		ActivePeers:     len(peerIDs),
		CurrentPeerID:   current,
		PreferredPeerID: cached.PreferredPeerID,
		ActivePeerIDs:   peerIDs,
		ActiveAddrs:     activeAddrs,
		PreferredAt:     timePtr(cached.PreferredAt),
		LastSwitchAt:    timePtr(cached.LastSwitchAt),
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
	return out
}

func currentRelayPeerID(active []peer.AddrInfo, cached RelayBootstrapSnapshot, configured []peer.AddrInfo) string {
	if len(active) == 0 {
		return ""
	}
	if cached.PreferredPeerID != "" && relayPeerActive(active, cached.PreferredPeerID) {
		return cached.PreferredPeerID
	}
	ordered := orderRelayInfos(active, "", cached.PeerIDs, peerIDsFromInfos(configured))
	if len(ordered) == 0 {
		return ""
	}
	return ordered[0].ID.String()
}

func orderRelayInfos(active []peer.AddrInfo, currentPeerID string, preferredOrders ...[]string) []peer.AddrInfo {
	if len(active) == 0 {
		return nil
	}

	orderIndex := make(map[string]int)
	nextIndex := 0
	for _, preferredOrder := range preferredOrders {
		for _, peerID := range preferredOrder {
			peerID = strings.TrimSpace(peerID)
			if peerID == "" {
				continue
			}
			if _, ok := orderIndex[peerID]; ok {
				continue
			}
			orderIndex[peerID] = nextIndex
			nextIndex++
		}
	}

	type candidate struct {
		info  peer.AddrInfo
		index int
		order int
	}

	candidates := make([]candidate, 0, len(active))
	for idx, info := range active {
		order := len(orderIndex) + idx + 1
		if ranked, ok := orderIndex[info.ID.String()]; ok {
			order = ranked
		}
		candidates = append(candidates, candidate{
			info:  info,
			index: idx,
			order: order,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if currentPeerID != "" {
			aCurrent := a.info.ID.String() == currentPeerID
			bCurrent := b.info.ID.String() == currentPeerID
			if aCurrent != bCurrent {
				return aCurrent
			}
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.index < b.index
	})

	out := make([]peer.AddrInfo, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.info)
	}
	return out
}

func relayPeerActive(active []peer.AddrInfo, peerID string) bool {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return false
	}
	for _, info := range active {
		if info.ID.String() == peerID {
			return true
		}
	}
	return false
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
