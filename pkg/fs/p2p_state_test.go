package fs

import (
	"path/filepath"
	"testing"
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
	if err := replica.recordSyncAttempt("peer-a", "local-1"); err != nil {
		t.Fatalf("recordSyncAttempt: %v", err)
	}
	if err := replica.recordSyncSuccess("peer-a", "local-2", "peer-2"); err != nil {
		t.Fatalf("recordSyncSuccess: %v", err)
	}

	reloaded, err := loadFSPeerSyncState(dir)
	if err != nil {
		t.Fatalf("loadFSPeerSyncState: %v", err)
	}
	if reloaded.NSID != "ns-agents" {
		t.Fatalf("nsid = %q, want %q", reloaded.NSID, "ns-agents")
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
	if peerState.LastSuccessAt.IsZero() {
		t.Fatal("last_success_at should be set")
	}

	if got := fsPeerSyncStatePath(dir); got != filepath.Join(dir, fsPeerSyncStateFile) {
		t.Fatalf("state path = %q", got)
	}
}
