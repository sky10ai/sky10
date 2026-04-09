package link

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pion/stun/v3"
)

// DefaultSTUNServers are public STUN endpoints used for netcheck when the
// caller does not provide an explicit list.
var DefaultSTUNServers = []string{
	"stun.cloudflare.com:3478",
	"stun.l.google.com:19302",
	"stun1.l.google.com:19302",
}

const defaultSTUNProbeTimeout = 2 * time.Second

// NetcheckProbe is one STUN probe attempt against a single server.
type NetcheckProbe struct {
	Server     string `json:"server"`
	PublicAddr string `json:"public_addr,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// NetcheckResult summarizes the current UDP reachability signal from public
// STUN servers. It is intentionally diagnostic-only for now; the daemon does
// not publish guessed public libp2p multiaddrs from these ephemeral probes.
type NetcheckResult struct {
	CheckedAt             time.Time       `json:"checked_at"`
	UDP                   bool            `json:"udp"`
	PublicAddr            string          `json:"public_addr,omitempty"`
	PreferredServer       string          `json:"preferred_server,omitempty"`
	MappingVariesByServer bool            `json:"mapping_varies_by_server,omitempty"`
	Probes                []NetcheckProbe `json:"probes"`
}

// Netcheck probes the provided STUN servers from one UDP socket so the mapped
// address comparison is meaningful across destinations.
func Netcheck(ctx context.Context, servers []string) NetcheckResult {
	if len(servers) == 0 {
		servers = DefaultSTUNServers
	}

	result := NetcheckResult{
		CheckedAt: time.Now().UTC(),
		Probes:    make([]NetcheckProbe, 0, len(servers)),
	}

	if err := ctx.Err(); err != nil {
		result.Probes = append(result.Probes, NetcheckProbe{Error: err.Error()})
		return result
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		result.Probes = append(result.Probes, NetcheckProbe{Error: fmt.Sprintf("opening UDP socket: %v", err)})
		return result
	}
	defer conn.Close()

	var best *NetcheckProbe
	seenMapped := make(map[string]struct{})

	for _, server := range servers {
		probe := runSTUNProbe(ctx, conn, server)
		result.Probes = append(result.Probes, probe)
		if probe.PublicAddr == "" {
			continue
		}

		result.UDP = true
		seenMapped[probe.PublicAddr] = struct{}{}
		if best == nil || probe.LatencyMS < best.LatencyMS {
			p := probe
			best = &p
		}
	}

	if best != nil {
		result.PublicAddr = best.PublicAddr
		result.PreferredServer = best.Server
	}
	result.MappingVariesByServer = len(seenMapped) > 1

	return result
}

func runSTUNProbe(ctx context.Context, conn *net.UDPConn, server string) NetcheckProbe {
	probe := NetcheckProbe{Server: server}

	serverAddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		probe.Error = fmt.Sprintf("resolving server: %v", err)
		return probe
	}

	req := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	deadline, err := probeDeadline(ctx, defaultSTUNProbeTimeout)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}

	start := time.Now()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		probe.Error = fmt.Sprintf("setting write deadline: %v", err)
		return probe
	}
	if _, err := conn.WriteToUDP(req.Raw, serverAddr); err != nil {
		probe.Error = fmt.Sprintf("sending binding request: %v", err)
		return probe
	}

	buf := make([]byte, 2048)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			probe.Error = fmt.Sprintf("setting read deadline: %v", err)
			return probe
		}

		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			probe.Error = fmt.Sprintf("reading binding response: %v", err)
			return probe
		}
		if !sameUDPAddr(from, serverAddr) {
			continue
		}

		msg := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
		if err := msg.Decode(); err != nil {
			probe.Error = fmt.Sprintf("decoding binding response: %v", err)
			return probe
		}
		if msg.TransactionID != req.TransactionID {
			continue
		}

		var xorAddr stun.XORMappedAddress
		if err := xorAddr.GetFrom(msg); err != nil {
			probe.Error = fmt.Sprintf("parsing XOR-MAPPED-ADDRESS: %v", err)
			return probe
		}

		probe.LatencyMS = time.Since(start).Milliseconds()
		probe.PublicAddr = stunAddrString(xorAddr.IP, xorAddr.Port)
		return probe
	}
}

func sameUDPAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

func probeDeadline(ctx context.Context, max time.Duration) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	deadline := time.Now().Add(max)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	return deadline, nil
}

func stunAddrString(ip net.IP, port int) string {
	if ip == nil {
		return ""
	}
	return (&net.UDPAddr{IP: ip, Port: port}).String()
}
