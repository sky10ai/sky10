package fs

import (
	"fmt"
	"time"
)

type fsSyncHealthSnapshot struct {
	Ready           bool   `json:"sync_ready"`
	PeerCount       int    `json:"peer_count"`
	SyncState       string `json:"sync_state,omitempty"`
	SyncMessage     string `json:"sync_message,omitempty"`
	PathIssueCount  int    `json:"path_issue_count,omitempty"`
	PathIssueMsg    string `json:"path_issue_message,omitempty"`
	LastSyncOK      int64  `json:"last_sync_ok,omitempty"`
	LastSyncPeer    string `json:"last_sync_peer,omitempty"`
	LastSyncError   string `json:"last_sync_error,omitempty"`
	LastSyncErrorAt int64  `json:"last_sync_error_at,omitempty"`
}

func summarizeFSSyncState(state fsReplicaSyncState) (lastOK time.Time, lastOKPeer string, lastErrAt time.Time, lastErrPeer string, lastErrMsg string) {
	for peerID, peerState := range state.Peers {
		if peerState.LastSuccessAt.After(lastOK) {
			lastOK = peerState.LastSuccessAt
			lastOKPeer = peerID
		}
		if peerState.LastErrorAt.After(lastErrAt) {
			lastErrAt = peerState.LastErrorAt
			lastErrPeer = peerID
			lastErrMsg = peerState.LastError
		}
	}
	return lastOK, lastOKPeer, lastErrAt, lastErrPeer, lastErrMsg
}

func (dm *DriveManager) peerCount() int {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	if dm.p2pSync == nil || dm.p2pSync.node == nil {
		return 0
	}
	return len(dm.p2pSync.node.ConnectedPrivateNetworkPeers())
}

func (dm *DriveManager) syncHealthSnapshot(id string) fsSyncHealthSnapshot {
	peerCount := dm.peerCount()

	dm.mu.RLock()
	runtime := dm.daemons[id]
	dm.mu.RUnlock()

	state, err := loadFSPeerSyncState(driveDataDir(id))
	if err != nil {
		return fsSyncHealthSnapshot{
			PeerCount:   peerCount,
			SyncState:   "error",
			SyncMessage: fmt.Sprintf("FS sync state unavailable: %v", err),
		}
	}

	issues := dm.pathPolicyIssuesSnapshot(id)
	issueCount, issueMsg := summarizePathPolicyIssues(issues)

	lastOK, lastOKPeer, lastErrAt, lastErrPeer, lastErr := summarizeFSSyncState(state)

	snap := fsSyncHealthSnapshot{
		PeerCount:      peerCount,
		PathIssueCount: issueCount,
		PathIssueMsg:   issueMsg,
	}
	if !lastOK.IsZero() {
		snap.LastSyncOK = lastOK.Unix()
		snap.LastSyncPeer = lastOKPeer
	}
	if !lastErrAt.IsZero() {
		snap.LastSyncErrorAt = lastErrAt.Unix()
		if lastErrPeer != "" {
			snap.LastSyncError = fmt.Sprintf("%s: %s", lastErrPeer, lastErr)
		} else {
			snap.LastSyncError = lastErr
		}
	}

	if runtime == nil || runtime.daemon == nil {
		snap.SyncState = "stopped"
		snap.SyncMessage = "Drive is stopped"
		return snap
	}

	snap.Ready = runtime.daemon.store != nil && runtime.daemon.store.nsID != ""
	if !snap.Ready {
		snap.SyncState = "error"
		snap.SyncMessage = "Drive namespace is not resolved"
		return snap
	}

	hasDurableStorage := runtime.daemon.store != nil && runtime.daemon.store.backend != nil
	p2pEnabled := dm.p2pEnabled()

	switch {
	case issueCount > 0:
		snap.SyncState = "error"
		if issueMsg != "" {
			snap.SyncMessage = issueMsg
		} else {
			snap.SyncMessage = "Windows path issues prevent local materialization"
		}
	case peerCount == 0 && p2pEnabled && !hasDurableStorage:
		snap.SyncState = "waiting"
		snap.SyncMessage = "No connected private-network peers"
	case p2pEnabled && peerCount > 0 && !lastErrAt.IsZero() && lastErrAt.After(lastOK):
		snap.SyncState = "error"
		if snap.LastSyncError != "" {
			snap.SyncMessage = snap.LastSyncError
		} else {
			snap.SyncMessage = "Recent FS anti-entropy failed"
		}
	case peerCount > 0 && p2pEnabled && lastOK.IsZero():
		snap.SyncState = "waiting"
		snap.SyncMessage = "Connected peers found, but no successful FS anti-entropy yet"
	default:
		snap.SyncState = "ok"
	}

	return snap
}

func (dm *DriveManager) p2pEnabled() bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.p2pSync != nil
}
