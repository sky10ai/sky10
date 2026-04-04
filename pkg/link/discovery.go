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
	nostr   *NostrDiscovery // optional: Nostr relay discovery
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

// WithNostr enables Nostr relay discovery.
func WithNostr(relays []string) ResolverOption {
	return func(r *Resolver) {
		if len(relays) > 0 {
			r.nostr = NewNostrDiscovery(relays, r.logger)
		}
	}
}

// Resolve finds a peer's addresses. Tries layers in order:
// 1. Same bucket (S3 devices/ registry) — own devices
// 2. DHT — any agent (network mode only)
//
// Identity address and peer ID are no longer 1:1, so resolution
// goes through the device registry or DHT records.
func (r *Resolver) Resolve(ctx context.Context, address string) (*peer.AddrInfo, error) {
	// Layer 1: Same bucket — S3 device registry.
	if r.backend != nil {
		info, err := r.resolveFromBucket(ctx, address)
		if err == nil && info != nil {
			return info, nil
		}
		r.logger.Debug("same-bucket resolve failed", "address", address, "error", err)
	}

	// Layer 2: Nostr relay discovery.
	if r.nostr != nil {
		multiaddrs, err := r.nostr.Query(ctx, address)
		if err == nil {
			for _, s := range multiaddrs {
				maddr, err := ma.NewMultiaddr(s)
				if err != nil {
					continue
				}
				info, err := peer.AddrInfoFromP2pAddr(maddr)
				if err != nil {
					continue
				}
				return info, nil
			}
		}
		r.logger.Debug("nostr resolve failed", "address", address, "error", err)
	}

	// Layer 3: DHT agent record (network mode only).
	if r.node.dht != nil {
		rec, err := r.node.ResolveRecord(ctx, address)
		if err == nil && len(rec.Multiaddrs) > 0 {
			for _, s := range rec.Multiaddrs {
				if a, err := ma.NewMultiaddr(s); err == nil {
					if info, err := peer.AddrInfoFromP2pAddr(a); err == nil {
						return info, nil
					}
				}
			}
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

// resolveFromBucket looks up devices by identity address in the S3 device registry.
func (r *Resolver) resolveFromBucket(ctx context.Context, address string) (*peer.AddrInfo, error) {
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

		// Parse multiaddrs (each contains /p2p/<peerID>).
		for _, s := range dev.Multiaddrs {
			maddr, err := ma.NewMultiaddr(s)
			if err != nil {
				continue
			}
			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				continue
			}
			return info, nil
		}
	}

	return nil, fmt.Errorf("device not found in bucket")
}

// AutoConnect discovers own devices and connects to them via their published
// multiaddrs. Tries S3 device registry (if backend is non-nil) and Nostr
// relays. Skips self by comparing peer IDs.
func AutoConnect(ctx context.Context, node *Node, backend adapter.Backend, nostrRelays []string) {
	selfPeerID := node.PeerID().String()

	// Layer 1: S3 device registry.
	if backend != nil {
		autoConnectFromS3(ctx, node, backend, selfPeerID)
	}

	// Layer 2: Nostr relays — query our own identity address.
	if len(nostrRelays) > 0 {
		autoConnectFromNostr(ctx, node, nostrRelays, selfPeerID)
	}
}

func autoConnectFromS3(ctx context.Context, node *Node, backend adapter.Backend, selfPeerID string) {
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

		connectMultiaddrs(ctx, node, dev.Name, dev.Multiaddrs, selfPeerID)
	}
}

func autoConnectFromNostr(ctx context.Context, node *Node, relays []string, selfPeerID string) {
	nd := NewNostrDiscovery(relays, node.logger)
	allAddrs, err := nd.QueryAll(ctx, node.Address())
	if err != nil {
		node.logger.Debug("auto-connect nostr query failed", "error", err)
		return
	}
	for _, addrs := range allAddrs {
		connectMultiaddrs(ctx, node, "nostr-peer", addrs, selfPeerID)
	}
}

func connectMultiaddrs(ctx context.Context, node *Node, name string, addrs []string, selfPeerID string) {
	for _, s := range addrs {
		maddr, err := ma.NewMultiaddr(s)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			continue
		}
		if info.ID.String() == selfPeerID {
			break // this is us
		}
		if err := node.host.Connect(ctx, *info); err != nil {
			node.logger.Debug("auto-connect failed",
				"device", name,
				"addr", s,
				"error", err,
			)
			continue
		}
		node.logger.Info("auto-connected to device",
			"device", name,
			"peer_id", info.ID.String(),
		)
		break
	}
}
