package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/sky10/sky10/pkg/adapter"
)

// Resolver finds peer addresses through multiple discovery layers.
type Resolver struct {
	node    *Node
	backend adapter.Backend // optional: same-bucket discovery
	logger  *slog.Logger
}

// ResolverOption configures the resolver.
type ResolverOption func(*Resolver)

// NewResolver creates a layered resolver.
func NewResolver(node *Node, opts ...ResolverOption) *Resolver {
	r := &Resolver{
		node:   node,
		logger: node.logger,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// WithBackend enables same-bucket discovery via S3 device registry.
func WithBackend(b adapter.Backend) ResolverOption {
	return func(r *Resolver) { r.backend = b }
}

// Resolve finds a peer's addresses. Tries layers in order:
// 1. Already connected (peerstore)
// 2. Same bucket (S3 devices/ registry) — own devices
// 3. DHT — any agent (network mode only)
func (r *Resolver) Resolve(ctx context.Context, address string) (*peer.AddrInfo, error) {
	pid, err := PeerIDFromAddress(address)
	if err != nil {
		return nil, err
	}

	// Layer 0: Already connected — check peerstore.
	if r.node.host != nil {
		addrs := r.node.host.Peerstore().Addrs(pid)
		if len(addrs) > 0 {
			return &peer.AddrInfo{ID: pid, Addrs: addrs}, nil
		}
	}

	// Layer 1: Same bucket — S3 device registry.
	if r.backend != nil {
		info, err := r.resolveFromBucket(ctx, pid, address)
		if err == nil && info != nil {
			return info, nil
		}
		r.logger.Debug("same-bucket resolve failed", "address", address, "error", err)
	}

	// Layer 2: DHT (network mode only).
	if r.node.dht != nil {
		info, err := r.node.dht.FindPeer(ctx, pid)
		if err == nil {
			return &info, nil
		}
		r.logger.Debug("DHT resolve failed", "address", address, "error", err)
	}

	return nil, fmt.Errorf("could not resolve %s", address)
}

// Connect resolves a peer's addresses and establishes a connection.
func (r *Resolver) Connect(ctx context.Context, address string) error {
	info, err := r.Resolve(ctx, address)
	if err != nil {
		return err
	}
	return r.node.host.Connect(ctx, *info)
}

// resolveFromBucket looks up a peer in the S3 device registry.
func (r *Resolver) resolveFromBucket(ctx context.Context, target peer.ID, address string) (*peer.AddrInfo, error) {
	keys, err := r.backend.List(ctx, "devices/")
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		rc, err := r.backend.Get(ctx, key)
		if err != nil {
			continue
		}

		var dev struct {
			PubKey string `json:"PubKey"`
			IP     string `json:"IP"`
		}
		if err := json.NewDecoder(rc).Decode(&dev); err != nil {
			rc.Close()
			continue
		}
		rc.Close()

		if dev.PubKey != address || dev.IP == "" {
			continue
		}

		// Try common libp2p ports — we don't know the exact port.
		// The peerstore will handle connection attempts.
		addrs := make([]ma.Multiaddr, 0, 2)
		if a, err := ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/0", dev.IP)); err == nil {
			addrs = append(addrs, a)
		}
		if len(addrs) > 0 {
			return &peer.AddrInfo{ID: target, Addrs: addrs}, nil
		}
	}

	return nil, fmt.Errorf("device not found in bucket")
}
