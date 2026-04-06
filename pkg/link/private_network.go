package link

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	privateNetworkMembershipMethod = "private-network.membership"

	defaultMembershipProviderCount = 8
	defaultPresenceProviderCount   = 4
)

type privateNetworkMembershipParams struct {
	Identity string `json:"identity,omitempty"`
}

// RegisterPrivateNetworkHandlers exposes the signed private-network membership
// record over skylink so peers discovered through the DHT can rebuild from a
// verified source instead of trusting local cache.
func RegisterPrivateNetworkHandlers(node *Node) {
	if node == nil {
		return
	}
	node.RegisterCapability(
		Capability{
			Name:        privateNetworkMembershipMethod,
			Description: "fetch the signed private-network membership record",
		},
		func(_ context.Context, req *PeerRequest) (interface{}, error) {
			var params privateNetworkMembershipParams
			if len(req.Params) > 0 {
				if err := json.Unmarshal(req.Params, &params); err != nil {
					return nil, fmt.Errorf("invalid membership params: %w", err)
				}
			}
			if params.Identity != "" && params.Identity != node.Address() {
				return nil, fmt.Errorf("identity mismatch")
			}
			return node.CurrentMembershipRecord()
		},
	)
}

func (n *Node) FetchMembershipRecord(ctx context.Context, info peer.AddrInfo, identity string) (*MembershipRecord, error) {
	if n == nil || n.host == nil {
		return nil, fmt.Errorf("node not running")
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := n.host.Connect(callCtx, info); err != nil {
		return nil, fmt.Errorf("connecting to membership provider %s: %w", info.ID, err)
	}

	raw, err := n.Call(callCtx, info.ID, privateNetworkMembershipMethod, privateNetworkMembershipParams{Identity: identity})
	if err != nil {
		return nil, fmt.Errorf("fetching membership from %s: %w", info.ID, err)
	}

	var rec MembershipRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("unmarshaling membership record: %w", err)
	}
	if err := rec.Validate(membershipDHTKey(identity)); err != nil {
		return nil, fmt.Errorf("validating membership record: %w", err)
	}
	return &rec, nil
}

func collectProviderInfos(ch <-chan peer.AddrInfo, limit int) []peer.AddrInfo {
	if limit <= 0 {
		limit = defaultMembershipProviderCount
	}
	seen := make(map[peer.ID]struct{}, limit)
	out := make([]peer.AddrInfo, 0, limit)
	for info := range ch {
		if _, ok := seen[info.ID]; ok {
			continue
		}
		seen[info.ID] = struct{}{}
		out = append(out, info)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (n *Node) FindMembershipProviders(ctx context.Context, identity string, limit int) ([]peer.AddrInfo, error) {
	if n == nil || n.dht == nil {
		return nil, fmt.Errorf("DHT not initialized (network mode required)")
	}
	if limit <= 0 {
		limit = defaultMembershipProviderCount
	}
	infos := collectProviderInfos(n.dht.FindProvidersAsync(ctx, membershipProviderCID(identity), limit), limit)
	if len(infos) == 0 {
		return nil, fmt.Errorf("no membership providers found for %s", identity)
	}
	return infos, nil
}

func (n *Node) FindPresenceProviders(ctx context.Context, identity, devicePubKey string, limit int) ([]peer.AddrInfo, error) {
	if n == nil || n.dht == nil {
		return nil, fmt.Errorf("DHT not initialized (network mode required)")
	}
	if limit <= 0 {
		limit = defaultPresenceProviderCount
	}
	infos := collectProviderInfos(n.dht.FindProvidersAsync(ctx, presenceProviderCID(identity, devicePubKey), limit), limit)
	if len(infos) == 0 {
		return nil, fmt.Errorf("no presence providers found for %s/%s", identity, devicePubKey)
	}
	slices.SortFunc(infos, func(a, b peer.AddrInfo) int {
		switch {
		case a.ID.String() < b.ID.String():
			return -1
		case a.ID.String() > b.ID.String():
			return 1
		default:
			return 0
		}
	})
	return infos, nil
}
