package link

import (
	"context"
	"testing"
	"time"
)

func TestGaterPrivateModeRejectsUnknown(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)

	// n1 uses a gater that only allows n2.
	gater := NewGater(Private)
	// Don't allow n2 — connection should fail.
	n1.gater = gater

	startTestNode(t, n1)
	startTestNode(t, n2)

	info := addrInfo(t, n1)
	err := n2.Host().Connect(context.Background(), info)
	if err == nil {
		// Check if actually connected (some libp2p versions don't error).
		time.Sleep(100 * time.Millisecond)
		peers := n1.ConnectedPeers()
		for _, p := range peers {
			if p == n2.PeerID() {
				t.Fatal("private gater should reject unknown peers")
			}
		}
	}
}

func TestGaterPrivateModeAllowsKnown(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)

	// n1 has a private gater that allows n2.
	gater := NewGater(Private)
	gater.Allow(n2.PeerID())
	n1.gater = gater
	// n2 has no gater — accepts all inbound.

	startTestNode(t, n1)
	startTestNode(t, n2)

	// n2 dials n1. n1's gater allows n2 (in allowlist).
	info := addrInfo(t, n1)
	if err := n2.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("allowed peer should connect: %v", err)
	}

	found := false
	for _, p := range n1.ConnectedPeers() {
		if p == n2.PeerID() {
			found = true
		}
	}
	if !found {
		t.Fatal("n2 should be in n1's peer list")
	}
}

func TestGaterNetworkModeAcceptsAll(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n1.config.Mode = Network
	n1.gater = NewGater(Network)

	n2 := generateTestNode(t)
	// n2 has no gater — accepts all.

	startTestNode(t, n1)
	startTestNode(t, n2)

	info := addrInfo(t, n1)
	if err := n2.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("network mode should accept all: %v", err)
	}
}

func TestGaterAllowRevoke(t *testing.T) {
	t.Parallel()
	gater := NewGater(Private)
	id := generateTestNode(t).PeerID()

	if gater.IsAllowed(id) {
		t.Fatal("should not be allowed before Allow")
	}

	gater.Allow(id)
	if !gater.IsAllowed(id) {
		t.Fatal("should be allowed after Allow")
	}

	gater.Revoke(id)
	if gater.IsAllowed(id) {
		t.Fatal("should not be allowed after Revoke")
	}
}

func TestGaterAllowedPeers(t *testing.T) {
	t.Parallel()
	gater := NewGater(Private)
	id1 := generateTestNode(t).PeerID()
	id2 := generateTestNode(t).PeerID()

	gater.Allow(id1)
	gater.Allow(id2)

	peers := gater.AllowedPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 allowed peers, got %d", len(peers))
	}
}
