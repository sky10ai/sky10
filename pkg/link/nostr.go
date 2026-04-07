package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nbd-wtf/go-nostr"
	skykey "github.com/sky10/sky10/pkg/key"
)

// NostrDiscovery publishes and queries private-network discovery records via
// Nostr relays. This is a discovery fallback only — never application traffic.
type NostrDiscovery struct {
	relays []string
	logger *slog.Logger
}

// Sky10NostrKind is a NIP-78 application-specific event kind for sky10.
const Sky10NostrKind = 30078

// NewNostrDiscovery creates a Nostr discovery client.
func NewNostrDiscovery(relays []string, logger *slog.Logger) *NostrDiscovery {
	return &NostrDiscovery{relays: relays, logger: defaultLogger(logger)}
}

func (d *NostrDiscovery) publish(ctx context.Context, signer *skykey.Key, tags nostr.Tags, content []byte) error {
	if signer == nil || !signer.IsPrivate() {
		return fmt.Errorf("nostr signer must have a private key")
	}
	sk := NostrSecretKey(signer)
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return fmt.Errorf("deriving nostr public key: %w", err)
	}

	ev := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      Sky10NostrKind,
		Tags:      tags,
		Content:   string(content),
	}
	if err := ev.Sign(sk); err != nil {
		return fmt.Errorf("signing nostr event: %w", err)
	}

	var published bool
	for _, relay := range d.relays {
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			d.logger.Debug("nostr relay connect failed", "relay", relay, "error", err)
			continue
		}
		if err := r.Publish(ctx, ev); err != nil {
			d.logger.Debug("nostr publish failed", "relay", relay, "error", err)
			r.Close()
			continue
		}
		published = true
		r.Close()
	}
	if !published {
		return fmt.Errorf("failed to publish to any nostr relay")
	}
	return nil
}

func (d *NostrDiscovery) queryEvents(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	var events []*nostr.Event
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
		events = append(events, evs...)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no nostr discovery events found")
	}
	return events, nil
}

func (d *NostrDiscovery) PublishMembership(ctx context.Context, identity *skykey.Key, rec *MembershipRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling nostr membership record: %w", err)
	}
	tags := nostr.Tags{
		{"d", membershipNostrDTag(rec.Identity)},
		{"i", rec.Identity},
		{"r", "membership"},
	}
	return d.publish(ctx, identity, tags, data)
}

func (d *NostrDiscovery) PublishPresence(ctx context.Context, device *skykey.Key, rec *PresenceRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling nostr presence record: %w", err)
	}
	tags := nostr.Tags{
		{"d", presenceNostrDTag(rec.Identity, rec.DevicePubKey)},
		{"i", rec.Identity},
		{"r", "presence"},
		{"x", rec.DevicePubKey},
	}
	return d.publish(ctx, device, tags, data)
}

func (d *NostrDiscovery) QueryMembership(ctx context.Context, identity string) (*MembershipRecord, error) {
	events, err := d.queryEvents(ctx, nostr.Filter{
		Kinds: []int{Sky10NostrKind},
		Tags:  nostr.TagMap{"d": []string{membershipNostrDTag(identity)}},
		Limit: 16,
	})
	if err != nil {
		return nil, err
	}

	var best *MembershipRecord
	for _, ev := range events {
		var rec MembershipRecord
		if err := json.Unmarshal([]byte(ev.Content), &rec); err != nil {
			continue
		}
		if err := rec.Validate(membershipDHTKey(identity)); err != nil {
			continue
		}
		candidate := rec
		best = selectBestMembership(best, &candidate)
	}
	if best == nil {
		return nil, fmt.Errorf("no valid nostr membership record found for %s", identity)
	}
	return best, nil
}

func (d *NostrDiscovery) QueryPresenceAll(ctx context.Context, identity string) ([]*PresenceRecord, error) {
	events, err := d.queryEvents(ctx, nostr.Filter{
		Kinds: []int{Sky10NostrKind},
		Tags: nostr.TagMap{
			"i": []string{identity},
			"r": []string{"presence"},
		},
		Limit: 128,
	})
	if err != nil {
		return nil, err
	}

	byDevice := make(map[string]*PresenceRecord)
	for _, ev := range events {
		var rec PresenceRecord
		if err := json.Unmarshal([]byte(ev.Content), &rec); err != nil {
			continue
		}
		key := presenceDHTKey(rec.Identity, rec.DevicePubKey)
		if err := rec.Validate(key); err != nil {
			continue
		}
		candidate := rec
		deviceKey := candidate.DevicePubKey
		byDevice[deviceKey] = selectBestPresence(byDevice[deviceKey], &candidate)
	}
	if len(byDevice) == 0 {
		return nil, fmt.Errorf("no valid nostr presence records found for %s", identity)
	}

	out := make([]*PresenceRecord, 0, len(byDevice))
	for _, rec := range byDevice {
		out = append(out, rec)
	}
	return out, nil
}
