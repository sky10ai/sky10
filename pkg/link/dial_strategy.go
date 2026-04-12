package link

import (
	"sort"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// AddrScore explains why a multiaddr won or lost during resolver ordering.
type AddrScore struct {
	Multiaddr    string   `json:"multiaddr"`
	Transport    string   `json:"transport"`
	Score        int      `json:"score"`
	FailureCount int      `json:"failure_count,omitempty"`
	Reasons      []string `json:"reasons,omitempty"`
}

// PrioritizeAddrInfo returns a copy of info with transport addresses reordered
// according to the current netcheck signal. Stable UDP reachability prefers
// QUIC-style addrs; missing or flaky UDP mapping prefers TCP.
func PrioritizeAddrInfo(info *peer.AddrInfo, result NetcheckResult) *peer.AddrInfo {
	out, _ := PrioritizeAddrInfoWithRelayPreference(info, result, PathHint{}, LiveRelayPreference{})
	return out
}

// PrioritizeAddrInfoWithHint returns a reordered copy of info plus the scoring
// explanation used to rank each address.
func PrioritizeAddrInfoWithHint(info *peer.AddrInfo, result NetcheckResult, hint PathHint) (*peer.AddrInfo, []AddrScore) {
	return PrioritizeAddrInfoWithRelayPreference(info, result, hint, LiveRelayPreference{})
}

// PrioritizeAddrInfoWithRelayPreference returns a reordered copy of info plus
// the scoring explanation used to rank each address, including an optional
// current-relay preference for relayed multiaddrs.
func PrioritizeAddrInfoWithRelayPreference(info *peer.AddrInfo, result NetcheckResult, hint PathHint, relayPreference LiveRelayPreference) (*peer.AddrInfo, []AddrScore) {
	if info == nil {
		return nil, nil
	}

	out := &peer.AddrInfo{
		ID:    info.ID,
		Addrs: append([]ma.Multiaddr(nil), info.Addrs...),
	}
	if len(out.Addrs) == 0 {
		return out, nil
	}

	type rankedAddr struct {
		addr  ma.Multiaddr
		score AddrScore
		index int
	}

	ranked := make([]rankedAddr, 0, len(out.Addrs))
	for idx, addr := range out.Addrs {
		ranked = append(ranked, rankedAddr{
			addr:  addr,
			score: scoreAddr(addr, result, hint, relayPreference),
			index: idx,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score.Score != ranked[j].score.Score {
			return ranked[i].score.Score > ranked[j].score.Score
		}
		return ranked[i].index < ranked[j].index
	})

	scores := make([]AddrScore, 0, len(ranked))
	for idx, rankedAddr := range ranked {
		out.Addrs[idx] = rankedAddr.addr
		scores = append(scores, rankedAddr.score)
	}
	return out, scores
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

func preferredTransportFromScores(scores []AddrScore) string {
	if len(scores) == 0 {
		return ""
	}
	return scores[0].Transport
}

func bestAddrScore(scores []AddrScore) int {
	if len(scores) == 0 {
		return 0
	}
	return scores[0].Score
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

func scoreAddr(addr ma.Multiaddr, result NetcheckResult, hint PathHint, relayPreference LiveRelayPreference) AddrScore {
	score := AddrScore{
		Multiaddr: addr.String(),
		Transport: transportClass(addr),
	}

	if hasNetcheckSignal(result) {
		switch rank := addrDialRank(addr, result); rank {
		case 0:
			score.Score += 40
			score.Reasons = append(score.Reasons, "netcheck_preferred")
		case 1:
			score.Score += 10
			score.Reasons = append(score.Reasons, "netcheck_acceptable")
		default:
			score.Score -= 10
			score.Reasons = append(score.Reasons, "netcheck_deprioritized")
		}
	}

	if score.Transport == "libp2p_relay" {
		switch {
		case !result.UDP || result.MappingVariesByServer:
			score.Score += 15
			score.Reasons = append(score.Reasons, "relay_live_fallback")
		default:
			score.Score -= 10
			score.Reasons = append(score.Reasons, "relay_deprioritized")
		}
	}
	if score.Transport == "libp2p_relay" && relayPreference.CurrentPeerID != "" {
		if relayPeerID := relayPeerID(addr); relayPeerID == relayPreference.CurrentPeerID {
			score.Score += 35
			score.Reasons = append(score.Reasons, "relay_home")
		}
	}

	if hint.LastSuccessTransport != "" && hint.LastSuccessTransport == score.Transport {
		score.Score += 45
		score.Reasons = append(score.Reasons, "last_success_transport")
	}
	if hint.LastSuccessAddr != "" && hint.LastSuccessAddr == score.Multiaddr {
		score.Score += 80
		score.Reasons = append(score.Reasons, "last_success_addr")
	}

	if failure, ok := hint.AddrFailures[score.Multiaddr]; ok {
		penalty := failurePenalty(failure.Count, 20, 80)
		score.Score -= penalty
		score.FailureCount += failure.Count
		score.Reasons = append(score.Reasons, "recent_addr_failure")
	}
	if failure, ok := hint.TransportFailures[score.Transport]; ok {
		penalty := failurePenalty(failure.Count, 15, 45)
		score.Score -= penalty
		score.FailureCount += failure.Count
		score.Reasons = append(score.Reasons, "recent_transport_failure")
	}

	return score
}

func failurePenalty(count, step, max int) int {
	if count <= 0 {
		return 0
	}
	penalty := count * step
	if penalty > max {
		return max
	}
	return penalty
}

func transportClass(addr ma.Multiaddr) string {
	switch {
	case isRelayAddr(addr):
		return "libp2p_relay"
	case isQUICAddr(addr):
		return "direct_quic"
	case isTCPAddr(addr):
		return "direct_tcp"
	default:
		return "other"
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

func isRelayAddr(addr ma.Multiaddr) bool {
	return multiaddrHasProtocol(addr, ma.P_CIRCUIT)
}

func relayPeerID(addr ma.Multiaddr) string {
	prefix, ok := relayPrefixAddr(addr)
	if !ok {
		return ""
	}
	info, err := peer.AddrInfoFromP2pAddr(prefix)
	if err != nil || info == nil {
		return ""
	}
	return info.ID.String()
}

func multiaddrHasProtocol(addr ma.Multiaddr, code int) bool {
	for _, protocol := range addr.Protocols() {
		if protocol.Code == code {
			return true
		}
	}
	return false
}
