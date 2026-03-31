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
			PubKey     string   `json:"pubkey"`
			Multiaddrs []string `json:"multiaddrs,omitempty"`
		}
		if err := json.NewDecoder(rc).Decode(&dev); err != nil {
			rc.Close()
			continue
		}
		rc.Close()

		if dev.PubKey != address {
			continue
		}

		// Use published multiaddrs (includes peer ID).
		if len(dev.Multiaddrs) > 0 {
			addrs := make([]ma.Multiaddr, 0, len(dev.Multiaddrs))
			for _, s := range dev.Multiaddrs {
				if a, err := ma.NewMultiaddr(s); err == nil {
					addrs = append(addrs, a)
				}
			}
			if len(addrs) > 0 {
				info, err := peer.AddrInfoFromP2pAddr(addrs[0])
				if err != nil {
					// Fall back to constructing manually.
					return &peer.AddrInfo{ID: target, Addrs: addrs}, nil
				}
				// Collect all transport addrs.
				for _, a := range addrs[1:] {
					if ai, err := peer.AddrInfoFromP2pAddr(a); err == nil {
						info.Addrs = append(info.Addrs, ai.Addrs...)
					}
				}
				return info, nil
			}
		}
	}

	return nil, fmt.Errorf("device not found in bucket")
}

// AutoConnect discovers own devices from the S3 registry and connects
// to them via their published multiaddrs.
func AutoConnect(ctx context.Context, node *Node, backend adapter.Backend, selfAddr string) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		node.logger.Warn("auto-connect: failed to list devices", "error", err)
		return
	}

	for _, key := range keys {
		rc, err := backend.Get(ctx, key)
		if err != nil {
			continue
		}

		var dev struct {
			PubKey     string   `json:"pubkey"`
			Name       string   `json:"name"`
			Multiaddrs []string `json:"multiaddrs,omitempty"`
		}
		if err := json.NewDecoder(rc).Decode(&dev); err != nil {
			rc.Close()
			continue
		}
		rc.Close()

		// Skip self.
		if dev.PubKey == selfAddr || len(dev.Multiaddrs) == 0 {
			continue
		}

		// Parse multiaddrs and connect.
		for _, s := range dev.Multiaddrs {
			maddr, err := ma.NewMultiaddr(s)
			if err != nil {
				continue
			}
			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				continue
			}
			if err := node.host.Connect(ctx, *info); err != nil {
				node.logger.Debug("auto-connect failed",
					"device", dev.Name,
					"addr", s,
					"error", err,
				)
				continue
			}
			node.logger.Info("auto-connected to device",
				"device", dev.Name,
				"peer_id", info.ID.String(),
			)
			break // connected via one addr, done with this device
		}
	}
}
