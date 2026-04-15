package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const fsPeerSyncStateFile = "p2p-sync-state.json"

type fsPeerSyncState struct {
	LastAttemptAt   time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt   time.Time `json:"last_success_at,omitempty"`
	LastErrorAt     time.Time `json:"last_error_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	LastLocalDigest string    `json:"last_local_digest,omitempty"`
	LastPeerDigest  string    `json:"last_peer_digest,omitempty"`
}

type fsReplicaSyncState struct {
	NSID  string                     `json:"nsid,omitempty"`
	Peers map[string]fsPeerSyncState `json:"peers,omitempty"`
}

func fsPeerSyncStatePath(dir string) string {
	return filepath.Join(dir, fsPeerSyncStateFile)
}

func loadFSPeerSyncState(dir string) (fsReplicaSyncState, error) {
	if dir == "" {
		return fsReplicaSyncState{}, nil
	}
	path := fsPeerSyncStatePath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fsReplicaSyncState{}, nil
		}
		return fsReplicaSyncState{}, fmt.Errorf("read fs peer sync state: %w", err)
	}
	var state fsReplicaSyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return fsReplicaSyncState{}, fmt.Errorf("decode fs peer sync state: %w", err)
	}
	if state.Peers == nil {
		state.Peers = make(map[string]fsPeerSyncState)
	}
	return state, nil
}

func saveFSPeerSyncState(dir string, state fsReplicaSyncState) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create fs peer sync state dir: %w", err)
	}
	path := fsPeerSyncStatePath(dir)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fs peer sync state: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write fs peer sync state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish fs peer sync state: %w", err)
	}
	return nil
}

func cloneFSPeerSyncState(state fsReplicaSyncState) fsReplicaSyncState {
	cloned := fsReplicaSyncState{
		NSID:  state.NSID,
		Peers: make(map[string]fsPeerSyncState, len(state.Peers)),
	}
	for peerID, peerState := range state.Peers {
		cloned.Peers[peerID] = peerState
	}
	return cloned
}
