package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// FSSyncProtocol is the libp2p protocol ID for FS snapshot anti-entropy.
const FSSyncProtocol = protocol.ID("/sky10/fs-sync/1.0.0")
const FSChunkProtocol = protocol.ID("/sky10/fs-chunk/1.0.0")

const fsSyncExchangeTimeout = 5 * time.Second
const fsReconnectSyncMinInterval = 2 * time.Second
const defaultFSAntiEntropyInterval = 10 * time.Second
const fsPeriodicSyncBatchSize = 1

type fsP2PNode interface {
	Host() host.Host
	PeerID() peer.ID
	ConnectedPrivateNetworkPeers() []peer.ID
}

type fsSyncMsg struct {
	Type         string           `json:"type"`                    // "summary", "snapshot", or "error"
	NSID         string           `json:"nsid"`                    // namespace ID
	Summary      *fsSnapshotState `json:"summary,omitempty"`       // logical snapshot digest + counts
	Data         json.RawMessage  `json:"data,omitempty"`          // encrypted full peer snapshot
	Error        string           `json:"error,omitempty"`         // explicit sync failure
	ExpectedNSID string           `json:"expected_nsid,omitempty"` // local expected namespace ID
	ObservedNSID string           `json:"observed_nsid,omitempty"` // remote/received namespace ID
}

type fsChunkMsg struct {
	NSID  string `json:"nsid"`
	Hash  string `json:"hash"`
	Data  []byte `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type fsSnapshotState struct {
	Digest        string `json:"digest"`
	Files         int    `json:"files"`
	Dirs          int    `json:"dirs"`
	Tombstones    int    `json:"tombstones"`
	DirTombstones int    `json:"dir_tombstones"`
	UpdatedUnix   int64  `json:"updated_unix"`
}

// P2PSyncReplica is one drive/namespace registered with the FS P2P sync manager.
type P2PSyncReplica struct {
	ID       string
	LocalLog *opslog.LocalOpsLog
	Resolve  func(context.Context) (string, []byte, error)
	OnChange func()
	StateDir string
}

type p2pReplica struct {
	id       string
	localLog *opslog.LocalOpsLog
	resolve  func(context.Context) (string, []byte, error)
	onChange func()
	stateDir string

	mu        sync.Mutex
	nsID      string
	nsKey     []byte
	syncState fsReplicaSyncState
}

func cloneFSSnapshotState(summary fsSnapshotState) *fsSnapshotState {
	cloned := summary
	return &cloned
}

func sameFSSnapshotState(a, b *fsSnapshotState) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Digest == b.Digest &&
		a.Files == b.Files &&
		a.Dirs == b.Dirs &&
		a.Tombstones == b.Tombstones &&
		a.DirTombstones == b.DirTombstones &&
		a.UpdatedUnix == b.UpdatedUnix
}

func (r *p2pReplica) state(ctx context.Context) (string, []byte, error) {
	r.mu.Lock()
	if r.nsID != "" && len(r.nsKey) > 0 {
		nsID := r.nsID
		nsKey := append([]byte(nil), r.nsKey...)
		r.mu.Unlock()
		return nsID, nsKey, nil
	}
	resolve := r.resolve
	r.mu.Unlock()

	if resolve == nil {
		return "", nil, fmt.Errorf("replica %s: namespace resolver not configured", r.id)
	}

	nsID, nsKey, err := resolve(ctx)
	if err != nil {
		return "", nil, err
	}
	if nsID == "" || len(nsKey) == 0 {
		return "", nil, fmt.Errorf("replica %s: namespace not resolved", r.id)
	}

	r.mu.Lock()
	r.nsID = nsID
	r.nsKey = append([]byte(nil), nsKey...)
	r.mu.Unlock()
	_ = r.persistResolvedNSID(nsID)
	return nsID, append([]byte(nil), nsKey...), nil
}

func newP2PReplica(replica P2PSyncReplica) (*p2pReplica, error) {
	r := &p2pReplica{
		id:       replica.ID,
		localLog: replica.LocalLog,
		resolve:  replica.Resolve,
		onChange: replica.OnChange,
		stateDir: replica.StateDir,
	}
	if replica.StateDir != "" {
		state, err := loadFSPeerSyncState(replica.StateDir)
		if err != nil {
			return nil, err
		}
		r.syncState = state
	}
	if r.syncState.Peers == nil {
		r.syncState.Peers = make(map[string]fsPeerSyncState)
	}
	return r, nil
}

func (r *p2pReplica) persistResolvedNSID(nsID string) error {
	if r == nil || r.stateDir == "" || nsID == "" {
		return nil
	}
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		if state.NSID == nsID {
			return false
		}
		state.NSID = nsID
		return true
	})
}

func (r *p2pReplica) updateSyncState(update func(*fsReplicaSyncState) bool) error {
	if r == nil || r.stateDir == "" || update == nil {
		return nil
	}
	r.mu.Lock()
	if update(&r.syncState) {
		state := cloneFSPeerSyncState(r.syncState)
		dir := r.stateDir
		r.mu.Unlock()
		return saveFSPeerSyncState(dir, state)
	}
	r.mu.Unlock()
	return nil
}

func (r *p2pReplica) syncStateSnapshot() fsReplicaSyncState {
	if r == nil {
		return fsReplicaSyncState{}
	}
	r.mu.Lock()
	state := cloneFSPeerSyncState(r.syncState)
	r.mu.Unlock()
	return state
}

func (r *p2pReplica) recordLocalSummary(summary fsSnapshotState) error {
	if r == nil || r.stateDir == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		changed := false
		if r.nsID != "" && state.NSID != r.nsID {
			state.NSID = r.nsID
			changed = true
		}
		next := cloneFSSnapshotState(summary)
		if !sameFSSnapshotState(state.LocalSummary, next) {
			state.LocalSummary = next
			changed = true
		}
		if changed || state.LocalStateAt.IsZero() {
			state.LocalStateAt = now
			changed = true
		}
		return changed
	})
}

func (r *p2pReplica) recordSyncAttempt(peerID string, localSummary fsSnapshotState) error {
	if r == nil || r.stateDir == "" || peerID == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		if state.Peers == nil {
			state.Peers = make(map[string]fsPeerSyncState)
		}
		if r.nsID != "" && state.NSID != r.nsID {
			state.NSID = r.nsID
		}
		if !sameFSSnapshotState(state.LocalSummary, cloneFSSnapshotState(localSummary)) {
			state.LocalSummary = cloneFSSnapshotState(localSummary)
			state.LocalStateAt = now
		}
		peerState := state.Peers[peerID]
		peerState.LastAttemptAt = now
		peerState.LastLocalDigest = localSummary.Digest
		peerState.LastLocalSummary = cloneFSSnapshotState(localSummary)
		state.Peers[peerID] = peerState
		return true
	})
}

func (r *p2pReplica) recordPeerSummary(peerID string, peerSummary fsSnapshotState) error {
	if r == nil || r.stateDir == "" || peerID == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		if state.Peers == nil {
			state.Peers = make(map[string]fsPeerSyncState)
		}
		peerState := state.Peers[peerID]
		next := cloneFSSnapshotState(peerSummary)
		changed := false
		if !sameFSSnapshotState(peerState.LastPeerSummary, next) {
			peerState.LastPeerSummary = next
			changed = true
		}
		if peerState.LastPeerDigest != peerSummary.Digest {
			peerState.LastPeerDigest = peerSummary.Digest
			changed = true
		}
		if changed || peerState.LastSummaryAt.IsZero() {
			peerState.LastSummaryAt = now
			changed = true
		}
		state.Peers[peerID] = peerState
		return changed
	})
}

func (r *p2pReplica) recordSyncSuccess(peerID string, localSummary, peerSummary fsSnapshotState) error {
	if r == nil || r.stateDir == "" || peerID == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		if state.Peers == nil {
			state.Peers = make(map[string]fsPeerSyncState)
		}
		changed := false
		if r.nsID != "" && state.NSID != r.nsID {
			state.NSID = r.nsID
			changed = true
		}
		local := cloneFSSnapshotState(localSummary)
		if !sameFSSnapshotState(state.LocalSummary, local) {
			state.LocalSummary = local
			changed = true
		}
		if changed || state.LocalStateAt.IsZero() {
			state.LocalStateAt = now
			changed = true
		}
		peerState := state.Peers[peerID]
		peerState.LastAttemptAt = now
		peerState.LastSuccessAt = now
		peerState.LastErrorAt = time.Time{}
		peerState.LastError = ""
		peerState.LastLocalDigest = localSummary.Digest
		peerState.LastLocalSummary = cloneFSSnapshotState(localSummary)
		peerState.LastPeerDigest = peerSummary.Digest
		peerState.LastPeerSummary = cloneFSSnapshotState(peerSummary)
		peerState.LastSummaryAt = now
		state.Peers[peerID] = peerState
		return true
	})
}

func (r *p2pReplica) recordSyncError(peerID string, localSummary *fsSnapshotState, err error) error {
	if r == nil || r.stateDir == "" || peerID == "" || err == nil {
		return nil
	}
	now := time.Now().UTC()
	return r.updateSyncState(func(state *fsReplicaSyncState) bool {
		if state.Peers == nil {
			state.Peers = make(map[string]fsPeerSyncState)
		}
		if r.nsID != "" && state.NSID != r.nsID {
			state.NSID = r.nsID
		}
		if localSummary != nil {
			next := cloneFSSnapshotState(*localSummary)
			if !sameFSSnapshotState(state.LocalSummary, next) {
				state.LocalSummary = next
				state.LocalStateAt = now
			}
		}
		peerState := state.Peers[peerID]
		peerState.LastAttemptAt = now
		peerState.LastErrorAt = now
		peerState.LastError = err.Error()
		if localSummary != nil {
			peerState.LastLocalDigest = localSummary.Digest
			peerState.LastLocalSummary = cloneFSSnapshotState(*localSummary)
		}
		state.Peers[peerID] = peerState
		return true
	})
}

func (r *p2pReplica) shouldSyncPeer(peerID string, localSummary fsSnapshotState, now time.Time, staleAfter time.Duration) (bool, time.Time) {
	if r == nil || peerID == "" {
		return false, time.Time{}
	}
	state := r.syncStateSnapshot()
	peerState, ok := state.Peers[peerID]
	if !ok {
		return true, time.Time{}
	}
	if peerState.LastSuccessAt.IsZero() {
		return true, time.Time{}
	}
	if peerState.LastErrorAt.After(peerState.LastSuccessAt) {
		return true, peerState.LastAttemptAt
	}
	if !sameFSSnapshotState(peerState.LastLocalSummary, cloneFSSnapshotState(localSummary)) {
		return true, peerState.LastAttemptAt
	}
	if staleAfter > 0 && now.Sub(peerState.LastSuccessAt) >= staleAfter {
		return true, peerState.LastSuccessAt
	}
	return false, peerState.LastSuccessAt
}

// P2PSync handles full-snapshot FS anti-entropy over libp2p streams.
// It keeps S3 as the blob/source of truth for now, but peers can exchange
// CRDT metadata directly so convergence no longer depends on snapshot polling.
type P2PSync struct {
	node   fsP2PNode
	logger *slog.Logger

	mu              sync.Mutex
	replicas        map[string]*p2pReplica
	registered      bool
	lastConnectPush map[peer.ID]time.Time
	antiEntropyLoop bool
}

// NewP2PSync creates an FS P2P sync manager.
func NewP2PSync(node fsP2PNode, logger *slog.Logger) *P2PSync {
	return &P2PSync{
		node:            node,
		logger:          componentLogger(logger),
		replicas:        make(map[string]*p2pReplica),
		lastConnectPush: make(map[peer.ID]time.Time),
	}
}

// AddReplica registers a drive/namespace with the FS sync manager.
func (s *P2PSync) AddReplica(replica P2PSyncReplica) {
	if replica.ID == "" || replica.LocalLog == nil || replica.Resolve == nil {
		return
	}

	wrapped, err := newP2PReplica(replica)
	if err != nil {
		s.logger.Warn("fs p2p: load replica sync state failed", "replica", replica.ID, "error", err)
		wrapped = &p2pReplica{
			id:       replica.ID,
			localLog: replica.LocalLog,
			resolve:  replica.Resolve,
			onChange: replica.OnChange,
			stateDir: replica.StateDir,
		}
	}

	s.mu.Lock()
	s.replicas[replica.ID] = wrapped
	registered := s.registered
	s.mu.Unlock()

	if registered {
		go s.PushToAll(context.Background())
	}
}

// RemoveReplica unregisters a drive/namespace.
func (s *P2PSync) RemoveReplica(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.replicas, id)
	s.mu.Unlock()
}

// RegisterProtocol registers the FS sync stream handler on the libp2p host.
func (s *P2PSync) RegisterProtocol() {
	if s == nil || s.node == nil {
		return
	}
	h := s.node.Host()
	if h == nil {
		return
	}
	h.SetStreamHandler(FSSyncProtocol, s.handleStream)
	h.SetStreamHandler(FSChunkProtocol, s.handleChunkStream)
	h.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(_ network.Network, conn network.Conn) {
			s.handlePeerConnect(conn.RemotePeer())
		},
	})

	s.mu.Lock()
	s.registered = true
	s.mu.Unlock()

	s.logger.Info("fs p2p sync protocol registered")
	go s.PushToAll(context.Background())
}

// StartAntiEntropy runs periodic bounded anti-entropy in the background so
// peer convergence does not depend on reconnects or write-triggered pushes.
func (s *P2PSync) StartAntiEntropy(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		interval = defaultFSAntiEntropyInterval
	}

	s.mu.Lock()
	if s.antiEntropyLoop {
		s.mu.Unlock()
		return
	}
	s.antiEntropyLoop = true
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.logger.Debug("fs p2p anti-entropy tick")
				s.pushDue(ctx, fsPeriodicSyncBatchSize, interval)
			}
		}
	}()
}

// PushToAll runs a summary-first anti-entropy round with all connected peers.
func (s *P2PSync) PushToAll(ctx context.Context) {
	if s == nil || s.node == nil {
		return
	}
	peers := s.node.ConnectedPrivateNetworkPeers()
	replicas := s.registeredReplicas()
	if len(peers) == 0 || len(replicas) == 0 {
		return
	}

	s.logger.Info("fs p2p push: broadcasting", "peers", len(peers), "replicas", len(replicas))
	for _, pid := range peers {
		s.pushToPeer(ctx, pid, replicas)
	}
}

type fsSyncTarget struct {
	peer     peer.ID
	replica  *p2pReplica
	lastSync time.Time
}

func (s *P2PSync) pushDue(ctx context.Context, limit int, staleAfter time.Duration) {
	if s == nil || s.node == nil {
		return
	}
	if limit <= 0 {
		limit = 1
	}
	peers := s.node.ConnectedPrivateNetworkPeers()
	replicas := s.registeredReplicas()
	if len(peers) == 0 || len(replicas) == 0 {
		return
	}

	now := time.Now().UTC()
	targets := make([]fsSyncTarget, 0, len(peers)*len(replicas))
	for _, replica := range replicas {
		_, summary, _, _, err := s.loadReplicaSnapshot(ctx, replica)
		if err != nil {
			s.logger.Warn("fs p2p anti-entropy: snapshot failed", "replica", replica.id, "error", err)
			continue
		}
		if err := replica.recordLocalSummary(summary); err != nil {
			s.logger.Warn("fs p2p anti-entropy: persist local summary failed", "replica", replica.id, "error", err)
		}
		for _, pid := range peers {
			if pid == s.node.PeerID() {
				continue
			}
			if due, lastSync := replica.shouldSyncPeer(pid.String(), summary, now, staleAfter); due {
				targets = append(targets, fsSyncTarget{
					peer:     pid,
					replica:  replica,
					lastSync: lastSync,
				})
			}
		}
	}
	if len(targets) == 0 {
		return
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].lastSync.Equal(targets[j].lastSync) {
			if targets[i].peer == targets[j].peer {
				return targets[i].replica.id < targets[j].replica.id
			}
			return targets[i].peer.String() < targets[j].peer.String()
		}
		if targets[i].lastSync.IsZero() {
			return true
		}
		if targets[j].lastSync.IsZero() {
			return false
		}
		return targets[i].lastSync.Before(targets[j].lastSync)
	})
	if len(targets) > limit {
		targets = targets[:limit]
	}
	for _, target := range targets {
		go func(target fsSyncTarget) {
			boundedCtx, cancel := boundedFSSyncContext(ctx)
			defer cancel()
			s.syncWithPeer(boundedCtx, target.peer, target.replica)
		}(target)
	}
}

func (s *P2PSync) pushToPeer(ctx context.Context, pid peer.ID, replicas []*p2pReplica) {
	if pid == s.node.PeerID() {
		return
	}
	if replicas == nil {
		replicas = s.registeredReplicas()
	}
	for _, replica := range replicas {
		go func(pid peer.ID, replica *p2pReplica) {
			boundedCtx, cancel := boundedFSSyncContext(ctx)
			defer cancel()
			s.syncWithPeer(boundedCtx, pid, replica)
		}(pid, replica)
	}
}

func (s *P2PSync) handlePeerConnect(pid peer.ID) {
	if s == nil || s.node == nil || pid == "" || pid == s.node.PeerID() {
		return
	}
	now := time.Now()
	s.mu.Lock()
	last := s.lastConnectPush[pid]
	if !last.IsZero() && now.Sub(last) < fsReconnectSyncMinInterval {
		s.mu.Unlock()
		return
	}
	s.lastConnectPush[pid] = now
	replicas := make([]*p2pReplica, 0, len(s.replicas))
	for _, replica := range s.replicas {
		if replica != nil {
			replicas = append(replicas, replica)
		}
	}
	s.mu.Unlock()

	if len(replicas) == 0 {
		return
	}
	s.logger.Info("fs p2p: peer connected, scheduling anti-entropy", "peer", pid, "replicas", len(replicas))
	s.pushToPeer(context.Background(), pid, replicas)
}

func (s *P2PSync) syncWithPeer(ctx context.Context, pid peer.ID, replica *p2pReplica) {
	snap, summary, nsID, nsKey, err := s.loadReplicaSnapshot(ctx, replica)
	if err != nil {
		s.logger.Warn("fs p2p push: snapshot failed", "peer", pid, "replica", replica.id, "error", err)
		return
	}
	if err := replica.recordLocalSummary(summary); err != nil {
		s.logger.Warn("fs p2p push: persist local summary failed", "peer", pid, "replica", replica.id, "error", err)
	}
	if err := replica.recordSyncAttempt(pid.String(), summary); err != nil {
		s.logger.Warn("fs p2p push: persist attempt failed", "peer", pid, "replica", replica.id, "error", err)
	}

	response, err := s.requestSummary(ctx, pid, nsID, summary)
	if err != nil {
		if stateErr := replica.recordSyncError(pid.String(), &summary, err); stateErr != nil {
			s.logger.Warn("fs p2p push: persist error failed", "peer", pid, "replica", replica.id, "error", stateErr)
		}
		if response != nil && response.ExpectedNSID != "" {
			s.logger.Warn("fs p2p push: namespace mismatch",
				"peer", pid,
				"replica", replica.id,
				"got", response.ObservedNSID,
				"want", response.ExpectedNSID,
			)
		} else {
			s.logger.Warn("fs p2p push: summary exchange failed", "peer", pid, "replica", replica.id, "error", err)
		}
		return
	}

	if response != nil {
		if response.Summary != nil {
			if err := replica.recordPeerSummary(pid.String(), *response.Summary); err != nil {
				s.logger.Warn("fs p2p push: persist peer summary failed", "peer", pid, "replica", replica.id, "error", err)
			}
		}
		if merged, err := s.mergeSnapshotMessage(*response, pid); err != nil {
			s.logger.Warn("fs p2p push: merge response failed", "peer", pid, "replica", replica.id, "error", err)
		} else if merged > 0 {
			s.logger.Info("fs p2p push: merged peer snapshot", "peer", pid, "replica", replica.id, "entries", merged)
		}
	}

	if response == nil || response.Summary == nil {
		s.logger.Warn("fs p2p push: peer did not return a snapshot summary", "peer", pid, "replica", replica.id)
		return
	}

	latest, latestSummary, _, _, err := s.loadReplicaSnapshot(ctx, replica)
	if err != nil {
		if stateErr := replica.recordSyncError(pid.String(), &summary, err); stateErr != nil {
			s.logger.Warn("fs p2p push: persist refresh error failed", "peer", pid, "replica", replica.id, "error", stateErr)
		}
		s.logger.Warn("fs p2p push: snapshot refresh failed", "peer", pid, "replica", replica.id, "error", err)
		return
	}
	if latestSummary.Digest == response.Summary.Digest {
		if err := replica.recordSyncSuccess(pid.String(), latestSummary, *response.Summary); err != nil {
			s.logger.Warn("fs p2p push: persist success failed", "peer", pid, "replica", replica.id, "error", err)
		}
		return
	}

	// If our local state changed after merging the peer response, send the
	// full snapshot back so both peers converge in one round.
	if err := s.sendSnapshotMessage(ctx, pid, nsID, nsKey, latest); err != nil {
		if stateErr := replica.recordSyncError(pid.String(), &latestSummary, err); stateErr != nil {
			s.logger.Warn("fs p2p push: persist send error failed", "peer", pid, "replica", replica.id, "error", stateErr)
		}
		s.logger.Warn("fs p2p push: send snapshot failed", "peer", pid, "replica", replica.id, "error", err)
		return
	}
	if err := replica.recordSyncSuccess(pid.String(), latestSummary, *response.Summary); err != nil {
		s.logger.Warn("fs p2p push: persist success failed", "peer", pid, "replica", replica.id, "error", err)
	}
	s.logger.Info("fs p2p push: sent snapshot", "peer", pid, "replica", replica.id, "files", latest.Len())
	_ = snap
}

func (s *P2PSync) requestSummary(ctx context.Context, pid peer.ID, nsID string, summary fsSnapshotState) (*fsSyncMsg, error) {
	h := s.node.Host()
	if h == nil {
		return nil, fmt.Errorf("host not running")
	}

	stream, err := h.NewStream(ctx, pid, FSSyncProtocol)
	if err != nil {
		return nil, fmt.Errorf("opening stream: %w", err)
	}
	defer stream.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}

	msg := fsSyncMsg{
		Type:    "summary",
		NSID:    nsID,
		Summary: &summary,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := writeFSSyncMsg(stream, payload); err != nil {
		return nil, fmt.Errorf("writing summary: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return nil, fmt.Errorf("closing write: %w", err)
	}

	responsePayload, err := readFSSyncMsg(stream)
	if err != nil {
		return nil, fmt.Errorf("reading summary response: %w", err)
	}

	var response fsSyncMsg
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return nil, fmt.Errorf("unmarshal summary response: %w", err)
	}
	if response.Type == "error" {
		return &response, fmt.Errorf("%s", response.Error)
	}
	return &response, nil
}

func (s *P2PSync) sendSnapshotMessage(ctx context.Context, pid peer.ID, nsID string, nsKey []byte, snap *opslog.Snapshot) error {
	h := s.node.Host()
	if h == nil {
		return fmt.Errorf("host not running")
	}

	encJSON, err := encodeFSSnapshotData(nsKey, snap)
	if err != nil {
		return err
	}
	summary, err := summarizeFSSnapshot(snap)
	if err != nil {
		return err
	}

	msg := fsSyncMsg{
		Type:    "snapshot",
		NSID:    nsID,
		Summary: &summary,
		Data:    encJSON,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	stream, err := h.NewStream(ctx, pid, FSSyncProtocol)
	if err != nil {
		return fmt.Errorf("opening stream: %w", err)
	}
	defer stream.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}

	if err := writeFSSyncMsg(stream, payload); err != nil {
		return fmt.Errorf("writing snapshot: %w", err)
	}
	return stream.CloseWrite()
}

func (s *P2PSync) handleStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(fsSyncExchangeTimeout))

	payload, err := readFSSyncMsg(stream)
	if err != nil {
		s.logger.Warn("fs p2p: read failed", "error", err)
		return
	}

	var msg fsSyncMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		s.logger.Warn("fs p2p: unmarshal failed", "error", err)
		return
	}

	switch msg.Type {
	case "summary":
		s.handleSummary(stream, msg)
	case "snapshot":
		if _, err := s.mergeSnapshotMessage(msg, stream.Conn().RemotePeer()); err != nil {
			s.logger.Warn("fs p2p: merge snapshot failed", "peer", stream.Conn().RemotePeer(), "error", err)
		}
	case "error":
		s.logger.Warn("fs p2p: peer reported sync error", "peer", stream.Conn().RemotePeer(), "error", msg.Error)
	default:
		s.logger.Warn("fs p2p: unknown message type", "type", msg.Type)
	}
}

// GetChunk requests a raw encrypted blob for a chunk from connected peers.
func (s *P2PSync) GetChunk(ctx context.Context, nsID, chunkHash string) ([]byte, error) {
	if s == nil || s.node == nil {
		return nil, fmt.Errorf("p2p sync not configured")
	}
	peers := s.node.ConnectedPrivateNetworkPeers()
	if len(peers) == 0 {
		return nil, fmt.Errorf("no connected private-network peers")
	}
	for _, pid := range peers {
		if pid == s.node.PeerID() {
			continue
		}
		raw, err := s.requestChunk(ctx, pid, nsID, chunkHash)
		if err == nil {
			return raw, nil
		}
		s.logger.Debug("fs p2p chunk request failed", "peer", pid, "hash", chunkHash, "error", err)
	}
	return nil, fmt.Errorf("chunk %s not available from connected peers", chunkHash)
}

func (s *P2PSync) requestChunk(ctx context.Context, pid peer.ID, nsID, chunkHash string) ([]byte, error) {
	h := s.node.Host()
	if h == nil {
		return nil, fmt.Errorf("host not running")
	}

	stream, err := h.NewStream(ctx, pid, FSChunkProtocol)
	if err != nil {
		return nil, fmt.Errorf("opening chunk stream: %w", err)
	}
	defer stream.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}

	payload, err := json.Marshal(fsChunkMsg{NSID: nsID, Hash: chunkHash})
	if err != nil {
		return nil, err
	}
	if err := writeFSSyncMsg(stream, payload); err != nil {
		return nil, fmt.Errorf("writing chunk request: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return nil, fmt.Errorf("closing chunk request: %w", err)
	}

	respPayload, err := readFSSyncMsg(stream)
	if err != nil {
		return nil, fmt.Errorf("reading chunk response: %w", err)
	}

	var resp fsChunkMsg
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		return nil, fmt.Errorf("decoding chunk response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("empty chunk response")
	}
	return resp.Data, nil
}

func (s *P2PSync) handleChunkStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(fsSyncExchangeTimeout))

	payload, err := readFSSyncMsg(stream)
	if err != nil {
		s.logger.Warn("fs p2p chunk: read failed", "error", err)
		return
	}

	var msg fsChunkMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		s.logger.Warn("fs p2p chunk: unmarshal failed", "error", err)
		return
	}

	resolvedNSID := msg.NSID
	if replica, _ := s.replicaForNSID(context.Background(), msg.NSID); replica != nil {
		if replicaNSID, _, err := replica.state(context.Background()); err == nil && replicaNSID != "" {
			resolvedNSID = replicaNSID
		}
	}

	raw, err := readLocalBlob(resolvedNSID, msg.Hash)
	resp := fsChunkMsg{NSID: resolvedNSID, Hash: msg.Hash}
	if err != nil {
		resp.Error = fmt.Sprintf("chunk not available: %v", err)
	} else {
		resp.Data = raw
	}

	respPayload, err := json.Marshal(resp)
	if err != nil {
		s.logger.Warn("fs p2p chunk: marshal response failed", "error", err)
		return
	}
	if err := writeFSSyncMsg(stream, respPayload); err != nil {
		s.logger.Warn("fs p2p chunk: write response failed", "error", err)
	}
}

func (s *P2PSync) handleSummary(stream network.Stream, msg fsSyncMsg) {
	from := stream.Conn().RemotePeer()
	replica, exact := s.replicaForNSID(context.Background(), msg.NSID)
	if replica == nil {
		s.writeErrorResponse(stream, "unknown namespace", "", msg.NSID)
		s.logger.Warn("fs p2p: unknown namespace", "nsid", msg.NSID)
		return
	}

	localSnap, localSummary, nsID, nsKey, err := s.loadReplicaSnapshot(context.Background(), replica)
	if err != nil {
		s.writeErrorResponse(stream, "namespace not resolved", "", msg.NSID)
		s.logger.Warn("fs p2p: snapshot load failed", "replica", replica.id, "error", err)
		return
	}
	if err := replica.recordLocalSummary(localSummary); err != nil {
		s.logger.Warn("fs p2p: persist local summary failed", "peer", from, "replica", replica.id, "error", err)
	}
	if !exact || msg.NSID != nsID {
		s.writeErrorResponse(stream, fmt.Sprintf("namespace mismatch: got %s want %s", msg.NSID, nsID), nsID, msg.NSID)
		s.logger.Warn("fs p2p: namespace mismatch", "got", msg.NSID, "want", nsID)
		return
	}
	if msg.Summary != nil {
		if err := replica.recordPeerSummary(from.String(), *msg.Summary); err != nil {
			s.logger.Warn("fs p2p: persist peer summary failed", "peer", from, "replica", replica.id, "error", err)
		}
	}

	resp := fsSyncMsg{
		Type:    "summary",
		NSID:    nsID,
		Summary: &localSummary,
	}
	if msg.Summary == nil || msg.Summary.Digest != localSummary.Digest {
		encJSON, err := encodeFSSnapshotData(nsKey, localSnap)
		if err != nil {
			s.writeErrorResponse(stream, "encoding snapshot failed", nsID, msg.NSID)
			s.logger.Warn("fs p2p: encode snapshot failed", "replica", replica.id, "error", err)
			return
		}
		resp.Data = encJSON
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		s.logger.Warn("fs p2p: marshal summary response failed", "error", err)
		return
	}
	if err := writeFSSyncMsg(stream, payload); err != nil {
		s.logger.Warn("fs p2p: write summary response failed", "error", err)
		if stateErr := replica.recordSyncError(from.String(), &localSummary, err); stateErr != nil {
			s.logger.Warn("fs p2p: persist response error failed", "peer", from, "replica", replica.id, "error", stateErr)
		}
		return
	}
	peerSummary := fsSnapshotState{}
	if msg.Summary != nil {
		peerSummary = *msg.Summary
	}
	if err := replica.recordSyncSuccess(from.String(), localSummary, peerSummary); err != nil {
		s.logger.Warn("fs p2p: persist response success failed", "peer", from, "replica", replica.id, "error", err)
	}
}

func (s *P2PSync) mergeSnapshotMessage(msg fsSyncMsg, from peer.ID) (int, error) {
	replica, exact := s.replicaForNSID(context.Background(), msg.NSID)
	if replica == nil {
		return 0, fmt.Errorf("unknown namespace: %s", msg.NSID)
	}

	nsID, nsKey, err := replica.state(context.Background())
	if err != nil {
		return 0, err
	}
	if !exact || msg.NSID != nsID {
		return 0, fmt.Errorf("namespace mismatch: got %s want %s", msg.NSID, nsID)
	}
	if len(msg.Data) == 0 {
		return 0, nil
	}

	remote, err := decodeFSSnapshotData(nsKey, msg.Data)
	if err != nil {
		return 0, err
	}

	merged, err := mergePeerSnapshot(replica.localLog, remote)
	if err != nil {
		if stateErr := replica.recordSyncError(from.String(), nil, err); stateErr != nil {
			s.logger.Warn("fs p2p: persist merge error failed", "peer", from, "replica", replica.id, "error", stateErr)
		}
		return 0, err
	}
	if merged > 0 && replica.onChange != nil {
		replica.onChange()
	}
	localSummary := fsSnapshotState{}
	if snap, summary, _, _, err := s.loadReplicaSnapshot(context.Background(), replica); err == nil {
		_ = snap
		localSummary = summary
		if err := replica.recordLocalSummary(summary); err != nil {
			s.logger.Warn("fs p2p: persist local summary failed", "peer", from, "replica", replica.id, "error", err)
		}
	}
	if msg.Summary != nil {
		if err := replica.recordPeerSummary(from.String(), *msg.Summary); err != nil {
			s.logger.Warn("fs p2p: persist peer summary failed", "peer", from, "replica", replica.id, "error", err)
		}
	}
	peerSummary := fsSnapshotState{}
	if msg.Summary != nil {
		peerSummary = *msg.Summary
	}
	if err := replica.recordSyncSuccess(from.String(), localSummary, peerSummary); err != nil {
		s.logger.Warn("fs p2p: persist merge success failed", "peer", from, "replica", replica.id, "error", err)
	}
	return merged, nil
}

func (s *P2PSync) loadReplicaSnapshot(ctx context.Context, replica *p2pReplica) (*opslog.Snapshot, fsSnapshotState, string, []byte, error) {
	nsID, nsKey, err := replica.state(ctx)
	if err != nil {
		return nil, fsSnapshotState{}, "", nil, err
	}
	snap, err := replica.localLog.Snapshot()
	if err != nil {
		return nil, fsSnapshotState{}, "", nil, err
	}
	summary, err := summarizeFSSnapshot(snap)
	if err != nil {
		return nil, fsSnapshotState{}, "", nil, err
	}
	if err := replica.recordLocalSummary(summary); err != nil {
		s.logger.Warn("fs p2p: persist local summary failed", "replica", replica.id, "error", err)
	}
	return snap, summary, nsID, nsKey, nil
}

func (s *P2PSync) registeredReplicas() []*p2pReplica {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*p2pReplica, 0, len(s.replicas))
	for _, replica := range s.replicas {
		if replica != nil {
			out = append(out, replica)
		}
	}
	return out
}

func (s *P2PSync) replicaForNSID(ctx context.Context, nsID string) (*p2pReplica, bool) {
	replicas := s.registeredReplicas()
	for _, replica := range replicas {
		resolvedNSID, _, err := replica.state(ctx)
		if err != nil {
			continue
		}
		if resolvedNSID == nsID {
			return replica, true
		}
	}
	if len(replicas) == 1 {
		return replicas[0], false
	}
	return nil, false
}

func (s *P2PSync) writeErrorResponse(stream network.Stream, message, expectedNSID, observedNSID string) {
	resp := fsSyncMsg{
		Type:         "error",
		Error:        message,
		NSID:         expectedNSID,
		ExpectedNSID: expectedNSID,
		ObservedNSID: observedNSID,
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		s.logger.Warn("fs p2p: marshal error response failed", "error", err)
		return
	}
	if err := writeFSSyncMsg(stream, payload); err != nil {
		s.logger.Warn("fs p2p: write error response failed", "error", err)
	}
}

func summarizeFSSnapshot(snap *opslog.Snapshot) (fsSnapshotState, error) {
	type summaryFile struct {
		Path         string   `json:"path"`
		Chunks       []string `json:"chunks,omitempty"`
		Size         int64    `json:"size"`
		Modified     int64    `json:"modified"`
		Checksum     string   `json:"checksum"`
		PrevChecksum string   `json:"prev_checksum,omitempty"`
		Namespace    string   `json:"namespace"`
		Device       string   `json:"device,omitempty"`
		Seq          int      `json:"seq,omitempty"`
		LinkTarget   string   `json:"link_target,omitempty"`
	}
	type summaryDir struct {
		Path      string `json:"path"`
		Namespace string `json:"namespace,omitempty"`
		Device    string `json:"device,omitempty"`
		Seq       int    `json:"seq,omitempty"`
		Modified  int64  `json:"modified"`
	}
	type summaryTomb struct {
		Path         string `json:"path"`
		Namespace    string `json:"namespace,omitempty"`
		Device       string `json:"device,omitempty"`
		Seq          int    `json:"seq,omitempty"`
		Modified     int64  `json:"modified"`
		PrevChecksum string `json:"prev_checksum,omitempty"`
	}
	type canonicalSnapshot struct {
		Files         []summaryFile `json:"files"`
		Dirs          []summaryDir  `json:"dirs"`
		Tombstones    []summaryTomb `json:"tombstones"`
		DirTombstones []summaryTomb `json:"dir_tombstones"`
		RootTombstone *summaryTomb  `json:"root_tombstone,omitempty"`
	}

	filesMap := snap.Files()
	filePaths := make([]string, 0, len(filesMap))
	for path := range filesMap {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)

	dirsMap := snap.Dirs()
	dirPaths := make([]string, 0, len(dirsMap))
	for path := range dirsMap {
		dirPaths = append(dirPaths, path)
	}
	sort.Strings(dirPaths)

	tombsMap := snap.Tombstones()
	tombPaths := make([]string, 0, len(tombsMap))
	for path := range tombsMap {
		tombPaths = append(tombPaths, path)
	}
	sort.Strings(tombPaths)

	dirTombsMap := snap.DirTombstones()
	dirTombPaths := make([]string, 0, len(dirTombsMap))
	for path := range dirTombsMap {
		dirTombPaths = append(dirTombPaths, path)
	}
	sort.Strings(dirTombPaths)

	canonical := canonicalSnapshot{
		Files:         make([]summaryFile, 0, len(filePaths)),
		Dirs:          make([]summaryDir, 0, len(dirPaths)),
		Tombstones:    make([]summaryTomb, 0, len(tombPaths)),
		DirTombstones: make([]summaryTomb, 0, len(dirTombPaths)),
	}
	for _, path := range filePaths {
		fi := filesMap[path]
		canonical.Files = append(canonical.Files, summaryFile{
			Path:         path,
			Chunks:       append([]string(nil), fi.Chunks...),
			Size:         fi.Size,
			Modified:     fi.Modified.Unix(),
			Checksum:     fi.Checksum,
			PrevChecksum: fi.PrevChecksum,
			Namespace:    fi.Namespace,
			Device:       fi.Device,
			Seq:          fi.Seq,
			LinkTarget:   fi.LinkTarget,
		})
	}
	for _, path := range dirPaths {
		di := dirsMap[path]
		canonical.Dirs = append(canonical.Dirs, summaryDir{
			Path:      path,
			Namespace: di.Namespace,
			Device:    di.Device,
			Seq:       di.Seq,
			Modified:  di.Modified.Unix(),
		})
	}
	for _, path := range tombPaths {
		tomb := tombsMap[path]
		canonical.Tombstones = append(canonical.Tombstones, summaryTomb{
			Path:         path,
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Seq:          tomb.Seq,
			Modified:     tomb.Modified.Unix(),
			PrevChecksum: tomb.PrevChecksum,
		})
	}
	for _, path := range dirTombPaths {
		tomb := dirTombsMap[path]
		canonical.DirTombstones = append(canonical.DirTombstones, summaryTomb{
			Path:         path,
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Seq:          tomb.Seq,
			Modified:     tomb.Modified.Unix(),
			PrevChecksum: tomb.PrevChecksum,
		})
	}
	if tomb, ok := snap.RootTombstone(); ok {
		canonical.RootTombstone = &summaryTomb{
			Path:         "",
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Seq:          tomb.Seq,
			Modified:     tomb.Modified.Unix(),
			PrevChecksum: tomb.PrevChecksum,
		}
	}

	data, err := json.Marshal(canonical)
	if err != nil {
		return fsSnapshotState{}, fmt.Errorf("marshal fs snapshot summary: %w", err)
	}
	sum := sha256.Sum256(data)
	return fsSnapshotState{
		Digest:        fmt.Sprintf("%x", sum[:]),
		Files:         len(filePaths),
		Dirs:          len(dirPaths),
		Tombstones:    len(tombPaths),
		DirTombstones: len(dirTombPaths),
		UpdatedUnix:   snap.Updated().Unix(),
	}, nil
}

func encodeFSSnapshotData(nsKey []byte, snap *opslog.Snapshot) (json.RawMessage, error) {
	data, err := opslog.MarshalPeerSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal peer snapshot: %w", err)
	}
	encrypted, err := Encrypt(data, nsKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt peer snapshot: %w", err)
	}
	encJSON, err := json.Marshal(encrypted)
	if err != nil {
		return nil, fmt.Errorf("marshal encrypted peer snapshot: %w", err)
	}
	return encJSON, nil
}

func decodeFSSnapshotData(nsKey []byte, data json.RawMessage) (*opslog.Snapshot, error) {
	var encrypted []byte
	if err := json.Unmarshal(data, &encrypted); err != nil {
		return nil, fmt.Errorf("decode encrypted peer snapshot: %w", err)
	}
	plain, err := Decrypt(encrypted, nsKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt peer snapshot: %w", err)
	}
	snap, err := opslog.UnmarshalPeerSnapshot(plain)
	if err != nil {
		return nil, fmt.Errorf("unmarshal peer snapshot: %w", err)
	}
	return snap, nil
}

func mergePeerSnapshot(localLog *opslog.LocalOpsLog, remote *opslog.Snapshot) (int, error) {
	localSnap, err := localLog.Snapshot()
	if err != nil {
		return 0, err
	}

	localFiles := localSnap.Files()
	localDirs := localSnap.Dirs()
	localTombs := localSnap.Tombstones()
	localDirTombs := localSnap.DirTombstones()
	localRootTomb, _ := localSnap.RootTombstone()

	merged := 0

	if remoteRootTomb, ok := remote.RootTombstone(); ok {
		if shouldApplyRemoteRootTombstone(*remoteRootTomb, localRootTomb) {
			if err := localLog.Append(opslog.Entry{
				Type:         opslog.DeleteRoot,
				Path:         "",
				Namespace:    remoteRootTomb.Namespace,
				Device:       remoteRootTomb.Device,
				Timestamp:    remoteRootTomb.Modified.Unix(),
				Seq:          remoteRootTomb.Seq,
				PrevChecksum: remoteRootTomb.PrevChecksum,
			}); err != nil {
				return merged, err
			}
			merged++
			tomb := *remoteRootTomb
			localRootTomb = &tomb
		}
	}

	remoteDirTombs := remote.DirTombstones()
	dirTombPaths := make([]string, 0, len(remoteDirTombs))
	for path := range remoteDirTombs {
		dirTombPaths = append(dirTombPaths, path)
	}
	sort.Strings(dirTombPaths)
	for _, path := range dirTombPaths {
		tomb := remoteDirTombs[path]
		if !shouldApplyRemoteDirTombstone(path, tomb, localDirs, localDirTombs, localRootTomb) {
			continue
		}
		if err := localLog.Append(opslog.Entry{
			Type:      opslog.DeleteDir,
			Path:      path,
			Namespace: tomb.Namespace,
			Device:    tomb.Device,
			Timestamp: tomb.Modified.Unix(),
			Seq:       tomb.Seq,
		}); err != nil {
			return merged, err
		}
		merged++
	}

	remoteTombs := remote.Tombstones()
	tombPaths := make([]string, 0, len(remoteTombs))
	for path := range remoteTombs {
		tombPaths = append(tombPaths, path)
	}
	sort.Strings(tombPaths)
	for _, path := range tombPaths {
		tomb := remoteTombs[path]
		if !shouldApplyRemoteTombstone(path, tomb, localFiles, localTombs, localDirTombs, localRootTomb) {
			continue
		}
		if err := localLog.Append(opslog.Entry{
			Type:         opslog.Delete,
			Path:         path,
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Timestamp:    tomb.Modified.Unix(),
			Seq:          tomb.Seq,
			PrevChecksum: tomb.PrevChecksum,
		}); err != nil {
			return merged, err
		}
		merged++
	}

	remoteDirs := remote.Dirs()
	dirPaths := make([]string, 0, len(remoteDirs))
	for path := range remoteDirs {
		dirPaths = append(dirPaths, path)
	}
	sort.Strings(dirPaths)
	for _, path := range dirPaths {
		di := remoteDirs[path]
		if !shouldApplyRemoteDir(path, di, localDirs, localDirTombs, localRootTomb) {
			continue
		}
		if err := localLog.Append(opslog.Entry{
			Type:      opslog.CreateDir,
			Path:      path,
			Namespace: di.Namespace,
			Device:    di.Device,
			Timestamp: di.Modified.Unix(),
			Seq:       di.Seq,
		}); err != nil {
			return merged, err
		}
		merged++
	}

	remoteFiles := remote.Files()
	filePaths := make([]string, 0, len(remoteFiles))
	for path := range remoteFiles {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)
	for _, path := range filePaths {
		fi := remoteFiles[path]
		if !shouldApplyRemoteFile(path, fi, localFiles, localTombs, localDirTombs, localRootTomb) {
			continue
		}
		entryType := opslog.Put
		if fi.LinkTarget != "" {
			entryType = opslog.Symlink
		}
		if err := localLog.Append(opslog.Entry{
			Type:         entryType,
			Path:         path,
			Chunks:       append([]string(nil), fi.Chunks...),
			Size:         fi.Size,
			Checksum:     fi.Checksum,
			PrevChecksum: fi.PrevChecksum,
			Namespace:    fi.Namespace,
			LinkTarget:   fi.LinkTarget,
			Device:       fi.Device,
			Timestamp:    fi.Modified.Unix(),
			Seq:          fi.Seq,
		}); err != nil {
			return merged, err
		}
		merged++
	}

	return merged, nil
}

type lwwClock struct {
	ts     int64
	device string
	seq    int
}

func (c lwwClock) beats(other lwwClock) bool {
	if c.ts != other.ts {
		return c.ts > other.ts
	}
	if c.device != other.device {
		return c.device > other.device
	}
	return c.seq > other.seq
}

func clockFromFileInfo(fi opslog.FileInfo) lwwClock {
	return lwwClock{ts: fi.Modified.Unix(), device: fi.Device, seq: fi.Seq}
}

func clockFromDirInfo(di opslog.DirInfo) lwwClock {
	return lwwClock{ts: di.Modified.Unix(), device: di.Device, seq: di.Seq}
}

func clockFromTombstone(tomb opslog.TombstoneInfo) lwwClock {
	return lwwClock{ts: tomb.Modified.Unix(), device: tomb.Device, seq: tomb.Seq}
}

func fileDescendsFile(candidate, current opslog.FileInfo) bool {
	return candidate.PrevChecksum != "" && current.Checksum != "" && candidate.PrevChecksum == current.Checksum
}

func tombstoneDescendsFile(tomb opslog.TombstoneInfo, current opslog.FileInfo) bool {
	return tomb.PrevChecksum != "" && current.Checksum != "" && tomb.PrevChecksum == current.Checksum
}

func shouldApplyRemoteRootTombstone(remote opslog.TombstoneInfo, localRoot *opslog.TombstoneInfo) bool {
	if localRoot == nil {
		return true
	}
	return clockFromTombstone(remote).beats(clockFromTombstone(*localRoot))
}

func shouldApplyRemoteFile(path string, remote opslog.FileInfo, localFiles map[string]opslog.FileInfo, localTombs, localDirTombs map[string]opslog.TombstoneInfo, localRoot *opslog.TombstoneInfo) bool {
	remoteClock := clockFromFileInfo(remote)
	if localRoot != nil && !remoteClock.beats(clockFromTombstone(*localRoot)) {
		return false
	}
	if local, ok := localFiles[path]; ok {
		if fileDescendsFile(local, remote) {
			return false
		}
		if !fileDescendsFile(remote, local) && !remoteClock.beats(clockFromFileInfo(local)) {
			return false
		}
	}
	if local, ok := localTombs[path]; ok {
		if !remoteClock.beats(clockFromTombstone(local)) {
			return false
		}
	}
	if local, ok := coveringDirTombstone(path, localDirTombs); ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	return true
}

func shouldApplyRemoteTombstone(path string, remote opslog.TombstoneInfo, localFiles map[string]opslog.FileInfo, localTombs, localDirTombs map[string]opslog.TombstoneInfo, localRoot *opslog.TombstoneInfo) bool {
	remoteClock := clockFromTombstone(remote)
	if localRoot != nil && !remoteClock.beats(clockFromTombstone(*localRoot)) {
		return false
	}
	if local, ok := localFiles[path]; ok {
		if !tombstoneDescendsFile(remote, local) && !remoteClock.beats(clockFromFileInfo(local)) {
			return false
		}
	}
	if local, ok := localTombs[path]; ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	if local, ok := coveringDirTombstone(path, localDirTombs); ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	return true
}

func shouldApplyRemoteDir(path string, remote opslog.DirInfo, localDirs map[string]opslog.DirInfo, localDirTombs map[string]opslog.TombstoneInfo, localRoot *opslog.TombstoneInfo) bool {
	remoteClock := clockFromDirInfo(remote)
	if localRoot != nil && !remoteClock.beats(clockFromTombstone(*localRoot)) {
		return false
	}
	if local, ok := localDirs[path]; ok && !remoteClock.beats(clockFromDirInfo(local)) {
		return false
	}
	if local, ok := localDirTombs[path]; ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	if local, ok := coveringDirTombstone(path, localDirTombs); ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	return true
}

func shouldApplyRemoteDirTombstone(path string, remote opslog.TombstoneInfo, localDirs map[string]opslog.DirInfo, localDirTombs map[string]opslog.TombstoneInfo, localRoot *opslog.TombstoneInfo) bool {
	remoteClock := clockFromTombstone(remote)
	if localRoot != nil && !remoteClock.beats(clockFromTombstone(*localRoot)) {
		return false
	}
	if local, ok := localDirs[path]; ok && !remoteClock.beats(clockFromDirInfo(local)) {
		return false
	}
	if local, ok := localDirTombs[path]; ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	if local, ok := coveringDirTombstone(path, localDirTombs); ok && !remoteClock.beats(clockFromTombstone(local)) {
		return false
	}
	return true
}

func coveringDirTombstone(path string, tombs map[string]opslog.TombstoneInfo) (opslog.TombstoneInfo, bool) {
	dir := path
	for {
		i := bytes.LastIndexByte([]byte(dir), '/')
		if i < 0 {
			return opslog.TombstoneInfo{}, false
		}
		dir = dir[:i]
		if tomb, ok := tombs[dir]; ok {
			return tomb, true
		}
	}
}

func boundedFSSyncContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= fsSyncExchangeTimeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, fsSyncExchangeTimeout)
}

func writeFSSyncMsg(w io.Writer, data []byte) error {
	length := uint32(len(data))
	header := []byte{byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := io.Copy(w, bytes.NewReader(data))
	return err
}

func readFSSyncMsg(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	if length > 4*1024*1024 {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
