package kv

import (
	"context"
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

	nodeA, storeA, syncA := startTestNode(t, ctx, "nodeA", nsKey)
	nodeB, storeB, syncB := startTestNode(t, ctx, "nodeB", nsKey)

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
	nodeA, storeA, _ := startTestNode(t, ctx, "nodeA", nsKey)
	nodeB, storeB, _ := startTestNode(t, ctx, "nodeB", nsKey)

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
	nodeA, storeA, _ := startTestNode(t, ctx, "nodeA", nsKey)
	nodeB, storeB, _ := startTestNode(t, ctx, "nodeB", nsKey)

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
	nodeA, storeA, _ := startTestNode(t, ctx, "nodeA", nsKey)
	nodeB, storeB, _ := startTestNode(t, ctx, "nodeB", nsKey)

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
