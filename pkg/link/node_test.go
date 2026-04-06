package link

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	p2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

func generateTestNode(t *testing.T) *Node {
	t.Helper()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n, err := NewFromKey(k, Config{Mode: Private}, nil)
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

func TestNodeNetworkModeInitializesDHT(t *testing.T) {
	t.Parallel()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n, err := NewFromKey(k, Config{Mode: Network, BootstrapPeers: []p2ppeer.AddrInfo{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	startTestNode(t, n)
	if n.dht == nil {
		t.Fatal("expected DHT to initialize in network mode")
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

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n1, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

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

func TestConnectedPrivateNetworkPeersFiltersPublicPeers(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n1, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	n3 := generateTestNode(t)

	startTestNode(t, n1)
	startTestNode(t, n2)
	startTestNode(t, n3)

	if err := n1.Host().Connect(context.Background(), addrInfo(t, n2)); err != nil {
		t.Fatalf("connect n1->n2: %v", err)
	}
	if err := n1.Host().Connect(context.Background(), addrInfo(t, n3)); err != nil {
		t.Fatalf("connect n1->n3: %v", err)
	}

	waitForPeers := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if len(n1.ConnectedPeers()) >= want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("expected at least %d connected peers, got %d", want, len(n1.ConnectedPeers()))
	}
	waitForPeers(2)

	privatePeers := n1.ConnectedPrivateNetworkPeers()
	if len(privatePeers) != 1 {
		t.Fatalf("private peer count = %d, want 1", len(privatePeers))
	}
	if privatePeers[0] != n2.PeerID() {
		t.Fatalf("private peer = %s, want %s", privatePeers[0], n2.PeerID())
	}
}

func TestNotifyOwnSkipsPublicPeers(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n1, err := New(bundleA, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := New(bundleB, Config{Mode: Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	n3 := generateTestNode(t)

	startTestNode(t, n1)
	startTestNode(t, n2)
	startTestNode(t, n3)

	if err := n1.Host().Connect(context.Background(), addrInfo(t, n2)); err != nil {
		t.Fatalf("connect n1->n2: %v", err)
	}
	if err := n1.Host().Connect(context.Background(), addrInfo(t, n3)); err != nil {
		t.Fatalf("connect n1->n3: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(n1.ConnectedPeers()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	doneAllowed := make(chan struct{})
	n2.OnSyncNotify(func(from p2ppeer.ID, topic string) {
		if from == n1.PeerID() && topic == "kv:default" {
			select {
			case <-doneAllowed:
			default:
				close(doneAllowed)
			}
		}
	})

	var outsiderReceived atomic.Bool
	n3.OnSyncNotify(func(from p2ppeer.ID, topic string) {
		if from == n1.PeerID() && topic == "kv:default" {
			outsiderReceived.Store(true)
		}
	})

	if err := n1.NotifyOwn(context.Background(), "kv:default"); err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case <-doneAllowed:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for private-network sync notification")
	}

	time.Sleep(200 * time.Millisecond)
	if outsiderReceived.Load() {
		t.Fatal("public peer received private-network sync notification")
	}
}

func addrInfo(t *testing.T, n *Node) p2ppeer.AddrInfo {
	t.Helper()
	info := n.Host().Peerstore().PeerInfo(n.PeerID())
	info.Addrs = n.Host().Addrs()
	return info
}
