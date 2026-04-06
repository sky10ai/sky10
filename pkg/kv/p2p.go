package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	skykey "github.com/sky10/sky10/pkg/key"
)

// KVSyncProtocol is the libp2p protocol ID for KV snapshot sync.
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
	Type string          `json:"type"`          // "snapshot" or "key_offer"
	NSID string          `json:"nsid"`          // namespace ID
	Data json.RawMessage `json:"data"`          // encrypted snapshot or wrapped key
	Seq  int             `json:"seq,omitempty"` // sender's max local seq
}

// P2PSync handles KV snapshot exchange over libp2p streams.
type P2PSync struct {
	store    *Store
	node     p2pNode
	identity *skykey.Key
	logger   *slog.Logger

	mu       sync.Mutex
	lastPush map[peer.ID]time.Time // rate-limit pushes per peer
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
		lastPush: make(map[peer.ID]time.Time),
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

	// Push current snapshot to all connected peers.
	go s.PushToAll(context.Background())
}

// PushToAll sends the current snapshot to all connected peers.
func (s *P2PSync) PushToAll(ctx context.Context) {
	peers := s.node.ConnectedPrivateNetworkPeers()
	s.logger.Info("kv p2p push: broadcasting", "peers", len(peers))
	for _, pid := range peers {
		if pid == s.node.PeerID() {
			continue
		}
		go s.pushToPeer(ctx, pid)
	}
}

// pushToPeer sends the current encrypted snapshot to a single peer.
func (s *P2PSync) pushToPeer(ctx context.Context, pid peer.ID) {
	snap, err := s.store.localLog.Snapshot()
	if err != nil || snap.Len() == 0 {
		s.logger.Warn("kv p2p push: no snapshot", "peer", pid, "err", err)
		return
	}

	nsKey := s.store.nsKey
	nsID := s.store.nsID
	if nsKey == nil || nsID == "" {
		s.logger.Warn("kv p2p push: namespace not resolved", "peer", pid)
		return
	}

	data, err := MarshalSnapshot(snap)
	if err != nil {
		s.logger.Warn("kv p2p push: marshal failed", "peer", pid, "error", err)
		return
	}
	encrypted, err := encrypt(data, nsKey)
	if err != nil {
		s.logger.Warn("kv p2p push: encrypt failed", "peer", pid, "error", err)
		return
	}

	encJSON, _ := json.Marshal(encrypted)
	msg := p2pSyncMsg{
		Type: "snapshot",
		NSID: nsID,
		Data: encJSON,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h := s.node.Host()
	if h == nil {
		return
	}

	stream, err := h.NewStream(ctx, pid, KVSyncProtocol)
	if err != nil {
		s.logger.Warn("kv p2p push: open stream failed", "peer", pid, "error", err)
		return
	}
	defer stream.Close()

	if err := writeMsg(stream, payload); err != nil {
		s.logger.Warn("kv p2p push: write failed", "peer", pid, "error", err)
		return
	}
	s.logger.Info("kv p2p push: sent snapshot", "peer", pid, "keys", snap.Len())
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
	case "snapshot":
		s.handleSnapshot(msg, stream.Conn().RemotePeer())
	default:
		s.logger.Warn("kv p2p: unknown message type", "type", msg.Type)
	}
}

// handleSnapshot merges a received encrypted snapshot.
func (s *P2PSync) handleSnapshot(msg p2pSyncMsg, from peer.ID) {
	nsKey := s.store.nsKey
	nsID := s.store.nsID
	if nsKey == nil || nsID == "" {
		s.logger.Warn("kv p2p: namespace not resolved, ignoring snapshot")
		return
	}
	if msg.NSID != nsID {
		s.logger.Warn("kv p2p: namespace mismatch", "got", msg.NSID, "want", nsID)
		return
	}

	// Unquote the base64/hex encoded encrypted data.
	var encrypted []byte
	if err := json.Unmarshal(msg.Data, &encrypted); err != nil {
		s.logger.Warn("kv p2p: decode encrypted data failed", "error", err)
		return
	}

	plain, err := decrypt(encrypted, nsKey)
	if err != nil {
		s.logger.Warn("kv p2p: decrypt failed", "error", err)
		return
	}

	remote, err := UnmarshalSnapshot(plain)
	if err != nil {
		s.logger.Warn("kv p2p: unmarshal snapshot failed", "error", err)
		return
	}

	// Diff and merge using the same logic as the S3 poller.
	peerID := from.String()
	if len(peerID) > 16 {
		peerID = peerID[:16]
	}

	baseline, _ := s.store.baselines.Load(peerID)
	merged := diffAndMerge(s.store.localLog, remote, baseline, s.logger)

	if err := s.store.baselines.Save(peerID, remote); err != nil {
		s.logger.Warn("kv p2p: save baseline failed", "error", err)
	}

	if merged > 0 {
		s.logger.Info("kv p2p: merged remote changes", "peer", from, "entries", merged)
		// Trigger upload to S3 if configured.
		if s.store.uploader != nil {
			s.store.uploader.Poke()
		}
	}
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
