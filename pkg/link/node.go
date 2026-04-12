package link

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/sky10/sky10/pkg/id"
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
	Mode                     Mode            // Private (default) or Network
	ListenAddrs              []string        // default: ["/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"]
	BootstrapPeers           []peer.AddrInfo // nil => libp2p defaults, empty => no default bootstrap peers
	RelayPeers               []peer.AddrInfo // static relay peers for live autorelay fallback
	ForcePrivateReachability bool            // primarily useful in tests to force autorelay reservation
	ForcePublicReachability  bool            // primarily useful in tests to force relay-service viability
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
	bundle *id.Bundle
	config Config
	logger *slog.Logger

	host     host.Host
	peerID   peer.ID
	version  string
	gater    *Gater
	dht      *dht.IpfsDHT
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
// The bundle's Device key is used for libp2p transport (peer ID), while the
// Identity key provides the external sky10q... address.
func New(bundle *id.Bundle, config Config, logger *slog.Logger) (*Node, error) {
	logger = componentLogger(logger)
	pid, err := PeerIDFromKey(bundle.Device)
	if err != nil {
		return nil, fmt.Errorf("deriving peer ID: %w", err)
	}
	return &Node{
		bundle: bundle,
		config: config,
		logger: logger,
		peerID: pid,
	}, nil
}

// NewFromKey creates a Node from a single key (both identity and device).
// This is a convenience for tests that don't need identity separation.
func NewFromKey(k *skykey.Key, config Config, logger *slog.Logger) (*Node, error) {
	manifest := id.NewManifest(k)
	manifest.AddDevice(k.PublicKey, "test")
	if err := manifest.Sign(k.PrivateKey); err != nil {
		return nil, err
	}
	bundle, err := id.New(k, k, manifest)
	if err != nil {
		return nil, err
	}
	return New(bundle, config, logger)
}

// PeerID returns this node's libp2p peer ID.
func (n *Node) PeerID() peer.ID { return n.peerID }

// Address returns this node's identity sky10q... address.
func (n *Node) Address() string { return n.bundle.Address() }

// Bundle returns the identity bundle.
func (n *Node) Bundle() *id.Bundle { return n.bundle }

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

// SetVersion sets the version string for agent records.
func (n *Node) SetVersion(v string) { n.version = v }

// Gater returns the connection gater.
func (n *Node) Gater() *Gater { return n.gater }

// ChannelManager returns the channel manager. Nil before Run.
func (n *Node) ChannelManager() *ChannelManager { return n.channels }

// ConnectedPeers returns the peer IDs of all connected peers.
func (n *Node) ConnectedPeers() []peer.ID {
	if n.host == nil {
		return nil
	}
	return n.host.Network().Peers()
}

// ConnectedPrivateNetworkPeers returns only the connected peers that belong to
// the current private-network membership manifest.
func (n *Node) ConnectedPrivateNetworkPeers() []peer.ID {
	if n.host == nil || n.bundle == nil || n.bundle.Manifest == nil {
		return nil
	}

	allowed := make(map[peer.ID]struct{}, len(n.bundle.Manifest.Devices))
	for _, device := range n.bundle.Manifest.Devices {
		pid, err := PeerIDFromPubKey(device.PublicKey)
		if err != nil || pid == n.peerID {
			continue
		}
		allowed[pid] = struct{}{}
	}

	connected := n.host.Network().Peers()
	out := make([]peer.ID, 0, len(connected))
	for _, pid := range connected {
		if _, ok := allowed[pid]; ok {
			out = append(out, pid)
		}
	}
	return out
}

// Run starts the libp2p host and blocks until ctx is cancelled.
func (n *Node) Run(ctx context.Context) error {
	privKey, err := Libp2pPrivKey(n.bundle.Device)
	if err != nil {
		return fmt.Errorf("converting device key: %w", err)
	}

	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(n.config.listenAddrs()...),
		libp2p.Security(noise.ID, noise.New),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	}

	if n.gater != nil {
		opts = append(opts, libp2p.ConnectionGater(n.gater))
	}

	if n.config.Mode == Network {
		if n.config.ForcePublicReachability {
			opts = append(opts, libp2p.ForceReachabilityPublic())
		} else if n.config.ForcePrivateReachability {
			opts = append(opts, libp2p.ForceReachabilityPrivate())
		}
		opts = append(opts,
			libp2p.EnableRelay(),
			libp2p.EnableRelayService(),
			libp2p.EnableAutoNATv2(),
		)
		if len(n.config.RelayPeers) > 0 {
			opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(n.config.RelayPeers))
		}
	} else {
		opts = append(opts, libp2p.DisableRelay())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("creating libp2p host: %w", err)
	}
	n.host = h

	// Initialize DHT in network mode.
	if n.config.Mode == Network {
		if err := n.initDHT(ctx); err != nil {
			h.Close()
			n.host = nil
			n.dht = nil
			return err
		}
	}

	// Initialize GossipSub for encrypted channels.
	ps, err := newPubSub(ctx, n)
	if err != nil {
		if n.dht != nil {
			n.dht.Close()
			n.dht = nil
		}
		h.Close()
		n.host = nil
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

	if n.dht != nil {
		n.dht.Close()
	}
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
