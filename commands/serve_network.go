package commands

import (
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/link"
)

func resolvedRelays(cfg *config.Config, override []string, noDefault bool) []string {
	relays := cleanStrings(override)
	if len(relays) > 0 {
		return relays
	}
	if noDefault {
		return nil
	}
	if cfg != nil {
		return cfg.Relays()
	}
	return append([]string(nil), config.DefaultNostrRelays...)
}

func resolvedManagedLiveRelays(cfg *config.Config, override []string) []string {
	relays := cleanStrings(override)
	if len(relays) > 0 {
		return relays
	}
	if cfg != nil {
		return cleanStrings(cfg.LiveRelays())
	}
	return nil
}

func managedLiveRelayWarning(managedRelays []string, resolvedRelayPeers []peer.AddrInfo) string {
	if len(managedRelays) > 0 {
		return ""
	}
	if len(resolvedRelayPeers) > 0 {
		return "starting without managed live relays; using cached relay bootstrap only"
	}
	return "starting without managed live relays; direct transport has no degraded-but-live relay fallback"
}

func resolvedLinkConfig(cfg *config.Config, listenAddrs, bootstrapAddrs, relayAddrs []string, noDefaultBootstrap bool, relayCachePath string) (link.Config, link.RelayBootstrapSnapshot, error) {
	linkCfg := link.Config{Mode: link.Network}

	if addrs := cleanStrings(listenAddrs); len(addrs) > 0 {
		linkCfg.ListenAddrs = addrs
	}

	bootstrap, err := parseBootstrapPeers(bootstrapAddrs)
	if err != nil {
		return link.Config{}, link.RelayBootstrapSnapshot{}, err
	}
	switch {
	case len(bootstrap) > 0:
		linkCfg.BootstrapPeers = bootstrap
	case noDefaultBootstrap:
		linkCfg.BootstrapPeers = []peer.AddrInfo{}
	}

	configuredRelays := resolvedManagedLiveRelays(cfg, relayAddrs)

	cachedRelays, snapshot, err := link.LoadRelayBootstrapPeers(relayCachePath)
	if err != nil {
		return link.Config{}, link.RelayBootstrapSnapshot{}, err
	}

	switch {
	case len(configuredRelays) > 0:
		linkCfg.RelayPeers, err = parseBootstrapPeers(configuredRelays)
		if err != nil {
			return link.Config{}, link.RelayBootstrapSnapshot{}, fmt.Errorf("parsing live relay peers: %w", err)
		}
	case len(cachedRelays) > 0:
		linkCfg.RelayPeers = cachedRelays
	}

	return linkCfg, snapshot, nil
}

func parseBootstrapPeers(values []string) ([]peer.AddrInfo, error) {
	cleaned := cleanStrings(values)
	peers := make([]peer.AddrInfo, 0, len(cleaned))
	for _, raw := range cleaned {
		addr, err := ma.NewMultiaddr(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing bootstrap multiaddr %q: %w", raw, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("parsing bootstrap peer info %q: %w", raw, err)
		}
		peers = append(peers, *info)
	}
	return peers, nil
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
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
