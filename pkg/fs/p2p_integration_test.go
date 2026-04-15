package fs

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

type fsTestReplica struct {
	node     *link.Node
	sync     *P2PSync
	log      *opslog.LocalOpsLog
	bundle   *id.Bundle
	stateDir string
}

func TestFSP2PSyncSingleInitiatorConvergesBothPeers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	nodeA, syncA, logA := replicaA.node, replicaA.sync, replicaA.log
	nodeB, syncB, logB := replicaB.node, replicaB.sync, replicaB.log

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

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	nodeA, syncA, logA := replicaA.node, replicaA.sync, replicaA.log
	nodeB, logB := replicaB.node, replicaB.log

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

func TestFSP2PSyncConnectTriggerReplicatesWithoutManualPush(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	nodeA, logA := replicaA.node, replicaA.log
	nodeB, logB := replicaB.node, replicaB.log

	if err := logA.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "connect-only.txt",
		Checksum:  "hc",
		Chunks:    []string{"cc"},
		Device:    "dev-a",
		Timestamp: 150,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}

	waitForFS(t, 5*time.Second, func() bool {
		snapB, _ := logB.Snapshot()
		_, ok := snapB.Lookup("connect-only.txt")
		return ok
	})
}

func TestFSP2PSyncPeriodicAntiEntropyReplicatesWithoutManualPush(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	nodeA, syncA, logA := replicaA.node, replicaA.sync, replicaA.log
	nodeB, logB := replicaB.node, replicaB.log
	syncA.StartAntiEntropy(ctx, 200*time.Millisecond)

	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := logA.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "periodic-only.txt",
		Checksum:  "hp",
		Chunks:    []string{"cp"},
		Device:    "dev-a",
		Timestamp: 175,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	waitForFS(t, 5*time.Second, func() bool {
		snapB, _ := logB.Snapshot()
		_, ok := snapB.Lookup("periodic-only.txt")
		return ok
	})
}

func TestFSP2PSyncLongOfflineCatchUpConvergesFinalState(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)

	offlineEntries := []opslog.Entry{
		{
			Type:      opslog.Put,
			Path:      "alpha.txt",
			Checksum:  "alpha-v1",
			Chunks:    []string{"alpha-c1"},
			Device:    "dev-a",
			Timestamp: 100,
			Seq:       1,
		},
		{
			Type:         opslog.Put,
			Path:         "alpha.txt",
			Checksum:     "alpha-v2",
			PrevChecksum: "alpha-v1",
			Chunks:       []string{"alpha-c2"},
			Device:       "dev-a",
			Timestamp:    110,
			Seq:          2,
		},
		{
			Type:      opslog.Put,
			Path:      "beta.txt",
			Checksum:  "beta-v1",
			Chunks:    []string{"beta-c1"},
			Device:    "dev-a",
			Timestamp: 120,
			Seq:       3,
		},
		{
			Type:         opslog.Delete,
			Path:         "beta.txt",
			PrevChecksum: "beta-v1",
			Device:       "dev-a",
			Timestamp:    130,
			Seq:          4,
		},
		{
			Type:      opslog.Put,
			Path:      "cycle.txt",
			Checksum:  "cycle-v1",
			Chunks:    []string{"cycle-c1"},
			Device:    "dev-a",
			Timestamp: 140,
			Seq:       5,
		},
		{
			Type:         opslog.Delete,
			Path:         "cycle.txt",
			PrevChecksum: "cycle-v1",
			Device:       "dev-a",
			Timestamp:    150,
			Seq:          6,
		},
		{
			Type:      opslog.Put,
			Path:      "cycle.txt",
			Checksum:  "cycle-v2",
			Chunks:    []string{"cycle-c2"},
			Device:    "dev-a",
			Timestamp: 160,
			Seq:       7,
		},
	}
	for _, entry := range offlineEntries {
		if err := replicaA.log.Append(entry); err != nil {
			t.Fatalf("append %s %s: %v", entry.Type, entry.Path, err)
		}
	}

	infoB := replicaB.node.Host().Peerstore().PeerInfo(replicaB.node.PeerID())
	if err := replicaA.node.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}

	waitForFS(t, 5*time.Second, func() bool {
		snapB, _ := replicaB.log.Snapshot()
		alpha, alphaOK := snapB.Lookup("alpha.txt")
		cycle, cycleOK := snapB.Lookup("cycle.txt")
		_, betaOK := snapB.Lookup("beta.txt")
		return alphaOK &&
			alpha.Checksum == "alpha-v2" &&
			cycleOK &&
			cycle.Checksum == "cycle-v2" &&
			!betaOK &&
			snapB.DeletedFiles()["beta.txt"] &&
			!snapB.DeletedFiles()["cycle.txt"]
	})

	snapB, err := replicaB.log.Snapshot()
	if err != nil {
		t.Fatalf("snapshot B: %v", err)
	}
	alpha, ok := snapB.Lookup("alpha.txt")
	if !ok || alpha.Checksum != "alpha-v2" {
		t.Fatalf("alpha.txt = %#v, want checksum %q", alpha, "alpha-v2")
	}
	cycle, ok := snapB.Lookup("cycle.txt")
	if !ok || cycle.Checksum != "cycle-v2" {
		t.Fatalf("cycle.txt = %#v, want checksum %q", cycle, "cycle-v2")
	}
	if _, ok := snapB.Lookup("beta.txt"); ok {
		t.Fatal("beta.txt should remain deleted after offline catch-up")
	}
	if !snapB.DeletedFiles()["beta.txt"] {
		t.Fatal("beta.txt tombstone should survive offline catch-up")
	}
	if snapB.DeletedFiles()["cycle.txt"] {
		t.Fatal("cycle.txt should not retain a tombstone after recreation")
	}
}

func TestFSP2PSyncLongOfflineCatchUpPersistsPeerStateAcrossReplicaReload(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)

	offlineEntries := []opslog.Entry{
		{
			Type:      opslog.Put,
			Path:      "offline.md",
			Checksum:  "offline-v1",
			Chunks:    []string{"offline-c1"},
			Device:    "dev-a",
			Timestamp: 100,
			Seq:       1,
		},
		{
			Type:         opslog.Put,
			Path:         "offline.md",
			Checksum:     "offline-v2",
			PrevChecksum: "offline-v1",
			Chunks:       []string{"offline-c2"},
			Device:       "dev-a",
			Timestamp:    110,
			Seq:          2,
		},
	}
	for _, entry := range offlineEntries {
		if err := replicaA.log.Append(entry); err != nil {
			t.Fatalf("append %s %s: %v", entry.Type, entry.Path, err)
		}
	}

	infoB := replicaB.node.Host().Peerstore().PeerInfo(replicaB.node.PeerID())
	if err := replicaA.node.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}

	waitForFS(t, 5*time.Second, func() bool {
		snapB, _ := replicaB.log.Snapshot()
		fi, ok := snapB.Lookup("offline.md")
		return ok && fi.Checksum == "offline-v2"
	})

	snapA, err := replicaA.log.Snapshot()
	if err != nil {
		t.Fatalf("snapshot A: %v", err)
	}
	summaryA, err := summarizeFSSnapshot(snapA)
	if err != nil {
		t.Fatalf("summarizeFSSnapshot A: %v", err)
	}

	savedState, err := loadFSPeerSyncState(replicaA.stateDir)
	if err != nil {
		t.Fatalf("loadFSPeerSyncState: %v", err)
	}
	if savedState.NSID != nsID {
		t.Fatalf("saved NSID = %q, want %q", savedState.NSID, nsID)
	}
	peerState, ok := savedState.Peers[replicaB.node.PeerID().String()]
	if !ok {
		t.Fatalf("missing peer state for %s", replicaB.node.PeerID())
	}
	if peerState.LastSuccessAt.IsZero() {
		t.Fatal("last_success_at should be set after offline catch-up")
	}
	if peerState.LastLocalSummary == nil || peerState.LastLocalSummary.Digest != summaryA.Digest {
		t.Fatalf("last_local_summary = %#v, want digest %q", peerState.LastLocalSummary, summaryA.Digest)
	}
	if peerState.LastPeerSummary == nil {
		t.Fatal("last_peer_summary should be recorded after offline catch-up")
	}

	savedStateB, err := loadFSPeerSyncState(replicaB.stateDir)
	if err != nil {
		t.Fatalf("loadFSPeerSyncState B: %v", err)
	}
	peerStateB, ok := savedStateB.Peers[replicaA.node.PeerID().String()]
	if !ok {
		t.Fatalf("missing peer state for %s on replica B", replicaA.node.PeerID())
	}
	if peerStateB.LastPeerSummary == nil || peerStateB.LastPeerSummary.Digest != summaryA.Digest {
		t.Fatalf("replica B last_peer_summary = %#v, want digest %q", peerStateB.LastPeerSummary, summaryA.Digest)
	}

	reloaded, err := newP2PReplica(P2PSyncReplica{
		ID:       "drive",
		LocalLog: replicaA.log,
		StateDir: replicaA.stateDir,
		Resolve: func(context.Context) (string, []byte, error) {
			return nsID, append([]byte(nil), nsKey...), nil
		},
	})
	if err != nil {
		t.Fatalf("newP2PReplica reload: %v", err)
	}
	if due, _ := reloaded.shouldSyncPeer(replicaB.node.PeerID().String(), summaryA, time.Now().UTC(), 10*time.Minute); due {
		t.Fatal("reloaded replica should preserve successful peer sync state after offline catch-up")
	}
}

func TestFSP2PChunkFetchWithoutBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	home := t.TempDir()
	t.Setenv("HOME", home)

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatal(err)
	}
	nsID := deriveNSID(nsKey, "agents")

	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	nodeA, syncA, bundleA := replicaA.node, replicaA.sync, replicaA.bundle
	nodeB, syncB, bundleB := replicaB.node, replicaB.sync, replicaB.bundle
	infoB := nodeB.Host().Peerstore().PeerInfo(nodeB.PeerID())
	if err := nodeA.Host().Connect(ctx, infoB); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	storeA := New(nil, bundleA.Identity)
	storeA.SetNamespace("agents")
	storeA.nsID = nsID
	storeA.nsKeys["agents"] = nsKey
	if err := storeA.Put(ctx, "shared.txt", bytes.NewReader([]byte("hello from peer chunk"))); err != nil {
		t.Fatalf("Put on A: %v", err)
	}
	put := storeA.LastPutResult()
	if put == nil {
		t.Fatal("missing put result on A")
	}

	storeB := New(nil, bundleB.Identity)
	storeB.SetNamespace("agents")
	storeB.nsID = nsID
	storeB.nsKeys["agents"] = nsKey
	storeB.SetPeerChunkFetcher(syncB)

	var buf bytes.Buffer
	if err := storeB.GetChunks(ctx, put.Chunks, "agents", &buf); err != nil {
		t.Fatalf("GetChunks on B: %v", err)
	}
	if got := buf.String(); got != "hello from peer chunk" {
		t.Fatalf("content = %q", got)
	}

	// Second read should hit the local cache populated by the peer fetch.
	buf.Reset()
	if err := storeB.GetChunks(ctx, put.Chunks, "agents", &buf); err != nil {
		t.Fatalf("second GetChunks on B: %v", err)
	}
	if got := buf.String(); got != "hello from peer chunk" {
		t.Fatalf("cached content = %q", got)
	}

	_ = syncA
}

func startSharedFSTestReplicaPair(t *testing.T, ctx context.Context, nsID string, nsKey []byte) (*fsTestReplica, *fsTestReplica) {
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

	replicaA := startFSTestReplicaFromBundle(t, ctx, bundleA, nsID, nsKey)
	replicaB := startFSTestReplicaFromBundle(t, ctx, bundleB, nsID, nsKey)
	return replicaA, replicaB
}

func startSharedFSTestPair(t *testing.T, ctx context.Context, nsID string, nsKey []byte) (*link.Node, *P2PSync, *opslog.LocalOpsLog, *link.Node, *P2PSync, *opslog.LocalOpsLog) {
	t.Helper()
	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	return replicaA.node, replicaA.sync, replicaA.log, replicaB.node, replicaB.sync, replicaB.log
}

func startSharedFSTestNodes(t *testing.T, ctx context.Context, nsID string, nsKey []byte) (*link.Node, *P2PSync, *id.Bundle, *link.Node, *P2PSync, *id.Bundle) {
	t.Helper()
	replicaA, replicaB := startSharedFSTestReplicaPair(t, ctx, nsID, nsKey)
	return replicaA.node, replicaA.sync, replicaA.bundle, replicaB.node, replicaB.sync, replicaB.bundle
}

func startFSTestNodeFromBundle(t *testing.T, ctx context.Context, bundle *id.Bundle, nsID string, nsKey []byte) (*link.Node, *P2PSync, *opslog.LocalOpsLog) {
	t.Helper()
	replica := startFSTestReplicaFromBundle(t, ctx, bundle, nsID, nsKey)
	return replica.node, replica.sync, replica.log
}

func startFSTestReplicaFromBundle(t *testing.T, ctx context.Context, bundle *id.Bundle, nsID string, nsKey []byte) *fsTestReplica {
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
	stateDir := filepath.Dir(logPath)
	localLog := opslog.NewLocalOpsLog(logPath, ShortPubkeyID(bundle.DevicePubKeyHex()))

	sync := NewP2PSync(node, nil)
	sync.AddReplica(P2PSyncReplica{
		ID:       "drive",
		LocalLog: localLog,
		StateDir: stateDir,
		Resolve: func(context.Context) (string, []byte, error) {
			return nsID, append([]byte(nil), nsKey...), nil
		},
	})
	sync.RegisterProtocol()

	t.Cleanup(func() {
		node.Close()
	})

	return &fsTestReplica{
		node:     node,
		sync:     sync,
		log:      localLog,
		bundle:   bundle,
		stateDir: stateDir,
	}
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
