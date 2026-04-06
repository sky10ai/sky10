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
	Info         *peer.AddrInfo
	DevicePubKey string
	PublishedAt  time.Time
	ExpiresAt    time.Time
	Source       string
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
		rec, err := r.resolveMembershipFromDHT(ctx, address)
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

func (r *Resolver) resolveMembershipFromDHT(ctx context.Context, address string) (*MembershipRecord, error) {
	providers, err := r.node.FindMembershipProviders(ctx, address, defaultMembershipProviderCount)
	if err != nil {
		return nil, err
	}

	var best *MembershipRecord
	for _, info := range providers {
		if info.ID == r.node.PeerID() {
			best = selectBestMembership(best, r.localMembershipCandidate(address))
			continue
		}

		rec, err := r.node.FetchMembershipRecord(ctx, info, address)
		if err != nil {
			r.logger.Debug("membership fetch from provider failed",
				"identity", address,
				"provider", info.ID.String(),
				"error", err,
			)
			continue
		}
		best = selectBestMembership(best, rec)
	}
	if best == nil {
		return nil, fmt.Errorf("could not fetch membership for %s from DHT providers", address)
	}
	return best, nil
}

func resolvedPeerSourceRank(source string) int {
	switch source {
	case "dht":
		return 2
	case "nostr":
		return 1
	default:
		return 0
	}
}

func compareResolvedPeers(a, b *ResolvedPeer) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if ar, br := resolvedPeerSourceRank(a.Source), resolvedPeerSourceRank(b.Source); ar != br {
		if ar > br {
			return 1
		}
		return -1
	}
	switch {
	case a.PublishedAt.After(b.PublishedAt):
		return 1
	case a.PublishedAt.Before(b.PublishedAt):
		return -1
	case a.ExpiresAt.After(b.ExpiresAt):
		return 1
	case a.ExpiresAt.Before(b.ExpiresAt):
		return -1
	case a.Info != nil && b.Info != nil && a.Info.ID.String() > b.Info.ID.String():
		return 1
	case a.Info != nil && b.Info != nil && a.Info.ID.String() < b.Info.ID.String():
		return -1
	default:
		return 0
	}
}

func resolvedPeerFromPresence(rec *PresenceRecord, source string) (*ResolvedPeer, error) {
	info, err := addrInfoFromMultiaddrStrings(rec.Multiaddrs)
	if err != nil {
		return nil, err
	}
	return &ResolvedPeer{
		Info:         info,
		DevicePubKey: rec.DevicePubKey,
		PublishedAt:  rec.PublishedAt,
		ExpiresAt:    rec.ExpiresAt,
		Source:       source,
	}, nil
}

func (r *Resolver) resolvePresenceFromDHT(ctx context.Context, identity, devicePubKey string) (*ResolvedPeer, error) {
	devicePub, err := decodeDevicePubKeyHex(devicePubKey)
	if err != nil {
		return nil, err
	}
	expectedPeerID, err := PeerIDFromPubKey(devicePub)
	if err != nil {
		return nil, err
	}

	providers, err := r.node.FindPresenceProviders(ctx, identity, devicePubKey, defaultPresenceProviderCount)
	if err == nil {
		for _, info := range providers {
			if info.ID != expectedPeerID || len(info.Addrs) == 0 {
				continue
			}
			return &ResolvedPeer{
				Info:         &info,
				DevicePubKey: devicePubKey,
				Source:       "dht",
			}, nil
		}
	}

	if r.node != nil && r.node.dht != nil {
		info, err := r.node.dht.FindPeer(ctx, expectedPeerID)
		if err == nil && len(info.Addrs) > 0 {
			return &ResolvedPeer{
				Info:         &info,
				DevicePubKey: devicePubKey,
				Source:       "dht",
			}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("could not resolve DHT presence for %s/%s", identity, devicePubKey)
}

// ResolveAll rebuilds the reachable peer set for a private network identity.
func (r *Resolver) ResolveAll(ctx context.Context, address string) (*Resolution, error) {
	membership, membershipSource, err := r.ResolveMembership(ctx, address)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	byDevice := make(map[string]*ResolvedPeer, len(membership.Devices))
	if r.node != nil && r.node.dht != nil {
		for _, device := range membership.Devices {
			resolved, err := r.resolvePresenceFromDHT(ctx, address, device.PublicKey)
			if err != nil {
				r.logger.Debug("DHT presence resolve failed",
					"identity", address,
					"device_pubkey", device.PublicKey,
					"error", err,
				)
				continue
			}
			byDevice[device.PublicKey] = resolved
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
				resolved, err := resolvedPeerFromPresence(rec, "nostr")
				if err != nil {
					r.logger.Debug("nostr presence addr conversion failed",
						"identity", address,
						"device_pubkey", rec.DevicePubKey,
						"error", err,
					)
					continue
				}
				current := byDevice[rec.DevicePubKey]
				if compareResolvedPeers(resolved, current) > 0 {
					byDevice[rec.DevicePubKey] = resolved
				}
			}
		}
	}

	peers := make([]*ResolvedPeer, 0, len(byDevice))
	for _, device := range membership.Devices {
		candidate, ok := byDevice[device.PublicKey]
		if !ok || candidate == nil || candidate.Info == nil {
			continue
		}
		peers = append(peers, candidate)
	}
	sort.Slice(peers, func(i, j int) bool {
		return compareResolvedPeers(peers[i], peers[j]) > 0
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
