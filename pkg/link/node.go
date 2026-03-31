package link

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Mode controls the node's network participation level.
type Mode int

const (
	// Private connects only to own devices (same S3 bucket). No public DHT,
	// no IPNS, no relay service, no Nostr. Default.
	Private Mode = iota

	// Network joins the public DHT, publishes IPNS records, accepts
	// authorized external peers, and acts as a circuit relay.
	Network
)

// Config holds Node configuration.
type Config struct {
	Mode        Mode     // Private (default) or Network
	ListenAddrs []string // default: ["/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"]
}

func (c Config) listenAddrs() []string {
	if len(c.ListenAddrs) > 0 {
		return c.ListenAddrs
	}
	return []string{
		"/ip4/0.0.0.0/tcp/0",
		"/ip4/0.0.0.0/udp/0/quic-v1",
	}
}

// Node is the skylink P2P communication node. It wraps a libp2p host
// and manages connections to other sky10 agents.
type Node struct {
	identity *skykey.Key
	config   Config
	logger   *slog.Logger

	host     host.Host
	peerID   peer.ID
	registry *Registry
	pubsub   *PubSub
	channels *ChannelManager

	mu      sync.RWMutex
	running bool

	// syncNotifyHandler is called when an own device sends a sync notification.
	syncNotifyMu      sync.RWMutex
	syncNotifyHandler func(from peer.ID, topic string)
}

// New creates a Node but does not start it. Call Run to start the libp2p host.
func New(identity *skykey.Key, config Config, logger *slog.Logger) (*Node, error) {
	if logger == nil {
		logger = slog.Default()
	}
	pid, err := PeerIDFromKey(identity)
	if err != nil {
		return nil, fmt.Errorf("deriving peer ID: %w", err)
	}
	return &Node{
		identity: identity,
		config:   config,
		logger:   logger,
		peerID:   pid,
	}, nil
}

// PeerID returns this node's libp2p peer ID.
func (n *Node) PeerID() peer.ID { return n.peerID }

// Address returns this node's sky10q... address.
func (n *Node) Address() string { return n.identity.Address() }

// Host returns the underlying libp2p host. Nil before Run.
func (n *Node) Host() host.Host { return n.host }

// RegisterCapability registers a capability handler on this node.
func (n *Node) RegisterCapability(cap Capability, handler HandlerFunc) {
	if n.registry == nil {
		n.registry = NewRegistry(n.logger)
	}
	n.registry.Register(cap, handler)
}

// Capabilities returns all registered capabilities.
func (n *Node) Capabilities() []Capability {
	if n.registry == nil {
		return nil
	}
	return n.registry.Capabilities()
}

// ChannelManager returns the channel manager. Nil before Run.
func (n *Node) ChannelManager() *ChannelManager { return n.channels }

// ConnectedPeers returns the peer IDs of all connected peers.
func (n *Node) ConnectedPeers() []peer.ID {
	if n.host == nil {
		return nil
	}
	return n.host.Network().Peers()
}

// Run starts the libp2p host and blocks until ctx is cancelled.
func (n *Node) Run(ctx context.Context) error {
	privKey, err := Libp2pPrivKey(n.identity)
	if err != nil {
		return fmt.Errorf("converting identity: %w", err)
	}

	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(n.config.listenAddrs()...),
		libp2p.Security(noise.ID, noise.New),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	}

	if n.config.Mode == Network {
		opts = append(opts,
			libp2p.EnableRelayService(),
			libp2p.EnableAutoNATv2(),
		)
	} else {
		opts = append(opts, libp2p.DisableRelay())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("creating libp2p host: %w", err)
	}
	n.host = h

	// Initialize GossipSub for encrypted channels.
	ps, err := newPubSub(ctx, n)
	if err != nil {
		h.Close()
		return fmt.Errorf("initializing pubsub: %w", err)
	}
	n.pubsub = ps
	n.channels = newChannelManager(n, ps)

	// Register protocol handlers.
	h.SetStreamHandler(SyncNotifyProtocol, n.handleSyncNotify)
	if n.registry == nil {
		n.registry = NewRegistry(n.logger)
	}
	h.SetStreamHandler(ProtocolID, n.registry.HandleStream)

	n.mu.Lock()
	n.running = true
	n.mu.Unlock()

	n.logger.Info("skylink node started",
		"peer_id", n.peerID.String(),
		"mode", modeString(n.config.Mode),
		"addrs", h.Addrs(),
	)

	<-ctx.Done()

	n.mu.Lock()
	n.running = false
	n.mu.Unlock()

	if n.pubsub != nil {
		n.pubsub.Close()
	}
	if err := h.Close(); err != nil {
		return fmt.Errorf("closing host: %w", err)
	}
	n.logger.Info("skylink node stopped")
	return nil
}

// Close shuts down the libp2p host.
func (n *Node) Close() error {
	if n.host == nil {
		return nil
	}
	return n.host.Close()
}

// OnSyncNotify registers a handler for incoming sync notifications from
// own devices. Called when a connected device sends a "poll now" nudge
// via direct stream.
func (n *Node) OnSyncNotify(handler func(from peer.ID, topic string)) {
	n.syncNotifyMu.Lock()
	n.syncNotifyHandler = handler
	n.syncNotifyMu.Unlock()
}

// handleSyncNotify is the libp2p stream handler for sync notification pokes.
func (n *Node) handleSyncNotify(s network.Stream) {
	defer s.Close()

	buf := make([]byte, 512)
	nr, err := s.Read(buf)
	if err != nil && nr == 0 {
		return
	}
	topic := string(buf[:nr])

	n.syncNotifyMu.RLock()
	handler := n.syncNotifyHandler
	n.syncNotifyMu.RUnlock()

	if handler != nil {
		handler(s.Conn().RemotePeer(), topic)
	}
}

func modeString(m Mode) string {
	if m == Network {
		return "network"
	}
	return "private"
}
