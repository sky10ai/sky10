package kv

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func TestP2PSyncLateConnectViaDHT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	nsKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatal(err)
	}
	bundleA, bundleB := sharedNetworkBundles(t)

	bootstrap := startNetworkBootstrapNode(t, ctx)
	bootstrapPeers := []peer.AddrInfo{bootstrap}
	nodeA, storeA, syncA := startNetworkKVNodeFromBundle(t, ctx, bundleA, nsKey, t.TempDir(), bootstrapPeers)
	nodeB, storeB, syncB := startNetworkKVNodeFromBundle(t, ctx, bundleB, nsKey, t.TempDir(), bootstrapPeers)

	connectNode(t, ctx, nodeA, bootstrap)
	connectNode(t, ctx, nodeB, bootstrap)

	if got := len(nodeA.ConnectedPrivateNetworkPeers()); got != 0 {
		t.Fatalf("nodeA private peers before discovery = %d, want 0", got)
	}
	if got := len(nodeB.ConnectedPrivateNetworkPeers()); got != 0 {
		t.Fatalf("nodeB private peers before discovery = %d, want 0", got)
	}

	if err := storeA.Set(ctx, "from-a", []byte("hello-from-a")); err != nil {
		t.Fatalf("Set on A: %v", err)
	}
	if err := storeB.Set(ctx, "from-b", []byte("hello-from-b")); err != nil {
		t.Fatalf("Set on B: %v", err)
	}

	publishPrivateNetworkRecord(t, ctx, nodeA)
	publishPrivateNetworkRecord(t, ctx, nodeB)

	resolverA := link.NewResolver(nodeA)
	resolverB := link.NewResolver(nodeB)

	waitFor(t, 10*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB, 2*time.Second)
		return connectedToPeer(nodeA, nodeB.PeerID()) && connectedToPeer(nodeB, nodeA.PeerID())
	})

	syncA.PushToAll(context.Background())
	syncB.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		valueFromB, okFromB := storeA.Get("from-b")
		valueFromA, okFromA := storeB.Get("from-a")
		return okFromB && string(valueFromB) == "hello-from-b" &&
			okFromA && string(valueFromA) == "hello-from-a"
	})
}

func TestP2PSyncRediscoveryAfterRestartViaDHT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	nsKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatal(err)
	}
	bundleA, bundleB := sharedNetworkBundles(t)

	bootstrap := startNetworkBootstrapNode(t, ctx)
	bootstrapPeers := []peer.AddrInfo{bootstrap}
	dataDirA := t.TempDir()
	dataDirB := t.TempDir()

	nodeA, storeA, syncA := startNetworkKVNodeFromBundle(t, ctx, bundleA, nsKey, dataDirA, bootstrapPeers)

	ctxB, cancelB := context.WithCancel(ctx)
	nodeB, storeB, syncB := startNetworkKVNodeFromBundle(t, ctxB, bundleB, nsKey, dataDirB, bootstrapPeers)

	connectNode(t, ctx, nodeA, bootstrap)
	connectNode(t, ctx, nodeB, bootstrap)

	if err := storeA.Set(ctx, "before-restart-a", []byte("one")); err != nil {
		t.Fatalf("Set on A: %v", err)
	}
	if err := storeB.Set(ctx, "before-restart-b", []byte("two")); err != nil {
		t.Fatalf("Set on B: %v", err)
	}

	publishPrivateNetworkRecord(t, ctx, nodeA)
	publishPrivateNetworkRecord(t, ctx, nodeB)

	resolverA := link.NewResolver(nodeA)
	resolverB := link.NewResolver(nodeB)

	waitFor(t, 10*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB, 2*time.Second)
		return connectedToPeer(nodeA, nodeB.PeerID()) && connectedToPeer(nodeB, nodeA.PeerID())
	})

	syncA.PushToAll(context.Background())
	syncB.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		valueFromB, okFromB := storeA.Get("before-restart-b")
		valueFromA, okFromA := storeB.Get("before-restart-a")
		return okFromB && string(valueFromB) == "two" &&
			okFromA && string(valueFromA) == "one"
	})

	cancelB()
	_ = nodeB.Close()

	waitFor(t, 10*time.Second, func() bool {
		return !connectedToPeer(nodeA, nodeB.PeerID())
	})

	if err := storeA.Set(ctx, "after-restart", []byte("three")); err != nil {
		t.Fatalf("Set on A after B restart: %v", err)
	}

	ctxB2, cancelB2 := context.WithCancel(ctx)
	defer cancelB2()

	nodeB2, storeB2, syncB2 := startNetworkKVNodeFromBundle(t, ctxB2, bundleB, nsKey, dataDirB, bootstrapPeers)
	connectNode(t, ctx, nodeB2, bootstrap)

	resolverB2 := link.NewResolver(nodeB2)
	waitFor(t, 10*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB2, 2*time.Second)
		return connectedToPeer(nodeA, nodeB2.PeerID()) && connectedToPeer(nodeB2, nodeA.PeerID())
	})

	syncA.PushToAll(context.Background())
	syncB2.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		valueAfterRestart, okAfterRestart := storeB2.Get("after-restart")
		valueFromB, okFromB := storeA.Get("before-restart-b")
		return okAfterRestart && string(valueAfterRestart) == "three" &&
			okFromB && string(valueFromB) == "two"
	})
}

func TestP2PSyncThreeNodesViaDHT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	nsKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatal(err)
	}
	bundleA, bundleB, bundleC := sharedNetworkTripleBundles(t)

	bootstrap := startNetworkBootstrapNode(t, ctx)
	bootstrapPeers := []peer.AddrInfo{bootstrap}
	nodeA, storeA, syncA := startNetworkKVNodeFromBundle(t, ctx, bundleA, nsKey, t.TempDir(), bootstrapPeers)
	nodeB, storeB, syncB := startNetworkKVNodeFromBundle(t, ctx, bundleB, nsKey, t.TempDir(), bootstrapPeers)
	nodeC, storeC, syncC := startNetworkKVNodeFromBundle(t, ctx, bundleC, nsKey, t.TempDir(), bootstrapPeers)

	connectNode(t, ctx, nodeA, bootstrap)
	connectNode(t, ctx, nodeB, bootstrap)
	connectNode(t, ctx, nodeC, bootstrap)

	publishPrivateNetworkRecord(t, ctx, nodeA)
	publishPrivateNetworkRecord(t, ctx, nodeB)
	publishPrivateNetworkRecord(t, ctx, nodeC)

	resolverA := link.NewResolver(nodeA)
	resolverB := link.NewResolver(nodeB)
	resolverC := link.NewResolver(nodeC)

	waitFor(t, 15*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB, 2*time.Second)
		autoConnectWithin(ctx, resolverC, 2*time.Second)
		return connectedToPeer(nodeA, nodeB.PeerID()) &&
			connectedToPeer(nodeA, nodeC.PeerID()) &&
			connectedToPeer(nodeB, nodeA.PeerID()) &&
			connectedToPeer(nodeB, nodeC.PeerID()) &&
			connectedToPeer(nodeC, nodeA.PeerID()) &&
			connectedToPeer(nodeC, nodeB.PeerID())
	})

	if err := storeA.Set(ctx, "from-a", []byte("hello-from-a")); err != nil {
		t.Fatalf("Set on A: %v", err)
	}
	if err := storeB.Set(ctx, "from-b", []byte("hello-from-b")); err != nil {
		t.Fatalf("Set on B: %v", err)
	}
	if err := storeC.Set(ctx, "from-c", []byte("hello-from-c")); err != nil {
		t.Fatalf("Set on C: %v", err)
	}

	syncA.PushToAll(context.Background())
	syncB.PushToAll(context.Background())
	syncC.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		return storeHasValue(storeA, "from-b", "hello-from-b") &&
			storeHasValue(storeA, "from-c", "hello-from-c") &&
			storeHasValue(storeB, "from-a", "hello-from-a") &&
			storeHasValue(storeB, "from-c", "hello-from-c") &&
			storeHasValue(storeC, "from-a", "hello-from-a") &&
			storeHasValue(storeC, "from-b", "hello-from-b")
	})
}

func TestP2PSyncThirdNodeLateDiscoveryViaDHT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	nsKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatal(err)
	}
	bundleA, bundleB, bundleC := sharedNetworkTripleBundles(t)

	bootstrap := startNetworkBootstrapNode(t, ctx)
	bootstrapPeers := []peer.AddrInfo{bootstrap}
	nodeA, storeA, syncA := startNetworkKVNodeFromBundle(t, ctx, bundleA, nsKey, t.TempDir(), bootstrapPeers)
	nodeB, storeB, syncB := startNetworkKVNodeFromBundle(t, ctx, bundleB, nsKey, t.TempDir(), bootstrapPeers)

	connectNode(t, ctx, nodeA, bootstrap)
	connectNode(t, ctx, nodeB, bootstrap)

	publishPrivateNetworkRecord(t, ctx, nodeA)
	publishPrivateNetworkRecord(t, ctx, nodeB)

	resolverA := link.NewResolver(nodeA)
	resolverB := link.NewResolver(nodeB)

	waitFor(t, 15*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB, 2*time.Second)
		return connectedToPeer(nodeA, nodeB.PeerID()) && connectedToPeer(nodeB, nodeA.PeerID())
	})

	if err := storeA.Set(ctx, "before-c", []byte("from-a-before-c")); err != nil {
		t.Fatalf("Set on A before C: %v", err)
	}
	if err := storeB.Set(ctx, "before-c-b", []byte("from-b-before-c")); err != nil {
		t.Fatalf("Set on B before C: %v", err)
	}

	syncA.PushToAll(context.Background())
	syncB.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		return storeHasValue(storeA, "before-c-b", "from-b-before-c") &&
			storeHasValue(storeB, "before-c", "from-a-before-c")
	})

	nodeC, storeC, syncC := startNetworkKVNodeFromBundle(t, ctx, bundleC, nsKey, t.TempDir(), bootstrapPeers)
	connectNode(t, ctx, nodeC, bootstrap)

	publishPrivateNetworkRecord(t, ctx, nodeC)
	publishPrivateNetworkRecord(t, ctx, nodeA)
	publishPrivateNetworkRecord(t, ctx, nodeB)

	resolverC := link.NewResolver(nodeC)
	waitFor(t, 15*time.Second, func() bool {
		autoConnectWithin(ctx, resolverA, 2*time.Second)
		autoConnectWithin(ctx, resolverB, 2*time.Second)
		autoConnectWithin(ctx, resolverC, 2*time.Second)
		return connectedToPeer(nodeA, nodeC.PeerID()) &&
			connectedToPeer(nodeB, nodeC.PeerID()) &&
			connectedToPeer(nodeC, nodeA.PeerID()) &&
			connectedToPeer(nodeC, nodeB.PeerID())
	})

	syncA.PushToAll(context.Background())
	syncB.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		return storeHasValue(storeC, "before-c", "from-a-before-c") &&
			storeHasValue(storeC, "before-c-b", "from-b-before-c")
	})

	if err := storeC.Set(ctx, "from-c", []byte("hello-from-c")); err != nil {
		t.Fatalf("Set on C: %v", err)
	}

	syncC.PushToAll(context.Background())

	waitFor(t, 10*time.Second, func() bool {
		return storeHasValue(storeA, "from-c", "hello-from-c") &&
			storeHasValue(storeB, "from-c", "hello-from-c")
	})
}

func sharedNetworkBundles(t *testing.T) (*id.Bundle, *id.Bundle) {
	t.Helper()

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
	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundleA, bundleB
}

func sharedNetworkTripleBundles(t *testing.T) (*id.Bundle, *id.Bundle, *id.Bundle) {
	t.Helper()

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
	deviceC, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	manifest.AddDevice(deviceC.PublicKey, "node-c")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleC, err := id.New(identity, deviceC, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return bundleA, bundleB, bundleC
}

func startNetworkBootstrapNode(t *testing.T, ctx context.Context) peer.AddrInfo {
	t.Helper()

	h, err := libp2p.New(libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	kad, err := dht.New(ctx, h, dht.Mode(dht.ModeServer), dht.DisableAutoRefresh())
	if err != nil {
		_ = h.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = kad.Close()
		_ = h.Close()
	})
	return peer.AddrInfo{
		ID:    h.ID(),
		Addrs: h.Addrs(),
	}
}

func startNetworkKVNodeFromBundle(t *testing.T, ctx context.Context, bundle *id.Bundle, nsKey []byte, dataDir string, bootstrapPeers []peer.AddrInfo) (*link.Node, *Store, *P2PSync) {
	t.Helper()

	node, err := link.New(bundle, link.Config{
		Mode:           link.Network,
		BootstrapPeers: bootstrapPeers,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	link.RegisterPrivateNetworkHandlers(node)
	startLinkNode(t, ctx, node)

	store := New(nil, bundle.Identity, Config{
		Namespace: "test-sync",
		DataDir:   dataDir,
		DeviceID:  bundle.DeviceID(),
		ActorID:   bundle.DevicePubKeyHex(),
	}, nil)

	store.mu.Lock()
	store.nsKey = nsKey
	store.nsID = deriveNSID(nsKey, "test-sync")
	store.mu.Unlock()

	sync := NewP2PSync(store, node, bundle.Identity, nil)
	store.SetP2PSync(sync)
	sync.RegisterProtocol()

	t.Cleanup(func() {
		_ = node.Close()
	})

	return node, store, sync
}

func startLinkNode(t *testing.T, ctx context.Context, node *link.Node) {
	t.Helper()

	go func() {
		_ = node.Run(ctx)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for node.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func connectNode(t *testing.T, ctx context.Context, node *link.Node, target peer.AddrInfo) {
	t.Helper()

	if err := node.Host().Connect(ctx, target); err != nil {
		t.Fatalf("connect %s -> %s: %v", node.PeerID(), target.ID, err)
	}
}

func publishPrivateNetworkRecord(t *testing.T, ctx context.Context, node *link.Node) {
	t.Helper()

	deadline := time.Now().Add(25 * time.Second)
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := node.PublishRecord(attemptCtx)
		cancel()
		if err == nil {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("publish private-network record for %s: %v", node.PeerID(), err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func autoConnectWithin(ctx context.Context, resolver *link.Resolver, timeout time.Duration) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	link.AutoConnect(attemptCtx, resolver)
}

func connectedToPeer(node *link.Node, want peer.ID) bool {
	for _, pid := range node.ConnectedPrivateNetworkPeers() {
		if pid == want {
			return true
		}
	}
	return false
}

func storeHasValue(store *Store, key, want string) bool {
	value, ok := store.Get(key)
	return ok && string(value) == want
}
