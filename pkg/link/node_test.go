package link

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	p2ppeer "github.com/libp2p/go-libp2p/core/peer"
	skykey "github.com/sky10/sky10/pkg/key"
)

func generateTestNode(t *testing.T) *Node {
	t.Helper()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(k, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func startTestNode(t *testing.T, n *Node) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- n.Run(ctx) }()

	// Wait for host and channels to be ready.
	deadline := time.Now().Add(5 * time.Second)
	for (n.Host() == nil || n.ChannelManager() == nil) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Host() == nil {
		cancel()
		t.Fatal("node did not start in time")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	return cancel
}

func TestNodeNew(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	if n.PeerID() == "" {
		t.Fatal("expected non-empty peer ID")
	}
	if n.Address() == "" {
		t.Fatal("expected non-empty address")
	}
}

func TestNodeRunStop(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	if len(n.Host().Addrs()) == 0 {
		t.Fatal("expected at least one listen address")
	}
}

func TestNodePrivateModeDisablesRelay(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	// In private mode, the node should be listening but not advertising
	// relay addresses.
	for _, addr := range n.Host().Addrs() {
		if addr.String() == "/p2p-circuit" {
			t.Fatal("private mode should not have relay addresses")
		}
	}
}

func TestTwoNodesConnect(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	if n1.PeerID() == n2.PeerID() {
		t.Fatal("nodes should have different peer IDs")
	}

	// Connect n1 to n2.
	n2Addrs := n2.Host().Addrs()
	n2Info := n2.Host().Peerstore().PeerInfo(n2.PeerID())
	n2Info.Addrs = n2Addrs
	if err := n1.Host().Connect(context.Background(), n2Info); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	peers := n1.ConnectedPeers()
	found := false
	for _, p := range peers {
		if p == n2.PeerID() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("n2 not in n1's connected peers")
	}
}

func TestNotifyOwnRoundTrip(t *testing.T) {
	t.Parallel()
	n1 := generateTestNode(t)
	n2 := generateTestNode(t)
	startTestNode(t, n1)
	startTestNode(t, n2)

	// Connect.
	n2Info := n2.Host().Peerstore().PeerInfo(n2.PeerID())
	n2Info.Addrs = n2.Host().Addrs()
	if err := n1.Host().Connect(context.Background(), n2Info); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// n2 registers a sync notify handler.
	var received atomic.Value
	done := make(chan struct{})
	n2.OnSyncNotify(func(from p2ppeer.ID, topic string) {
		received.Store(topic)
		close(done)
	})

	// n1 sends a sync notification.
	if err := n1.NotifyOwn(context.Background(), "kv:default"); err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sync notification")
	}

	got := received.Load().(string)
	if got != "kv:default" {
		t.Fatalf("got topic %q, want %q", got, "kv:default")
	}
}

func TestNotifyOwnNoHost(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	err := n.NotifyOwn(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when node not running")
	}
}

func TestNotifyOwnNoPeers(t *testing.T) {
	t.Parallel()
	n := generateTestNode(t)
	startTestNode(t, n)

	// No connected peers — should succeed silently.
	if err := n.NotifyOwn(context.Background(), "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func addrInfo(t *testing.T, n *Node) p2ppeer.AddrInfo {
	t.Helper()
	info := n.Host().Peerstore().PeerInfo(n.PeerID())
	info.Addrs = n.Host().Addrs()
	return info
}
