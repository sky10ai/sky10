package fs

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestFSPeerSyncStateRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	replica, err := newP2PReplica(P2PSyncReplica{
		ID:       "drive",
		LocalLog: nil,
		Resolve:  nil,
		StateDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	replica.mu.Lock()
	replica.nsID = "ns-agents"
	replica.mu.Unlock()

	if err := replica.persistResolvedNSID("ns-agents"); err != nil {
		t.Fatalf("persistResolvedNSID: %v", err)
	}
	local1 := fsSnapshotState{Digest: "local-1", Files: 1, UpdatedUnix: 10}
	local2 := fsSnapshotState{Digest: "local-2", Files: 2, UpdatedUnix: 20}
	peer2 := fsSnapshotState{Digest: "peer-2", Files: 3, UpdatedUnix: 30}
	if err := replica.recordLocalSummary(local1); err != nil {
		t.Fatalf("recordLocalSummary: %v", err)
	}
	if err := replica.recordSyncAttempt("peer-a", local1); err != nil {
		t.Fatalf("recordSyncAttempt: %v", err)
	}
	if err := replica.recordPeerSummary("peer-a", peer2); err != nil {
		t.Fatalf("recordPeerSummary: %v", err)
	}
	if err := replica.recordSyncSuccess("peer-a", local2, peer2); err != nil {
		t.Fatalf("recordSyncSuccess: %v", err)
	}

	reloaded, err := loadFSPeerSyncState(dir)
	if err != nil {
		t.Fatalf("loadFSPeerSyncState: %v", err)
	}
	if reloaded.NSID != "ns-agents" {
		t.Fatalf("nsid = %q, want %q", reloaded.NSID, "ns-agents")
	}
	if reloaded.LocalSummary == nil || reloaded.LocalSummary.Digest != "local-2" {
		t.Fatalf("local summary = %#v, want digest %q", reloaded.LocalSummary, "local-2")
	}
	peerState, ok := reloaded.Peers["peer-a"]
	if !ok {
		t.Fatal("missing peer-a state")
	}
	if peerState.LastLocalDigest != "local-2" {
		t.Fatalf("last_local_digest = %q, want %q", peerState.LastLocalDigest, "local-2")
	}
	if peerState.LastPeerDigest != "peer-2" {
		t.Fatalf("last_peer_digest = %q, want %q", peerState.LastPeerDigest, "peer-2")
	}
	if peerState.LastLocalSummary == nil || peerState.LastLocalSummary.Digest != "local-2" {
		t.Fatalf("last_local_summary = %#v, want digest %q", peerState.LastLocalSummary, "local-2")
	}
	if peerState.LastPeerSummary == nil || peerState.LastPeerSummary.Digest != "peer-2" {
		t.Fatalf("last_peer_summary = %#v, want digest %q", peerState.LastPeerSummary, "peer-2")
	}
	if peerState.LastSummaryAt.IsZero() {
		t.Fatal("last_summary_at should be set")
	}
	if peerState.LastSuccessAt.IsZero() {
		t.Fatal("last_success_at should be set")
	}

	if got := fsPeerSyncStatePath(dir); got != filepath.Join(dir, fsPeerSyncStateFile) {
		t.Fatalf("state path = %q", got)
	}
}

func TestFSPeerSyncStateShouldSyncPeer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	replica, err := newP2PReplica(P2PSyncReplica{
		ID:       "drive",
		LocalLog: nil,
		Resolve:  nil,
		StateDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	local := fsSnapshotState{Digest: "local-2", Files: 2, UpdatedUnix: 20}
	peer := fsSnapshotState{Digest: "peer-2", Files: 3, UpdatedUnix: 30}
	if err := replica.recordSyncSuccess("peer-a", local, peer); err != nil {
		t.Fatalf("recordSyncSuccess: %v", err)
	}

	if due, _ := replica.shouldSyncPeer("peer-a", local, now, 10*time.Minute); due {
		t.Fatal("peer should not be due immediately after a successful sync")
	}
	if due, _ := replica.shouldSyncPeer("peer-a", fsSnapshotState{Digest: "local-3", Files: 4, UpdatedUnix: 40}, now, 10*time.Minute); !due {
		t.Fatal("peer should be due when the local summary digest changes")
	}
	if due, _ := replica.shouldSyncPeer("peer-a", local, now.Add(11*time.Minute), 10*time.Minute); !due {
		t.Fatal("peer should be due when the last success is stale")
	}
	if due, _ := replica.shouldSyncPeer("peer-b", local, now, 10*time.Minute); !due {
		t.Fatal("unknown peer should be due for sync")
	}
	if err := replica.recordSyncError("peer-c", &local, fmt.Errorf("unknown namespace")); err != nil {
		t.Fatalf("recordSyncError: %v", err)
	}
	if due, _ := replica.shouldSyncPeer("peer-c", local, time.Now().UTC(), 10*time.Minute); due {
		t.Fatal("recent peer sync error should back off")
	}
	if due, _ := replica.shouldSyncPeer("peer-c", local, time.Now().UTC().Add(fsPeerSyncErrorBackoff+time.Second), 10*time.Minute); !due {
		t.Fatal("peer sync error should be due after backoff")
	}
}

func TestFSPeerSyncStatePrunesOldPeers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := time.Now().UTC().Add(-time.Hour)
	state := fsReplicaSyncState{
		Peers: make(map[string]fsPeerSyncState, fsPeerSyncStateMaxPeers+10),
	}
	for i := 0; i < fsPeerSyncStateMaxPeers+10; i++ {
		state.Peers[fmt.Sprintf("peer-%03d", i)] = fsPeerSyncState{
			LastAttemptAt: base.Add(time.Duration(i) * time.Second),
		}
	}

	if err := saveFSPeerSyncState(dir, state); err != nil {
		t.Fatalf("saveFSPeerSyncState: %v", err)
	}
	loaded, err := loadFSPeerSyncState(dir)
	if err != nil {
		t.Fatalf("loadFSPeerSyncState: %v", err)
	}
	if len(loaded.Peers) != fsPeerSyncStateMaxPeers {
		t.Fatalf("peers = %d, want %d", len(loaded.Peers), fsPeerSyncStateMaxPeers)
	}
	if _, ok := loaded.Peers[fmt.Sprintf("peer-%03d", fsPeerSyncStateMaxPeers+9)]; !ok {
		t.Fatal("newest peer state should be kept")
	}
	if _, ok := loaded.Peers["peer-000"]; ok {
		t.Fatal("oldest peer state should be pruned")
	}
}
