package kv

import (
	"fmt"
	"time"
)

type syncHealth struct {
	ready           bool
	readyError      string
	lastSyncOK      time.Time
	lastSyncPeer    string
	lastSyncErrorAt time.Time
	lastSyncError   string
	lastSyncErrPeer string
	mismatchAt      time.Time
	mismatchPeer    string
	mismatchGot     string
	mismatchWant    string
}

type Status struct {
	Namespace       string `json:"namespace"`
	DeviceID        string `json:"device_id"`
	Keys            int    `json:"keys"`
	NSID            string `json:"nsid,omitempty"`
	Ready           bool   `json:"ready"`
	PeerCount       int    `json:"peer_count"`
	ExpectedPeers   int    `json:"expected_peers"`
	SyncState       string `json:"sync_state"`
	SyncMessage     string `json:"sync_message,omitempty"`
	LastSyncOK      string `json:"last_sync_ok,omitempty"`
	LastSyncPeer    string `json:"last_sync_peer,omitempty"`
	LastSyncError   string `json:"last_sync_error,omitempty"`
	LastSyncErrorAt string `json:"last_sync_error_at,omitempty"`
}

func (s *Store) ensureReady() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nsKey != nil && s.nsID != "" {
		return nil
	}
	if s.health.readyError != "" {
		return fmt.Errorf("kv namespace unavailable: %s", s.health.readyError)
	}
	return fmt.Errorf("kv namespace not resolved yet")
}

func (s *Store) markReadyLocked() {
	s.health.ready = true
	s.health.readyError = ""
}

func (s *Store) markResolveError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.ready = false
	s.health.readyError = err.Error()
	s.health.lastSyncError = err.Error()
	s.health.lastSyncErrorAt = time.Now().UTC()
}

func (s *Store) recordSyncSuccess(peer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.lastSyncOK = time.Now().UTC()
	s.health.lastSyncPeer = peer
	s.health.lastSyncError = ""
	s.health.lastSyncErrorAt = time.Time{}
	s.health.lastSyncErrPeer = ""
	s.health.mismatchAt = time.Time{}
	s.health.mismatchPeer = ""
	s.health.mismatchGot = ""
	s.health.mismatchWant = ""
}

func (s *Store) recordSyncError(peer string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.lastSyncError = err.Error()
	s.health.lastSyncErrorAt = time.Now().UTC()
	s.health.lastSyncErrPeer = peer
}

func (s *Store) recordNamespaceMismatch(peer, got, want string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.lastSyncError = fmt.Sprintf("namespace mismatch: got %s want %s", got, want)
	s.health.lastSyncErrorAt = time.Now().UTC()
	s.health.lastSyncErrPeer = peer
	s.health.mismatchAt = s.health.lastSyncErrorAt
	s.health.mismatchPeer = peer
	s.health.mismatchGot = got
	s.health.mismatchWant = want
}

func (s *Store) Status() (Status, error) {
	snap, err := s.localLog.Snapshot()
	if err != nil {
		return Status{}, err
	}

	s.mu.Lock()
	status := Status{
		Namespace:     s.config.Namespace,
		DeviceID:      s.deviceID,
		Keys:          visibleKeyCount(snap),
		NSID:          s.nsID,
		Ready:         s.nsKey != nil && s.nsID != "" && s.health.readyError == "",
		ExpectedPeers: s.config.ExpectedPeers,
	}
	health := s.health
	p2p := s.p2pSync
	s.mu.Unlock()

	if p2p != nil && p2p.node != nil {
		status.PeerCount = len(p2p.node.ConnectedPrivateNetworkPeers())
	}

	switch {
	case !status.Ready:
		status.SyncState = "error"
		if health.readyError != "" {
			status.SyncMessage = health.readyError
		} else {
			status.SyncMessage = "KV namespace is not resolved"
		}
	case !health.mismatchAt.IsZero():
		status.SyncState = "error"
		status.SyncMessage = fmt.Sprintf("Private-network KV namespace mismatch with peer %s", health.mismatchPeer)
	case health.lastSyncError != "":
		status.SyncState = "error"
		status.SyncMessage = health.lastSyncError
	case status.ExpectedPeers > 0 && status.PeerCount == 0:
		status.SyncState = "waiting"
		status.SyncMessage = "No connected private-network peers"
	case status.ExpectedPeers > 0 && health.lastSyncOK.IsZero():
		status.SyncState = "waiting"
		status.SyncMessage = "Connected peers found, but no successful KV sync yet"
	default:
		status.SyncState = "ok"
	}

	if !health.lastSyncOK.IsZero() {
		status.LastSyncOK = health.lastSyncOK.Format(time.RFC3339)
		status.LastSyncPeer = health.lastSyncPeer
	}
	if health.lastSyncError != "" {
		status.LastSyncError = health.lastSyncError
		if !health.lastSyncErrorAt.IsZero() {
			status.LastSyncErrorAt = health.lastSyncErrorAt.Format(time.RFC3339)
		}
	}

	return status, nil
}
