package kv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

// TestP2PSyncTwoNodes verifies that two libp2p nodes with KV stores
// sync data bidirectionally over a direct P2P connection.
func TestP2PSyncTwoNodes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shared namespace key (simulates post-join state where both devices
	// have the same key).
	nsKey, _ := skykey.GenerateSymmetricKey()

	nodeA, storeA, syncA, nodeB, storeB, syncB := startSharedTestPair(t, ctx, nsKey)

	// Connect A → B directly.
	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A→B: %v", err)
	}

	// Wait for connection to establish.
	time.Sleep(200 * time.Millisecond)

	// Set a key on A.
	if err := storeA.Set(ctx, "from-a", []byte("hello-from-a")); err != nil {
		t.Fatalf("Set on A: %v", err)
	}

	// The Set triggers pokeSync which calls PushToAll.
	// Give it a moment to propagate.
	waitFor(t, 3*time.Second, func() bool {
		val, ok := storeB.Get("from-a")
		return ok && string(val) == "hello-from-a"
	})

	valB, ok := storeB.Get("from-a")
	if !ok || string(valB) != "hello-from-a" {
		t.Errorf("B.Get(from-a) = %q, %v; want hello-from-a", valB, ok)
	}

	// Now set a key on B, verify it reaches A.
	if err := storeB.Set(ctx, "from-b", []byte("hello-from-b")); err != nil {
		t.Fatalf("Set on B: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		val, ok := storeA.Get("from-b")
		return ok && string(val) == "hello-from-b"
	})

	valA, ok := storeA.Get("from-b")
	if !ok || string(valA) != "hello-from-b" {
		t.Errorf("A.Get(from-b) = %q, %v; want hello-from-b", valA, ok)
	}

	_ = syncA
	_ = syncB
}

// TestP2PSyncMultipleKeys verifies that a batch of keys set on one node
// all arrive on the other.
func TestP2PSyncMultipleKeys(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, _ := skykey.GenerateSymmetricKey()
	nodeA, storeA, _, nodeB, storeB, _ := startSharedTestPair(t, ctx, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Set 10 keys on A. Each Set triggers a push with the full snapshot.
	for i := 0; i < 10; i++ {
		key := "key-" + string(rune('a'+i))
		storeA.Set(ctx, key, []byte("val-"+key))
	}

	waitFor(t, 5*time.Second, func() bool {
		_, ok := storeB.Get("key-j")
		return ok
	})

	// Verify all 10 keys.
	for i := 0; i < 10; i++ {
		key := "key-" + string(rune('a'+i))
		val, ok := storeB.Get(key)
		if !ok || string(val) != "val-"+key {
			t.Errorf("B.Get(%s) = %q, %v; want val-%s", key, val, ok, key)
		}
	}
}

// Regression: pokeSync used to pass the caller's context to PushToAll.
// When Set was called from an RPC handler, the context was cancelled as
// soon as the response was sent — killing the push mid-stream. The fix
// uses context.Background() so pushes survive caller cancellation.
func TestP2PSyncCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, _ := skykey.GenerateSymmetricKey()
	nodeA, storeA, _, nodeB, storeB, _ := startSharedTestPair(t, ctx, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Simulate an RPC context that gets cancelled immediately after Set.
	rpcCtx, rpcCancel := context.WithCancel(ctx)
	if err := storeA.Set(rpcCtx, "survives-cancel", []byte("yes")); err != nil {
		t.Fatal(err)
	}
	rpcCancel() // RPC response sent — context dead

	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeB.Get("survives-cancel")
		return ok && string(val) == "yes"
	})

	val, ok := storeB.Get("survives-cancel")
	if !ok || string(val) != "yes" {
		t.Errorf("B.Get(survives-cancel) = %q, %v; want yes", val, ok)
	}
}

// Regression: a per-peer rate limiter silently dropped pushes that
// happened within the same second. RegisterProtocol's initial PushToAll
// set the lastPush timestamp, then Set's pokeSync was blocked. Rapid
// sequential Sets must all propagate.
func TestP2PSyncRapidSets(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, _ := skykey.GenerateSymmetricKey()
	nodeA, storeA, _, nodeB, storeB, _ := startSharedTestPair(t, ctx, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Simulate RegisterProtocol's initial push.
	storeA.Set(ctx, "init", []byte("warmup"))
	time.Sleep(50 * time.Millisecond)

	// Rapid-fire sets — no delay between them.
	for i := 0; i < 5; i++ {
		storeA.Set(ctx, "rapid", []byte(string(rune('a'+i))))
	}

	// The last write wins (LWW). B must see the final value.
	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeB.Get("rapid")
		return ok && string(val) == "e"
	})

	val, ok := storeB.Get("rapid")
	if !ok || string(val) != "e" {
		t.Errorf("B.Get(rapid) = %q, %v; want e", val, ok)
	}
}

func TestP2PSyncSingleInitiatorConvergesBothPeers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, _ := skykey.GenerateSymmetricKey()
	nodeA, storeA, _, nodeB, storeB, _ := startSharedTestPair(t, ctx, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := storeA.localLog.AppendLocal(Entry{Type: Set, Key: "from-a", Value: []byte("hello-from-a")}); err != nil {
		t.Fatalf("append on A: %v", err)
	}
	if err := storeB.localLog.AppendLocal(Entry{Type: Set, Key: "from-b", Value: []byte("hello-from-b")}); err != nil {
		t.Fatalf("append on B: %v", err)
	}

	// Only A initiates anti-entropy. B now returns a compact summary response
	// and follows up with its delta on a separate stream while A sends its own
	// delta after reading B's summary.
	storeA.p2pSync.PushToAll(ctx)

	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeA.Get("from-b")
		return ok && string(val) == "hello-from-b"
	})

	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeB.Get("from-a")
		return ok && string(val) == "hello-from-a"
	})
}

func TestP2PSyncMergedPeerDeltaRebroadcastsToOtherPeers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, _ := skykey.GenerateSymmetricKey()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceHub, err := skykey.Generate()
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
	manifest.AddDevice(deviceHub.PublicKey, "hub")
	manifest.AddDevice(deviceA.PublicKey, "nodeA")
	manifest.AddDevice(deviceB.PublicKey, "nodeB")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleHub, err := id.New(identity, deviceHub, manifest)
	if err != nil {
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

	nodeHub, storeHub, _ := startTestNodeFromBundle(t, ctx, bundleHub, nsKey)
	nodeA, storeA, syncA := startTestNodeFromBundle(t, ctx, bundleA, nsKey)
	nodeB, storeB, _ := startTestNodeFromBundle(t, ctx, bundleB, nsKey)

	infoHub := nodeHub.Host().Peerstore().PeerInfo(nodeHub.PeerID())
	if err := nodeA.Host().Connect(ctx, infoHub); err != nil {
		t.Fatalf("connect A→hub: %v", err)
	}
	if err := nodeB.Host().Connect(ctx, infoHub); err != nil {
		t.Fatalf("connect B→hub: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := storeA.localLog.AppendLocal(Entry{Type: Set, Key: "fanout/mailbox-event", Value: []byte("delivered")}); err != nil {
		t.Fatalf("append on A: %v", err)
	}

	// Only A initiates the sync. Hub must merge that delta and fan it back out
	// to B in P2P-only mode.
	syncA.PushToAll(ctx)

	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeHub.Get("fanout/mailbox-event")
		return ok && string(val) == "delivered"
	})
	waitFor(t, 5*time.Second, func() bool {
		val, ok := storeB.Get("fanout/mailbox-event")
		return ok && string(val) == "delivered"
	})
}

func TestP2PSyncNamespaceMismatchSurfacesErrorStatus(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKeyA, _ := skykey.GenerateSymmetricKey()
	nsKeyB, _ := skykey.GenerateSymmetricKey()
	nodeA, _, syncA, nodeB, storeB, _ := startSharedPairWithDistinctKeys(t, ctx, nsKeyA, nsKeyB)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	syncA.PushToAll(ctx)

	waitFor(t, 5*time.Second, func() bool {
		status, err := storeB.Status()
		return err == nil && status.SyncState == "error" && strings.Contains(status.SyncMessage, "namespace mismatch")
	})

	status, err := storeB.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.SyncState != "error" {
		t.Fatalf("sync_state = %q, want error", status.SyncState)
	}
	if !strings.Contains(status.SyncMessage, "namespace mismatch") {
		t.Fatalf("sync_message = %q, want namespace mismatch", status.SyncMessage)
	}
}

func TestP2PSyncPeriodicAntiEntropyWithoutWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	home := t.TempDir()
	t.Setenv("HOME", home)

	nsKey, _ := skykey.GenerateSymmetricKey()

	identity, _ := skykey.Generate()
	deviceA, _ := skykey.Generate()
	deviceB, _ := skykey.Generate()
	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "nodeA")
	manifest.AddDevice(deviceB.PublicKey, "nodeB")
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

	CacheKeyLocally("test-sync", bundleA.DeviceID(), nsKey)
	CacheKeyLocally("test-sync", bundleB.DeviceID(), nsKey)

	nodeA, err := link.New(bundleA, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := link.New(bundleB, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go nodeA.Run(ctx)
	go nodeB.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for nodeA.Host() == nil || nodeB.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("hosts did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}

	storeA := New(nil, bundleA.Identity, Config{
		Namespace:           "test-sync",
		DataDir:             t.TempDir(),
		DeviceID:            bundleA.DeviceID(),
		ActorID:             bundleA.DevicePubKeyHex(),
		AntiEntropyInterval: 200 * time.Millisecond,
	}, nil)
	storeB := New(nil, bundleB.Identity, Config{
		Namespace:           "test-sync",
		DataDir:             t.TempDir(),
		DeviceID:            bundleB.DeviceID(),
		ActorID:             bundleB.DevicePubKeyHex(),
		AntiEntropyInterval: 200 * time.Millisecond,
	}, nil)

	runErrA := make(chan error, 1)
	runErrB := make(chan error, 1)
	go func() { runErrA <- storeA.Run(ctx) }()
	go func() { runErrB <- storeB.Run(ctx) }()

	syncA := NewP2PSync(storeA, nodeA, bundleA.Identity, nil)
	syncB := NewP2PSync(storeB, nodeB, bundleB.Identity, nil)
	storeA.SetP2PSync(syncA)
	storeB.SetP2PSync(syncB)
	syncA.RegisterProtocol()
	syncB.RegisterProtocol()

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Bypass Set so convergence depends on periodic anti-entropy rather than
	// the immediate write-triggered push path.
	if err := storeA.localLog.AppendLocal(Entry{Type: Set, Key: "from-a", Value: []byte("hello-from-a")}); err != nil {
		t.Fatal(err)
	}
	if err := storeB.localLog.AppendLocal(Entry{Type: Set, Key: "from-b", Value: []byte("hello-from-b")}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 8*time.Second, func() bool {
		val, ok := storeA.Get("from-b")
		return ok && string(val) == "hello-from-b"
	})
	waitFor(t, 8*time.Second, func() bool {
		val, ok := storeB.Get("from-a")
		return ok && string(val) == "hello-from-a"
	})

	cancel()
	<-runErrA
	<-runErrB
}

// --- helpers ---

func startTestNode(t *testing.T, ctx context.Context, name string, nsKey []byte) (*link.Node, *Store, *P2PSync) {
	t.Helper()

	identity, _ := skykey.Generate()
	device, _ := skykey.Generate()
	manifest := id.NewManifest(identity)
	manifest.AddDevice(device.PublicKey, name)
	manifest.Sign(identity.PrivateKey)
	bundle, err := id.New(identity, device, manifest)
	if err != nil {
		t.Fatal(err)
	}

	node, err := link.New(bundle, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go node.Run(ctx)

	// Wait for host.
	deadline := time.Now().Add(5 * time.Second)
	for node.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}

	store := New(nil, identity, Config{
		Namespace: "test-sync",
		DataDir:   t.TempDir(),
		DeviceID:  bundle.DeviceID(),
		ActorID:   bundle.DevicePubKeyHex(),
	}, nil)

	// Resolve keys with the shared nsKey (bypass the normal resolution).
	nsID := deriveNSID(nsKey, "test-sync")
	store.mu.Lock()
	store.nsKey = nsKey
	store.nsID = nsID
	store.mu.Unlock()

	sync := NewP2PSync(store, node, identity, nil)
	store.SetP2PSync(sync)
	node.Host().SetStreamHandler(KVSyncProtocol, sync.handleStream)

	// Shut down the host before t.TempDir cleanup removes the data dir.
	// t.Cleanup is LIFO, so this runs before TempDir's RemoveAll.
	t.Cleanup(func() {
		node.Close()
	})

	return node, store, sync
}

func startSharedTestPair(t *testing.T, ctx context.Context, nsKey []byte) (*link.Node, *Store, *P2PSync, *link.Node, *Store, *P2PSync) {
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
	manifest.AddDevice(deviceA.PublicKey, "nodeA")
	manifest.AddDevice(deviceB.PublicKey, "nodeB")
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

	nodeA, storeA, syncA := startTestNodeFromBundle(t, ctx, bundleA, nsKey)
	nodeB, storeB, syncB := startTestNodeFromBundle(t, ctx, bundleB, nsKey)
	return nodeA, storeA, syncA, nodeB, storeB, syncB
}

func startSharedPairWithDistinctKeys(t *testing.T, ctx context.Context, nsKeyA, nsKeyB []byte) (*link.Node, *Store, *P2PSync, *link.Node, *Store, *P2PSync) {
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
	manifest.AddDevice(deviceA.PublicKey, "nodeA")
	manifest.AddDevice(deviceB.PublicKey, "nodeB")
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

	nodeA, storeA, syncA := startTestNodeFromBundle(t, ctx, bundleA, nsKeyA)
	nodeB, storeB, syncB := startTestNodeFromBundle(t, ctx, bundleB, nsKeyB)
	return nodeA, storeA, syncA, nodeB, storeB, syncB
}

func startTestNodeFromBundle(t *testing.T, ctx context.Context, bundle *id.Bundle, nsKey []byte) (*link.Node, *Store, *P2PSync) {
	t.Helper()

	node, err := link.New(bundle, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go node.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for node.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}

	store := New(nil, bundle.Identity, Config{
		Namespace: "test-sync",
		DataDir:   t.TempDir(),
		DeviceID:  bundle.DeviceID(),
		ActorID:   bundle.DevicePubKeyHex(),
	}, nil)

	nsID := deriveNSID(nsKey, "test-sync")
	store.mu.Lock()
	store.nsKey = nsKey
	store.nsID = nsID
	store.mu.Unlock()

	sync := NewP2PSync(store, node, bundle.Identity, nil)
	store.SetP2PSync(sync)
	node.Host().SetStreamHandler(KVSyncProtocol, sync.handleStream)

	t.Cleanup(func() {
		node.Close()
	})

	return node, store, sync
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
