package link

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPathMemoryExpiresHints(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	memory := NewPathMemory()
	memory.now = func() time.Time { return now }

	info := testPeerAddrInfo(t, []string{
		"/ip4/203.0.113.10/udp/4101/quic-v1",
	})
	pid := peer.ID("12D3KooWQp6KSGY7N4r7VJx4nVQ2zVQKp8S7gJ3N4Pp7vQ9QmR4A")

	memory.RecordSuccess(pid, "dht", info)
	memory.RecordFailure(pid, info)

	hint := memory.Snapshot(pid)
	if hint.LastSuccessTransport != "direct_quic" {
		t.Fatalf("last success transport = %q, want direct_quic", hint.LastSuccessTransport)
	}
	if len(hint.AddrFailures) != 1 {
		t.Fatalf("addr failures = %d, want 1", len(hint.AddrFailures))
	}

	now = now.Add(pathFailureTTL + time.Minute)
	hint = memory.Snapshot(pid)
	if len(hint.AddrFailures) != 0 {
		t.Fatalf("addr failures = %d after ttl, want 0", len(hint.AddrFailures))
	}
	if hint.LastSuccessAt.IsZero() {
		t.Fatal("last success should still be present before success ttl")
	}

	now = now.Add(pathSuccessTTL)
	hint = memory.Snapshot(pid)
	if !hint.LastSuccessAt.IsZero() {
		t.Fatalf("last success still present after ttl: %v", hint.LastSuccessAt)
	}
}
