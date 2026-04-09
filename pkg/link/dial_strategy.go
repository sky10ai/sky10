package link

import (
	"sort"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// PrioritizeAddrInfo returns a copy of info with transport addresses reordered
// according to the current netcheck signal. Stable UDP reachability prefers
// QUIC-style addrs; missing or flaky UDP mapping prefers TCP.
func PrioritizeAddrInfo(info *peer.AddrInfo, result NetcheckResult) *peer.AddrInfo {
	if info == nil {
		return nil
	}

	out := &peer.AddrInfo{
		ID:    info.ID,
		Addrs: append([]ma.Multiaddr(nil), info.Addrs...),
	}
	if len(out.Addrs) < 2 || !hasNetcheckSignal(result) {
		return out
	}

	sort.SliceStable(out.Addrs, func(i, j int) bool {
		return addrDialRank(out.Addrs[i], result) < addrDialRank(out.Addrs[j], result)
	})
	return out
}

func hasNetcheckSignal(result NetcheckResult) bool {
	if result.PublicAddr != "" || result.PreferredServer != "" || result.UDP {
		return true
	}
	for _, probe := range result.Probes {
		if probe.PublicAddr != "" {
			return true
		}
	}
	return false
}

func addrDialRank(addr ma.Multiaddr, result NetcheckResult) int {
	preferTCP := !result.UDP || result.MappingVariesByServer
	switch {
	case preferTCP && isTCPAddr(addr):
		return 0
	case !preferTCP && isQUICAddr(addr):
		return 0
	case preferTCP && isQUICAddr(addr):
		return 2
	case !preferTCP && isTCPAddr(addr):
		return 1
	default:
		return 1
	}
}

func isTCPAddr(addr ma.Multiaddr) bool {
	return multiaddrHasProtocol(addr, ma.P_TCP)
}

func isQUICAddr(addr ma.Multiaddr) bool {
	return multiaddrHasProtocol(addr, ma.P_QUIC) ||
		multiaddrHasProtocol(addr, ma.P_QUIC_V1) ||
		multiaddrHasProtocol(addr, ma.P_WEBTRANSPORT)
}

func multiaddrHasProtocol(addr ma.Multiaddr, code int) bool {
	for _, protocol := range addr.Protocols() {
		if protocol.Code == code {
			return true
		}
	}
	return false
}
