package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// NostrDiscovery publishes and queries multiaddrs via Nostr relays.
// This is a dumb pipe for discovery only — zero application traffic.
type NostrDiscovery struct {
	relays []string
	logger *slog.Logger
}

// nostrEvent is the content of a sky10 discovery event on Nostr.
type nostrEvent struct {
	Address    string   `json:"address"`    // sky10q... address
	Multiaddrs []string `json:"multiaddrs"` // libp2p multiaddrs
	Version    string   `json:"version,omitempty"`
	UpdatedAt  int64    `json:"updated_at"` // unix timestamp
}

// Sky10NostrKind is a NIP-78 application-specific event kind for sky10.
const Sky10NostrKind = 30078

// NewNostrDiscovery creates a Nostr discovery client.
func NewNostrDiscovery(relays []string, logger *slog.Logger) *NostrDiscovery {
	if logger == nil {
		logger = slog.Default()
	}
	return &NostrDiscovery{relays: relays, logger: logger}
}

// Publish publishes the agent's multiaddrs to Nostr relays.
func (d *NostrDiscovery) Publish(ctx context.Context, sk string, address string, multiaddrs []string) error {
	content, err := json.Marshal(nostrEvent{
		Address:    address,
		Multiaddrs: multiaddrs,
		UpdatedAt:  time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("marshaling nostr event: %w", err)
	}

	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return fmt.Errorf("deriving nostr public key: %w", err)
	}

	ev := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      Sky10NostrKind,
		Tags:      nostr.Tags{{"d", "sky10:" + address}},
		Content:   string(content),
	}
	if err := ev.Sign(sk); err != nil {
		return fmt.Errorf("signing nostr event: %w", err)
	}

	for _, relay := range d.relays {
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			d.logger.Debug("nostr relay connect failed", "relay", relay, "error", err)
			continue
		}
		if err := r.Publish(ctx, ev); err != nil {
			d.logger.Debug("nostr publish failed", "relay", relay, "error", err)
		}
		r.Close()
	}
	return nil
}

// Query looks up multiaddrs for a sky10 address on Nostr relays.
func (d *NostrDiscovery) Query(ctx context.Context, address string) ([]string, error) {
	filter := nostr.Filter{
		Kinds: []int{Sky10NostrKind},
		Tags:  nostr.TagMap{"d": []string{"sky10:" + address}},
		Limit: 1,
	}

	for _, relay := range d.relays {
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			d.logger.Debug("nostr relay connect failed", "relay", relay, "error", err)
			continue
		}

		evs, err := r.QuerySync(ctx, filter)
		r.Close()
		if err != nil {
			d.logger.Debug("nostr query failed", "relay", relay, "error", err)
			continue
		}

		for _, ev := range evs {
			var ne nostrEvent
			if err := json.Unmarshal([]byte(ev.Content), &ne); err != nil {
				continue
			}
			if len(ne.Multiaddrs) > 0 {
				return ne.Multiaddrs, nil
			}
		}
	}

	return nil, fmt.Errorf("no multiaddrs found for %s on nostr", address)
}
