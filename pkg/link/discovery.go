package link

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/adapter"
)

// ResolvedPeer is one reachable device in a private network.
type ResolvedPeer struct {
	Info     *peer.AddrInfo
	Presence *PresenceRecord
	Source   string
}

// Resolution contains the winning membership record plus the currently
// reachable peers discovered for that identity.
type Resolution struct {
	Identity         string            `json:"identity"`
	Membership       *MembershipRecord `json:"membership,omitempty"`
	MembershipSource string            `json:"membership_source,omitempty"`
	Peers            []*ResolvedPeer   `json:"peers,omitempty"`
}

// Resolver finds peer addresses through the private-network discovery layers.
type Resolver struct {
	node    *Node
	backend adapter.Backend // deprecated; kept for construction compatibility
	nostr   *NostrDiscovery
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

// WithBackend keeps construction compatibility with the older resolver shape.
// Private-network discovery no longer treats the S3 device registry as an
// authoritative or required discovery path.
func WithBackend(b adapter.Backend) ResolverOption {
	return func(r *Resolver) { r.backend = b }
}

// WithNostr enables Nostr relay discovery fallback.
func WithNostr(relays []string) ResolverOption {
	return func(r *Resolver) {
		if len(relays) > 0 {
			r.nostr = NewNostrDiscovery(relays, r.logger)
		}
	}
}

func (r *Resolver) localMembershipCandidate(address string) *MembershipRecord {
	if r.node == nil || r.node.Bundle() == nil || r.node.Bundle().Manifest == nil {
		return nil
	}
	if r.node.Address() != address {
		return nil
	}
	rec := membershipRecordFromManifest(r.node.Bundle().Manifest)
	if rec == nil {
		return nil
	}
	if err := rec.Sign(r.node.Bundle().Identity.PrivateKey); err != nil {
		r.logger.Debug("signing local membership candidate failed", "error", err)
		return nil
	}
	return rec
}

// ResolveMembership returns the best verified membership record for an
// identity, choosing between local cache (for the current node), DHT, and
// Nostr fallback.
func (r *Resolver) ResolveMembership(ctx context.Context, address string) (*MembershipRecord, string, error) {
	type candidate struct {
		record *MembershipRecord
		source string
	}

	var candidates []candidate
	if local := r.localMembershipCandidate(address); local != nil {
		candidates = append(candidates, candidate{record: local, source: "local"})
	}
	if r.node != nil && r.node.dht != nil {
		rec, err := r.node.ResolveMembershipRecord(ctx, address)
		if err == nil {
			candidates = append(candidates, candidate{record: rec, source: "dht"})
		} else {
			r.logger.Debug("DHT membership resolve failed", "address", address, "error", err)
		}
	}
	if r.nostr != nil {
		rec, err := r.nostr.QueryMembership(ctx, address)
		if err == nil {
			candidates = append(candidates, candidate{record: rec, source: "nostr"})
		} else {
			r.logger.Debug("nostr membership resolve failed", "address", address, "error", err)
		}
	}
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("could not resolve membership for %s", address)
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if compareMembershipRecords(candidate.record, best.record) > 0 {
			best = candidate
		}
	}
	return best.record, best.source, nil
}

// ResolveAll rebuilds the reachable peer set for a private network identity.
func (r *Resolver) ResolveAll(ctx context.Context, address string) (*Resolution, error) {
	membership, membershipSource, err := r.ResolveMembership(ctx, address)
	if err != nil {
		return nil, err
	}

	type presenceCandidate struct {
		record *PresenceRecord
		source string
	}

	now := time.Now().UTC()
	byDevice := make(map[string]presenceCandidate, len(membership.Devices))
	if r.node != nil && r.node.dht != nil {
		for _, device := range membership.Devices {
			rec, err := r.node.ResolvePresenceRecord(ctx, address, device.PublicKey)
			if err != nil {
				r.logger.Debug("DHT presence resolve failed",
					"identity", address,
					"device_pubkey", device.PublicKey,
					"error", err,
				)
				continue
			}
			if !rec.Usable(membership, now) {
				continue
			}
			byDevice[device.PublicKey] = presenceCandidate{record: rec, source: "dht"}
		}
	}
	if r.nostr != nil {
		recs, err := r.nostr.QueryPresenceAll(ctx, address)
		if err != nil {
			r.logger.Debug("nostr presence resolve failed", "identity", address, "error", err)
		} else {
			for _, rec := range recs {
				if !rec.Usable(membership, now) {
					continue
				}
				current := byDevice[rec.DevicePubKey]
				if comparePresenceRecords(rec, current.record) > 0 {
					byDevice[rec.DevicePubKey] = presenceCandidate{record: rec, source: "nostr"}
				}
			}
		}
	}

	peers := make([]*ResolvedPeer, 0, len(byDevice))
	for _, device := range membership.Devices {
		candidate, ok := byDevice[device.PublicKey]
		if !ok || candidate.record == nil {
			continue
		}
		info, err := addrInfoFromMultiaddrStrings(candidate.record.Multiaddrs)
		if err != nil {
			r.logger.Debug("presence addr conversion failed",
				"identity", address,
				"device_pubkey", device.PublicKey,
				"error", err,
			)
			continue
		}
		peers = append(peers, &ResolvedPeer{
			Info:     info,
			Presence: candidate.record,
			Source:   candidate.source,
		})
	}
	sort.Slice(peers, func(i, j int) bool {
		return comparePresenceRecords(peers[i].Presence, peers[j].Presence) > 0
	})

	if len(peers) == 0 {
		return nil, fmt.Errorf("could not resolve any live peers for %s", address)
	}
	return &Resolution{
		Identity:         address,
		Membership:       membership,
		MembershipSource: membershipSource,
		Peers:            peers,
	}, nil
}

// Resolve returns the freshest reachable peer for the given identity.
func (r *Resolver) Resolve(ctx context.Context, address string) (*peer.AddrInfo, error) {
	resolution, err := r.ResolveAll(ctx, address)
	if err != nil {
		return nil, err
	}
	if len(resolution.Peers) == 0 {
		return nil, fmt.Errorf("could not resolve %s", address)
	}
	return resolution.Peers[0].Info, nil
}

// Connect resolves a peer's addresses and establishes a connection.
func (r *Resolver) Connect(ctx context.Context, address string) error {
	info, err := r.Resolve(ctx, address)
	if err != nil {
		return err
	}
	return r.node.host.Connect(ctx, *info)
}

// AutoConnect discovers all reachable peers in the current node's private
// network and connects to them. It is resilient to stale local cache because
// the resolver chooses the best signed membership first.
func AutoConnect(ctx context.Context, resolver *Resolver) {
	if resolver == nil || resolver.node == nil {
		return
	}

	resolution, err := resolver.ResolveAll(ctx, resolver.node.Address())
	if err != nil {
		resolver.logger.Debug("auto-connect resolve failed", "error", err)
		return
	}

	selfPeerID := resolver.node.PeerID()
	for _, resolved := range resolution.Peers {
		if resolved.Info == nil || resolved.Info.ID == selfPeerID {
			continue
		}
		if resolver.node.host.Network().Connectedness(resolved.Info.ID) == network.Connected {
			continue
		}
		if err := resolver.node.host.Connect(ctx, *resolved.Info); err != nil {
			resolver.logger.Debug("auto-connect failed",
				"peer_id", resolved.Info.ID.String(),
				"source", resolved.Source,
				"error", err,
			)
			continue
		}
		resolver.logger.Info("auto-connected to private-network peer",
			"peer_id", resolved.Info.ID.String(),
			"source", resolved.Source,
		)
	}
}
