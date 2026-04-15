package fs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func TestFSP2PSyncSingleInitiatorConvergesBothPeers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	nodeA, syncA, logA, nodeB, syncB, logB := startSharedFSTestPair(t, ctx, nsID, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := logA.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "from-a.txt",
		Checksum:  "ha",
		Chunks:    []string{"ca"},
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := logB.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "from-b.txt",
		Checksum:  "hb",
		Chunks:    []string{"cb"},
		Device:    "dev-b",
		Timestamp: 110,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	syncA.PushToAll(ctx)

	waitForFS(t, 5*time.Second, func() bool {
		snapA, _ := logA.Snapshot()
		snapB, _ := logB.Snapshot()
		_, aHasRemote := snapA.Lookup("from-b.txt")
		_, bHasRemote := snapB.Lookup("from-a.txt")
		return aHasRemote && bHasRemote
	})

	snapA, _ := logA.Snapshot()
	snapB, _ := logB.Snapshot()
	if _, ok := snapA.Lookup("from-b.txt"); !ok {
		t.Fatal("node A should receive from-b.txt")
	}
	if _, ok := snapB.Lookup("from-a.txt"); !ok {
		t.Fatal("node B should receive from-a.txt")
	}

	_ = syncB
}

func TestFSP2PSyncPropagatesDeleteTombstones(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	nodeA, syncA, logA, nodeB, _, logB := startSharedFSTestPair(t, ctx, nsID, nsKey)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := logB.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "gone.txt",
		Checksum:  "old",
		Chunks:    []string{"c1"},
		Device:    "dev-b",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := logA.Append(opslog.Entry{
		Type:      opslog.Delete,
		Path:      "gone.txt",
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	syncA.PushToAll(ctx)

	waitForFS(t, 5*time.Second, func() bool {
		snapB, _ := logB.Snapshot()
		_, ok := snapB.Lookup("gone.txt")
		return !ok && snapB.DeletedFiles()["gone.txt"]
	})

	snapB, _ := logB.Snapshot()
	if _, ok := snapB.Lookup("gone.txt"); ok {
		t.Fatal("gone.txt should be deleted on node B")
	}
	if !snapB.DeletedFiles()["gone.txt"] {
		t.Fatal("gone.txt tombstone should be present on node B")
	}
}

func startSharedFSTestPair(t *testing.T, ctx context.Context, nsID string, nsKey []byte) (*link.Node, *P2PSync, *opslog.LocalOpsLog, *link.Node, *P2PSync, *opslog.LocalOpsLog) {
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

	nodeA, syncA, logA := startFSTestNodeFromBundle(t, ctx, bundleA, nsID, nsKey)
	nodeB, syncB, logB := startFSTestNodeFromBundle(t, ctx, bundleB, nsID, nsKey)
	return nodeA, syncA, logA, nodeB, syncB, logB
}

func startFSTestNodeFromBundle(t *testing.T, ctx context.Context, bundle *id.Bundle, nsID string, nsKey []byte) (*link.Node, *P2PSync, *opslog.LocalOpsLog) {
	t.Helper()

	node, err := link.New(bundle, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = node.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for node.Host() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}

	logPath := filepath.Join(t.TempDir(), "ops.jsonl")
	localLog := opslog.NewLocalOpsLog(logPath, ShortPubkeyID(bundle.DevicePubKeyHex()))

	sync := NewP2PSync(node, nil)
	sync.AddReplica(P2PSyncReplica{
		ID:       "drive",
		LocalLog: localLog,
		Resolve: func(context.Context) (string, []byte, error) {
			return nsID, append([]byte(nil), nsKey...), nil
		},
	})
	sync.RegisterProtocol()

	t.Cleanup(func() {
		node.Close()
	})

	return node, sync, localLog
}

func waitForFS(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
