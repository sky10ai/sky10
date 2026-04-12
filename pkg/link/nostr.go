package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
	skykey "github.com/sky10/sky10/pkg/key"
)

// NostrDiscovery publishes and queries private-network discovery records via
// Nostr relays. This is a discovery fallback only — never application traffic.
type NostrDiscovery struct {
	relays  []string
	logger  *slog.Logger
	tracker *NostrRelayTracker

	cacheMu         sync.RWMutex
	membershipCache map[string]MembershipRecord
	presenceCache   map[string]map[string]PresenceRecord
	queryFn         func(context.Context, nostr.Filter) ([]*nostr.Event, error)
}

// Sky10NostrKind is a NIP-78 application-specific event kind for sky10.
const Sky10NostrKind = 30078

// NewNostrDiscovery creates a Nostr discovery client.
func NewNostrDiscovery(relays []string, logger *slog.Logger) *NostrDiscovery {
	return NewNostrDiscoveryWithTracker(relays, logger, nil)
}

// NewNostrDiscoveryWithTracker creates a Nostr discovery client with optional
// relay health tracking and ranking.
func NewNostrDiscoveryWithTracker(relays []string, logger *slog.Logger, tracker *NostrRelayTracker) *NostrDiscovery {
	return &NostrDiscovery{
		relays:          append([]string(nil), relays...),
		logger:          defaultLogger(logger),
		tracker:         tracker,
		membershipCache: make(map[string]MembershipRecord),
		presenceCache:   make(map[string]map[string]PresenceRecord),
	}
}

func (d *NostrDiscovery) publish(ctx context.Context, signer *skykey.Key, operation string, tags nostr.Tags, content []byte) (NostrPublishOutcome, error) {
	if signer == nil || !signer.IsPrivate() {
		return NostrPublishOutcome{}, fmt.Errorf("nostr signer must have a private key")
	}
	sk := NostrSecretKey(signer)
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return NostrPublishOutcome{}, fmt.Errorf("deriving nostr public key: %w", err)
	}

	ev := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      Sky10NostrKind,
		Tags:      tags,
		Content:   string(content),
	}
	if err := ev.Sign(sk); err != nil {
		return NostrPublishOutcome{}, fmt.Errorf("signing nostr event: %w", err)
	}

	ordered := d.orderedRelays()
	successes := 0
	for _, relay := range ordered {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			d.recordRelay(relay, time.Since(started), err)
			d.logger.Debug("nostr relay connect failed", "relay", relay, "error", err)
			continue
		}
		if err := r.Publish(ctx, ev); err != nil {
			d.recordRelay(relay, time.Since(started), err)
			d.logger.Debug("nostr publish failed", "relay", relay, "error", err)
			r.Close()
			continue
		}
		d.recordRelay(relay, time.Since(started), nil)
		successes++
		r.Close()
	}
	outcome := d.recordPublishOutcome(operation, len(ordered), successes)
	if successes == 0 {
		return outcome, fmt.Errorf("failed to publish to any nostr relay")
	}
	return outcome, nil
}

func (d *NostrDiscovery) queryEvents(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	if d != nil && d.queryFn != nil {
		return d.queryFn(ctx, filter)
	}
	var events []*nostr.Event
	for _, relay := range d.orderedRelays() {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			d.recordRelay(relay, time.Since(started), err)
			d.logger.Debug("nostr relay connect failed", "relay", relay, "error", err)
			continue
		}
		evs, err := r.QuerySync(ctx, filter)
		r.Close()
		if err != nil {
			d.recordRelay(relay, time.Since(started), err)
			d.logger.Debug("nostr query failed", "relay", relay, "error", err)
			continue
		}
		d.recordRelay(relay, time.Since(started), nil)
		events = append(events, evs...)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no nostr discovery events found")
	}
	return events, nil
}

func (d *NostrDiscovery) PublishMembership(ctx context.Context, identity *skykey.Key, rec *MembershipRecord) (NostrPublishOutcome, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return NostrPublishOutcome{}, fmt.Errorf("marshaling nostr membership record: %w", err)
	}
	tags := nostr.Tags{
		{"d", membershipNostrDTag(rec.Identity)},
		{"i", rec.Identity},
		{"r", "membership"},
	}
	return d.publish(ctx, identity, "membership", tags, data)
}

func (d *NostrDiscovery) PublishPresence(ctx context.Context, device *skykey.Key, rec *PresenceRecord) (NostrPublishOutcome, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return NostrPublishOutcome{}, fmt.Errorf("marshaling nostr presence record: %w", err)
	}
	tags := nostr.Tags{
		{"d", presenceNostrDTag(rec.Identity, rec.DevicePubKey)},
		{"i", rec.Identity},
		{"r", "presence"},
		{"x", rec.DevicePubKey},
	}
	return d.publish(ctx, device, "presence", tags, data)
}

func (d *NostrDiscovery) QueryMembership(ctx context.Context, identity string) (*MembershipRecord, error) {
	events, err := d.queryEvents(ctx, nostr.Filter{
		Kinds: []int{Sky10NostrKind},
		Tags:  nostr.TagMap{"d": []string{membershipNostrDTag(identity)}},
		Limit: 16,
	})
	if err != nil {
		if cached := d.cachedMembership(identity); cached != nil {
			return cached, nil
		}
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
	if cached := d.cachedMembership(identity); cached != nil {
		best = selectBestMembership(best, cached)
	}
	if best == nil {
		return nil, fmt.Errorf("no valid nostr membership record found for %s", identity)
	}
	d.storeMembership(best)
	return cloneMembershipRecord(best), nil
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
		cached := d.cachedPresence(identity, time.Now().UTC())
		if len(cached) == 0 {
			return nil, err
		}
		out := make([]*PresenceRecord, 0, len(cached))
		for _, rec := range cached {
			out = append(out, clonePresenceRecord(rec))
		}
		return out, nil
	}

	byDevice := d.cachedPresence(identity, time.Now().UTC())
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
		out = append(out, clonePresenceRecord(rec))
	}
	d.storePresence(identity, out)
	return out, nil
}

func (d *NostrDiscovery) orderedRelays() []string {
	if d == nil {
		return nil
	}
	if d.tracker == nil {
		return append([]string(nil), d.relays...)
	}
	return d.tracker.Ordered(d.relays)
}

func (d *NostrDiscovery) recordRelay(relay string, latency time.Duration, err error) {
	if d == nil || d.tracker == nil {
		return
	}
	d.tracker.Record(relay, latency, err)
}

func (d *NostrDiscovery) recordPublishOutcome(operation string, attempts, successes int) NostrPublishOutcome {
	quorum := DefaultNostrPublishQuorum(attempts)
	if d == nil || d.tracker == nil {
		return NostrPublishOutcome{
			Operation: operation,
			Attempts:  attempts,
			Successes: successes,
			Quorum:    quorum,
			Degraded:  successes > 0 && quorum > 0 && successes < quorum,
			At:        timePtr(time.Now().UTC()),
		}
	}
	return d.tracker.RecordPublishOutcome(operation, attempts, successes, quorum)
}

func (d *NostrDiscovery) cachedMembership(identity string) *MembershipRecord {
	if d == nil {
		return nil
	}
	d.cacheMu.RLock()
	defer d.cacheMu.RUnlock()
	rec, ok := d.membershipCache[identity]
	if !ok {
		return nil
	}
	return cloneMembershipRecord(&rec)
}

func (d *NostrDiscovery) storeMembership(rec *MembershipRecord) {
	if d == nil || rec == nil {
		return
	}
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	d.membershipCache[rec.Identity] = *cloneMembershipRecord(rec)
}

func (d *NostrDiscovery) cachedPresence(identity string, now time.Time) map[string]*PresenceRecord {
	out := make(map[string]*PresenceRecord)
	if d == nil {
		return out
	}
	d.cacheMu.RLock()
	defer d.cacheMu.RUnlock()
	for device, rec := range d.presenceCache[identity] {
		candidate := rec
		if !candidate.ExpiresAt.After(now) {
			continue
		}
		out[device] = clonePresenceRecord(&candidate)
	}
	return out
}

func (d *NostrDiscovery) storePresence(identity string, records []*PresenceRecord) {
	if d == nil {
		return
	}
	filtered := make(map[string]PresenceRecord)
	now := time.Now().UTC()
	for _, rec := range records {
		if rec == nil || !rec.ExpiresAt.After(now) {
			continue
		}
		filtered[rec.DevicePubKey] = *clonePresenceRecord(rec)
	}
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	if len(filtered) == 0 {
		delete(d.presenceCache, identity)
		return
	}
	d.presenceCache[identity] = filtered
}

func cloneMembershipRecord(rec *MembershipRecord) *MembershipRecord {
	if rec == nil {
		return nil
	}
	out := *rec
	out.Devices = append([]MembershipDevice(nil), rec.Devices...)
	out.Revoked = append([]RevokedDevice(nil), rec.Revoked...)
	out.Signature = append([]byte(nil), rec.Signature...)
	return &out
}

func clonePresenceRecord(rec *PresenceRecord) *PresenceRecord {
	if rec == nil {
		return nil
	}
	out := *rec
	out.Multiaddrs = append([]string(nil), rec.Multiaddrs...)
	out.Signature = append([]byte(nil), rec.Signature...)
	return &out
}
