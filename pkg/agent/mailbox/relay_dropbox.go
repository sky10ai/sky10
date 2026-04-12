package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

const (
	relayMailboxRole          = "mailbox"
	relayRecordTypeItem       = "item"
	relayRecordTypeDelivery   = "delivery_receipt"
	relayRecordTypeQueueClaim = "queue_claim"
	defaultRelayQueryLimit    = 256
	defaultRelayPollInterval  = 15 * time.Second
)

// DefaultRelayPollInterval returns the default public-network relay poll
// cadence used by the daemon.
func DefaultRelayPollInterval() time.Duration { return defaultRelayPollInterval }

// NetworkRelay is the public-network store-and-forward interface used by the
// mailbox router.
type NetworkRelay interface {
	HandoffItem(ctx context.Context, item Item) (RelayHandoff, error)
	PublishDeliveryReceipt(ctx context.Context, recipient string, receipt RelayDeliveryReceipt) error
	PublishQueueClaim(ctx context.Context, recipient string, claim QueueClaim) error
	Poll(ctx context.Context) ([]RelayInbound, error)
}

// RelayHandoff is the sender-side confirmation that an item was stored in the
// public-network dropbox.
type RelayHandoff struct {
	ID          string
	Recipient   string
	PublishedAt time.Time
}

// RelayDeliveryReceipt confirms that a recipient mailbox host ingested a
// handed-off item.
type RelayDeliveryReceipt struct {
	ItemID      string    `json:"item_id"`
	HandoffID   string    `json:"handoff_id"`
	DeliveredBy string    `json:"delivered_by"`
	DeliveredAt time.Time `json:"delivered_at"`
}

// RelayInbound is one decoded, recipient-opened relay envelope.
type RelayInbound struct {
	EventID    string
	RecordType string
	HandoffID  string
	Sender     string
	Recipient  string
	CreatedAt  time.Time
	Item       *Item
	Receipt    *RelayDeliveryReceipt
	Claim      *QueueClaim
}

type relayEnvelope struct {
	Version   int                   `json:"version"`
	Type      string                `json:"type"`
	HandoffID string                `json:"handoff_id"`
	Sender    string                `json:"sender"`
	Recipient string                `json:"recipient"`
	CreatedAt time.Time             `json:"created_at"`
	Item      *Item                 `json:"item,omitempty"`
	Receipt   *RelayDeliveryReceipt `json:"receipt,omitempty"`
	Claim     *QueueClaim           `json:"claim,omitempty"`
}

// RelayTransportEvent is one sealed record written to or read from a
// store-and-forward relay transport.
type RelayTransportEvent struct {
	ID         string
	DTag       string
	RecordType string
	Recipient  string
	ItemID     string
	CreatedAt  time.Time
	Payload    []byte
}

// RelayTransport stores and queries sealed relay envelopes.
type RelayTransport interface {
	Publish(ctx context.Context, signer *skykey.Key, event RelayTransportEvent) error
	Query(ctx context.Context, recipient, recordType string, limit int) ([]RelayTransportEvent, error)
}

// RelayDropbox implements public-network mailbox handoff over Nostr relays.
type RelayDropbox struct {
	owner     *skykey.Key
	transport RelayTransport
	logger    *slog.Logger
	now       func() time.Time
}

// NewRelayDropbox creates a relay-backed mailbox helper for one identity.
func NewRelayDropbox(owner *skykey.Key, transport RelayTransport, logger *slog.Logger) *RelayDropbox {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(ioDiscard{}, nil))
	}
	return &RelayDropbox{
		owner:     owner,
		transport: transport,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// NewNostrRelayTransport creates a Nostr-backed relay transport.
func NewNostrRelayTransport(relays []string, logger *slog.Logger) RelayTransport {
	return NewNostrRelayTransportWithTracker(relays, logger, nil)
}

// NewNostrRelayTransportWithTracker creates a Nostr-backed relay transport
// with optional shared relay health tracking.
func NewNostrRelayTransportWithTracker(relays []string, logger *slog.Logger, tracker *link.NostrRelayTracker) RelayTransport {
	return &nostrRelayTransport{relays: relays, logger: logger, tracker: tracker}
}

// HandoffItem stores a mailbox item in the public-network dropbox for the
// recipient's sky10 address.
func (r *RelayDropbox) HandoffItem(ctx context.Context, item Item) (RelayHandoff, error) {
	if r == nil || r.owner == nil || !r.owner.IsPrivate() {
		return RelayHandoff{}, fmt.Errorf("relay dropbox owner key is required")
	}
	if r.transport == nil {
		return RelayHandoff{}, fmt.Errorf("relay dropbox transport is required")
	}
	if item.To == nil {
		return RelayHandoff{}, fmt.Errorf("relay mailbox item %s has no recipient", item.ID)
	}
	recipient := item.To.RouteAddress()
	if recipient == "" {
		return RelayHandoff{}, fmt.Errorf("relay mailbox item %s has no recipient route address", item.ID)
	}

	stored := cloneItem(item)
	if stored.From.RouteAddress() == "" {
		stored.From.RouteHint = r.owner.Address()
	}

	publishedAt := r.now()
	envelope := relayEnvelope{
		Version:   1,
		Type:      relayRecordTypeItem,
		HandoffID: stored.ID,
		Sender:    r.owner.Address(),
		Recipient: recipient,
		CreatedAt: publishedAt,
		Item:      &stored,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return RelayHandoff{}, fmt.Errorf("marshal relay item %s: %w", stored.ID, err)
	}
	sealed, err := skykey.SealFor(payload, recipient)
	if err != nil {
		return RelayHandoff{}, fmt.Errorf("seal relay item %s: %w", stored.ID, err)
	}
	if err := r.transport.Publish(ctx, r.owner, RelayTransportEvent{
		DTag:       relayItemDTag(recipient, stored.ID),
		RecordType: relayRecordTypeItem,
		Recipient:  recipient,
		ItemID:     stored.ID,
		CreatedAt:  publishedAt,
		Payload:    sealed,
	}); err != nil {
		return RelayHandoff{}, err
	}
	return RelayHandoff{ID: stored.ID, Recipient: recipient, PublishedAt: publishedAt}, nil
}

// PublishDeliveryReceipt stores a recipient-side receipt for the sender to
// poll later.
func (r *RelayDropbox) PublishDeliveryReceipt(ctx context.Context, recipient string, receipt RelayDeliveryReceipt) error {
	if r == nil || r.owner == nil || !r.owner.IsPrivate() {
		return fmt.Errorf("relay dropbox owner key is required")
	}
	if r.transport == nil {
		return fmt.Errorf("relay dropbox transport is required")
	}
	if _, err := skykey.ParseAddress(strings.TrimSpace(recipient)); err != nil {
		return fmt.Errorf("invalid receipt recipient: %w", err)
	}
	if strings.TrimSpace(receipt.ItemID) == "" {
		return fmt.Errorf("delivery receipt item_id is required")
	}
	if strings.TrimSpace(receipt.HandoffID) == "" {
		receipt.HandoffID = receipt.ItemID
	}
	if strings.TrimSpace(receipt.DeliveredBy) == "" {
		receipt.DeliveredBy = r.owner.Address()
	}
	if receipt.DeliveredAt.IsZero() {
		receipt.DeliveredAt = r.now()
	}

	envelope := relayEnvelope{
		Version:   1,
		Type:      relayRecordTypeDelivery,
		HandoffID: receipt.HandoffID,
		Sender:    r.owner.Address(),
		Recipient: recipient,
		CreatedAt: receipt.DeliveredAt,
		Receipt:   &receipt,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal delivery receipt %s: %w", receipt.ItemID, err)
	}
	sealed, err := skykey.SealFor(payload, recipient)
	if err != nil {
		return fmt.Errorf("seal delivery receipt %s: %w", receipt.ItemID, err)
	}
	return r.transport.Publish(ctx, r.owner, RelayTransportEvent{
		DTag:       relayReceiptDTag(recipient, receipt.ItemID),
		RecordType: relayRecordTypeDelivery,
		Recipient:  recipient,
		ItemID:     receipt.ItemID,
		CreatedAt:  receipt.DeliveredAt,
		Payload:    sealed,
	})
}

// PublishQueueClaim stores one sealed claim request for the sender to inspect
// and arbitrate.
func (r *RelayDropbox) PublishQueueClaim(ctx context.Context, recipient string, claim QueueClaim) error {
	if r == nil || r.owner == nil || !r.owner.IsPrivate() {
		return fmt.Errorf("relay dropbox owner key is required")
	}
	if r.transport == nil {
		return fmt.Errorf("relay dropbox transport is required")
	}
	if _, err := skykey.ParseAddress(strings.TrimSpace(recipient)); err != nil {
		return fmt.Errorf("invalid claim recipient: %w", err)
	}
	if err := claim.Validate(); err != nil {
		return err
	}
	if claim.Sender == "" {
		claim.Sender = recipient
	}
	if claim.Claimant == "" {
		claim.Claimant = r.owner.Address()
	}
	if claim.RequestedAt.IsZero() {
		claim.RequestedAt = r.now()
	}

	envelope := relayEnvelope{
		Version:   1,
		Type:      relayRecordTypeQueueClaim,
		HandoffID: claim.ItemID,
		Sender:    r.owner.Address(),
		Recipient: recipient,
		CreatedAt: claim.RequestedAt,
		Claim:     &claim,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal queue claim %s: %w", claim.ClaimID, err)
	}
	sealed, err := skykey.SealFor(payload, recipient)
	if err != nil {
		return fmt.Errorf("seal queue claim %s: %w", claim.ClaimID, err)
	}
	return r.transport.Publish(ctx, r.owner, RelayTransportEvent{
		DTag:       relayClaimDTag(recipient, claim.ItemID, claim.ClaimID),
		RecordType: relayRecordTypeQueueClaim,
		Recipient:  recipient,
		ItemID:     claim.ItemID,
		CreatedAt:  claim.RequestedAt,
		Payload:    sealed,
	})
}

// Poll opens all relay envelopes currently addressed to the dropbox owner.
func (r *RelayDropbox) Poll(ctx context.Context) ([]RelayInbound, error) {
	if r == nil || r.owner == nil || !r.owner.IsPrivate() {
		return nil, fmt.Errorf("relay dropbox owner key is required")
	}
	if r.transport == nil {
		return nil, fmt.Errorf("relay dropbox transport is required")
	}

	address := r.owner.Address()
	recordTypes := []string{relayRecordTypeItem, relayRecordTypeDelivery, relayRecordTypeQueueClaim}
	var events []RelayTransportEvent
	for _, recordType := range recordTypes {
		found, err := r.transport.Query(ctx, address, recordType, defaultRelayQueryLimit)
		if err != nil {
			return nil, err
		}
		events = append(events, found...)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})

	inbound := make([]RelayInbound, 0, len(events))
	for _, event := range events {
		plain, err := skykey.Open(event.Payload, r.owner.PrivateKey)
		if err != nil {
			r.logger.Debug("relay payload open failed", "event_id", event.ID, "error", err)
			continue
		}
		var envelope relayEnvelope
		if err := json.Unmarshal(plain, &envelope); err != nil {
			r.logger.Debug("relay payload decode failed", "event_id", event.ID, "error", err)
			continue
		}
		if envelope.Recipient != address {
			continue
		}
		inbound = append(inbound, RelayInbound{
			EventID:    event.ID,
			RecordType: envelope.Type,
			HandoffID:  envelope.HandoffID,
			Sender:     envelope.Sender,
			Recipient:  envelope.Recipient,
			CreatedAt:  envelope.CreatedAt,
			Item:       envelope.Item,
			Receipt:    envelope.Receipt,
			Claim:      envelope.Claim,
		})
	}
	return inbound, nil
}

type nostrRelayTransport struct {
	relays  []string
	logger  *slog.Logger
	tracker *link.NostrRelayTracker
}

func (t *nostrRelayTransport) Publish(ctx context.Context, signer *skykey.Key, event RelayTransportEvent) error {
	if signer == nil || !signer.IsPrivate() {
		return fmt.Errorf("nostr signer must have a private key")
	}
	sk := link.NostrSecretKey(signer)
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return fmt.Errorf("deriving nostr public key: %w", err)
	}

	ev := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Timestamp(event.CreatedAt.Unix()),
		Kind:      link.Sky10NostrKind,
		Tags: nostr.Tags{
			{"d", event.DTag},
			{"i", event.Recipient},
			{"r", relayMailboxRole},
			{"t", event.RecordType},
			{"m", event.ItemID},
		},
		Content: base64.RawURLEncoding.EncodeToString(event.Payload),
	}
	if err := ev.Sign(sk); err != nil {
		return fmt.Errorf("signing relay event: %w", err)
	}

	ordered := t.orderedRelays()
	successes := 0
	for _, relay := range ordered {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("relay connect failed", "relay", relay, "error", err)
			continue
		}
		if err := r.Publish(ctx, ev); err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("relay publish failed", "relay", relay, "error", err)
			r.Close()
			continue
		}
		t.recordRelay(relay, time.Since(started), nil)
		successes++
		r.Close()
	}
	t.recordPublishOutcome("mailbox_"+event.RecordType, len(ordered), successes)
	if successes == 0 {
		return fmt.Errorf("failed to publish relay event to any nostr relay")
	}
	return nil
}

func (t *nostrRelayTransport) Query(ctx context.Context, recipient, recordType string, limit int) ([]RelayTransportEvent, error) {
	filter := nostr.Filter{
		Kinds: []int{link.Sky10NostrKind},
		Tags: nostr.TagMap{
			"i": []string{recipient},
			"r": []string{relayMailboxRole},
			"t": []string{recordType},
		},
		Limit: limit,
	}
	byID := make(map[string]RelayTransportEvent)
	for _, relay := range t.orderedRelays() {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("relay connect failed", "relay", relay, "error", err)
			continue
		}
		events, err := r.QuerySync(ctx, filter)
		r.Close()
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("relay query failed", "relay", relay, "error", err)
			continue
		}
		t.recordRelay(relay, time.Since(started), nil)
		for _, event := range events {
			payload, err := base64.RawURLEncoding.DecodeString(event.Content)
			if err != nil {
				continue
			}
			byID[event.ID] = RelayTransportEvent{
				ID:         event.ID,
				DTag:       nostrTagValue(event.Tags, "d"),
				RecordType: recordType,
				Recipient:  recipient,
				ItemID:     nostrTagValue(event.Tags, "m"),
				CreatedAt:  event.CreatedAt.Time(),
				Payload:    payload,
			}
		}
	}
	if len(byID) == 0 {
		return nil, nil
	}
	out := make([]RelayTransportEvent, 0, len(byID))
	for _, event := range byID {
		out = append(out, event)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (t *nostrRelayTransport) debug(msg string, args ...interface{}) {
	if t.logger == nil {
		return
	}
	t.logger.Debug(msg, args...)
}

func (t *nostrRelayTransport) orderedRelays() []string {
	if t == nil {
		return nil
	}
	if t.tracker == nil {
		return append([]string(nil), t.relays...)
	}
	return t.tracker.Ordered(t.relays)
}

func (t *nostrRelayTransport) recordRelay(relay string, latency time.Duration, err error) {
	if t == nil || t.tracker == nil {
		return
	}
	t.tracker.Record(relay, latency, err)
}

func (t *nostrRelayTransport) recordPublishOutcome(operation string, attempts, successes int) {
	if t == nil || t.tracker == nil {
		return
	}
	t.tracker.RecordPublishOutcome(operation, attempts, successes, link.DefaultNostrPublishQuorum(attempts))
}

func relayItemDTag(recipient, itemID string) string {
	return "mailbox:item:" + recipient + ":" + itemID
}

func relayReceiptDTag(recipient, itemID string) string {
	return "mailbox:receipt:" + recipient + ":" + itemID
}

func relayClaimDTag(recipient, itemID, claimID string) string {
	return "mailbox:claim:" + recipient + ":" + itemID + ":" + claimID
}

func nostrTagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
