package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	skykey "github.com/sky10/sky10/pkg/key"
)

// KVSyncProtocol is the libp2p protocol ID for KV anti-entropy sync.
const KVSyncProtocol = protocol.ID("/sky10/kv-sync/1.0.0")

// p2pNode is the subset of link.Node that P2PSync needs. Avoids import
// cycle between kv and link.
type p2pNode interface {
	Host() host.Host
	PeerID() peer.ID
	ConnectedPrivateNetworkPeers() []peer.ID
}

// p2pSyncMsg is the wire format for KV sync messages.
type p2pSyncMsg struct {
	Type    string           `json:"type"`              // "summary", "delta", or legacy "snapshot"
	NSID    string           `json:"nsid"`              // namespace ID
	Summary *SnapshotSummary `json:"summary,omitempty"` // causal summary
	Data    json.RawMessage  `json:"data,omitempty"`    // encrypted snapshot/delta
}

// P2PSync handles KV snapshot exchange over libp2p streams.
type P2PSync struct {
	store    *Store
	node     p2pNode
	identity *skykey.Key
	logger   *slog.Logger
}

// NewP2PSync creates a P2P sync handler for the given KV store.
func NewP2PSync(store *Store, node p2pNode, identity *skykey.Key, logger *slog.Logger) *P2PSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &P2PSync{
		store:    store,
		node:     node,
		identity: identity,
		logger:   logger,
	}
}

// RegisterProtocol registers the KV sync stream handler on the libp2p host.
// Must be called after the host is ready.
func (s *P2PSync) RegisterProtocol() {
	h := s.node.Host()
	if h == nil {
		return
	}
	h.SetStreamHandler(KVSyncProtocol, s.handleStream)
	s.logger.Info("kv p2p sync protocol registered")

	// Trigger an initial anti-entropy round with all connected peers.
	go s.PushToAll(context.Background())
}

// PushToAll runs a summary-first anti-entropy round with all connected peers.
func (s *P2PSync) PushToAll(ctx context.Context) {
	peers := s.node.ConnectedPrivateNetworkPeers()
	s.logger.Info("kv p2p push: broadcasting", "peers", len(peers))
	for _, pid := range peers {
		if pid == s.node.PeerID() {
			continue
		}
		go s.syncWithPeer(ctx, pid)
	}
}

// syncWithPeer exchanges summaries first, then sends only the state the peer
// is missing. This keeps reconnect healing correct without always blasting a
// full snapshot.
func (s *P2PSync) syncWithPeer(ctx context.Context, pid peer.ID) {
	snap, err := s.store.localLog.Snapshot()
	if err != nil {
		s.logger.Warn("kv p2p push: snapshot failed", "peer", pid, "err", err)
		return
	}

	nsID := s.store.nsID
	if nsID == "" {
		s.logger.Warn("kv p2p push: namespace not resolved", "peer", pid)
		return
	}

	response, err := s.requestSummary(ctx, pid, snap.Summary())
	if err != nil {
		s.logger.Warn("kv p2p push: summary exchange failed", "peer", pid, "error", err)
		return
	}

	if response != nil {
		if merged, err := s.mergeSnapshotMessage(*response, pid, false); err != nil {
			s.logger.Warn("kv p2p push: merge response failed", "peer", pid, "error", err)
		} else if merged > 0 {
			s.logger.Info("kv p2p push: merged peer delta", "peer", pid, "entries", merged)
		}
	}

	if response == nil || response.Summary == nil {
		return
	}

	latest, err := s.store.localLog.Snapshot()
	if err != nil {
		s.logger.Warn("kv p2p push: snapshot refresh failed", "peer", pid, "error", err)
		return
	}
	delta := latest.DeltaSince(response.Summary.Vector)
	if !delta.HasState() {
		return
	}
	if err := s.sendSnapshotMessage(ctx, pid, "delta", delta); err != nil {
		s.logger.Warn("kv p2p push: send delta failed", "peer", pid, "error", err)
		return
	}
	s.logger.Info("kv p2p push: sent delta", "peer", pid, "keys", delta.Len(), "tombstones", len(delta.Tombstones()))
}

func (s *P2PSync) requestSummary(ctx context.Context, pid peer.ID, summary SnapshotSummary) (*p2pSyncMsg, error) {
	h := s.node.Host()
	if h == nil {
		return nil, fmt.Errorf("host not running")
	}

	stream, err := h.NewStream(ctx, pid, KVSyncProtocol)
	if err != nil {
		return nil, fmt.Errorf("opening stream: %w", err)
	}
	defer stream.Close()

	if deadline, ok := ctx.Deadline(); ok {
		stream.SetDeadline(deadline)
	}

	msg := p2pSyncMsg{
		Type:    "summary",
		NSID:    s.store.nsID,
		Summary: &summary,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := writeMsg(stream, payload); err != nil {
		return nil, fmt.Errorf("writing summary: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return nil, fmt.Errorf("closing write: %w", err)
	}

	responsePayload, err := readMsg(stream)
	if err != nil {
		return nil, fmt.Errorf("reading summary response: %w", err)
	}

	var response p2pSyncMsg
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return nil, fmt.Errorf("unmarshal summary response: %w", err)
	}
	return &response, nil
}

func (s *P2PSync) sendSnapshotMessage(ctx context.Context, pid peer.ID, kind string, snap *Snapshot) error {
	h := s.node.Host()
	if h == nil {
		return fmt.Errorf("host not running")
	}

	nsID := s.store.nsID
	if nsID == "" {
		return fmt.Errorf("namespace not resolved")
	}

	encJSON, err := s.encodeSnapshotData(snap)
	if err != nil {
		return err
	}

	msg := p2pSyncMsg{
		Type:    kind,
		NSID:    nsID,
		Summary: summaryPtr(snap.Summary()),
		Data:    encJSON,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	stream, err := h.NewStream(ctx, pid, KVSyncProtocol)
	if err != nil {
		return fmt.Errorf("opening stream: %w", err)
	}
	defer stream.Close()

	if deadline, ok := ctx.Deadline(); ok {
		stream.SetDeadline(deadline)
	}

	if err := writeMsg(stream, payload); err != nil {
		return fmt.Errorf("writing %s: %w", kind, err)
	}
	return stream.CloseWrite()
}

// handleStream processes an incoming KV sync stream.
func (s *P2PSync) handleStream(stream network.Stream) {
	defer stream.Close()

	payload, err := readMsg(stream)
	if err != nil {
		s.logger.Warn("kv p2p: read failed", "error", err)
		return
	}

	var msg p2pSyncMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		s.logger.Warn("kv p2p: unmarshal failed", "error", err)
		return
	}

	switch msg.Type {
	case "summary":
		s.handleSummary(stream, msg, stream.Conn().RemotePeer())
	case "delta":
		if _, err := s.mergeSnapshotMessage(msg, stream.Conn().RemotePeer(), false); err != nil {
			s.logger.Warn("kv p2p: merge delta failed", "error", err)
		}
	case "snapshot":
		if _, err := s.mergeSnapshotMessage(msg, stream.Conn().RemotePeer(), true); err != nil {
			s.logger.Warn("kv p2p: merge legacy snapshot failed", "error", err)
		}
	default:
		s.logger.Warn("kv p2p: unknown message type", "type", msg.Type)
	}
}

func (s *P2PSync) handleSummary(stream network.Stream, msg p2pSyncMsg, from peer.ID) {
	nsID := s.store.nsID
	if nsID == "" {
		s.logger.Warn("kv p2p: namespace not resolved, ignoring summary")
		return
	}
	if msg.NSID != nsID {
		s.logger.Warn("kv p2p: namespace mismatch", "got", msg.NSID, "want", nsID)
		return
	}

	snap, err := s.store.localLog.Snapshot()
	if err != nil {
		s.logger.Warn("kv p2p: local snapshot failed", "error", err)
		return
	}

	response := p2pSyncMsg{
		Type:    "summary",
		NSID:    nsID,
		Summary: summaryPtr(snap.Summary()),
	}
	if msg.Summary != nil {
		delta := snap.DeltaSince(msg.Summary.Vector)
		if delta.HasState() {
			encJSON, err := s.encodeSnapshotData(delta)
			if err != nil {
				s.logger.Warn("kv p2p: encode delta failed", "error", err)
				return
			}
			response.Data = encJSON
		}
	}

	payload, err := json.Marshal(response)
	if err != nil {
		s.logger.Warn("kv p2p: marshal summary response failed", "error", err)
		return
	}
	if err := writeMsg(stream, payload); err != nil {
		s.logger.Warn("kv p2p: write summary response failed", "error", err)
	}
}

func (s *P2PSync) mergeSnapshotMessage(msg p2pSyncMsg, from peer.ID, useBaseline bool) (int, error) {
	nsKey := s.store.nsKey
	nsID := s.store.nsID
	if nsKey == nil || nsID == "" {
		return 0, fmt.Errorf("namespace not resolved")
	}
	if msg.NSID != nsID {
		return 0, fmt.Errorf("namespace mismatch: got %s want %s", msg.NSID, nsID)
	}
	if len(msg.Data) == 0 {
		return 0, nil
	}

	remote, err := s.decodeSnapshotData(msg.Data)
	if err != nil {
		return 0, err
	}

	peerID := from.String()
	if len(peerID) > 16 {
		peerID = peerID[:16]
	}

	var baseline *Snapshot
	if useBaseline {
		baseline, _ = s.store.baselines.Load(peerID)
	}
	merged := diffAndMerge(s.store.localLog, remote, baseline, s.logger)

	if useBaseline {
		if err := s.store.baselines.Save(peerID, remote); err != nil {
			s.logger.Warn("kv p2p: save baseline failed", "error", err)
		}
	}

	if merged > 0 {
		if s.store.uploader != nil {
			s.store.uploader.Poke()
		}
	}
	return merged, nil
}

func (s *P2PSync) encodeSnapshotData(snap *Snapshot) (json.RawMessage, error) {
	nsKey := s.store.nsKey
	if nsKey == nil {
		return nil, fmt.Errorf("namespace key not resolved")
	}
	data, err := MarshalSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	encrypted, err := encrypt(data, nsKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt snapshot: %w", err)
	}
	encJSON, err := json.Marshal(encrypted)
	if err != nil {
		return nil, fmt.Errorf("marshal encrypted snapshot: %w", err)
	}
	return encJSON, nil
}

func (s *P2PSync) decodeSnapshotData(data json.RawMessage) (*Snapshot, error) {
	nsKey := s.store.nsKey
	if nsKey == nil {
		return nil, fmt.Errorf("namespace key not resolved")
	}
	var encrypted []byte
	if err := json.Unmarshal(data, &encrypted); err != nil {
		return nil, fmt.Errorf("decode encrypted data: %w", err)
	}
	plain, err := decrypt(encrypted, nsKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt snapshot: %w", err)
	}
	remote, err := UnmarshalSnapshot(plain)
	if err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return remote, nil
}

func summaryPtr(summary SnapshotSummary) *SnapshotSummary {
	return &summary
}

// writeMsg writes a length-prefixed message to a stream.
func writeMsg(w io.Writer, data []byte) error {
	// Simple length prefix: 4 bytes big-endian.
	length := uint32(len(data))
	header := []byte{byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := io.Copy(w, bytes.NewReader(data))
	return err
}

// readMsg reads a length-prefixed message from a stream.
func readMsg(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	if length > 4*1024*1024 { // 4MB max
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}
