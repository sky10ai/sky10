package mailbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

const (
	queueOfferRole           = "queue_offer"
	queueOfferStatusOpen     = "open"
	queueOfferStatusAssigned = "assigned"
	defaultQueueQueryLimit   = 256
	defaultClaimTTL          = time.Minute
)

// QueueOffer is the minimal public metadata published for a claimable
// sky10-network task. Payload bytes stay sealed in the addressed mailbox path.
type QueueOffer struct {
	ItemID    string    `json:"item_id"`
	RequestID string    `json:"request_id,omitempty"`
	Sender    string    `json:"sender"`
	Queue     string    `json:"queue"`
	Skill     string    `json:"skill,omitempty"`
	Method    string    `json:"method,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Status    string    `json:"status,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Open reports whether the offer is currently available for claims.
func (o QueueOffer) Open() bool {
	status := strings.TrimSpace(o.Status)
	return status == "" || status == queueOfferStatusOpen
}

// QueueOfferFilter selects public queue offers.
type QueueOfferFilter struct {
	Skill string
	Queue string
	Limit int
}

// QueueClaim is a sealed claim request sent back to the queue sender.
type QueueClaim struct {
	ClaimID     string    `json:"claim_id"`
	ItemID      string    `json:"item_id"`
	RequestID   string    `json:"request_id,omitempty"`
	Sender      string    `json:"sender"`
	Queue       string    `json:"queue"`
	Skill       string    `json:"skill,omitempty"`
	AgentID     string    `json:"agent_id"`
	Claimant    string    `json:"claimant"`
	RequestedAt time.Time `json:"requested_at"`
	TTLSeconds  int       `json:"ttl_seconds,omitempty"`
}

// Validate checks whether a public queue claim is structurally valid.
func (c QueueClaim) Validate() error {
	switch {
	case strings.TrimSpace(c.ClaimID) == "":
		return fmt.Errorf("queue claim_id is required")
	case strings.TrimSpace(c.ItemID) == "":
		return fmt.Errorf("queue item_id is required")
	case strings.TrimSpace(c.Sender) == "":
		return fmt.Errorf("queue sender is required")
	case strings.TrimSpace(c.Queue) == "":
		return fmt.Errorf("queue name is required")
	case strings.TrimSpace(c.AgentID) == "":
		return fmt.Errorf("queue agent_id is required")
	case strings.TrimSpace(c.Claimant) == "":
		return fmt.Errorf("queue claimant address is required")
	default:
		return nil
	}
}

// TTL returns the requested claim lease duration with a safe default.
func (c QueueClaim) TTL() time.Duration {
	if c.TTLSeconds <= 0 {
		return defaultClaimTTL
	}
	return time.Duration(c.TTLSeconds) * time.Second
}

// ActorPrincipal returns the network actor that wants to claim the offer.
func (c QueueClaim) ActorPrincipal() Principal {
	return Principal{
		ID:        c.AgentID,
		Kind:      PrincipalKindNetworkAgent,
		Scope:     ScopeSky10Network,
		RouteHint: c.Claimant,
	}
}

// NewQueueClaim creates a sealed claimant request for one public offer.
func NewQueueClaim(offer QueueOffer, actor Principal, ttl time.Duration, now time.Time) (QueueClaim, error) {
	if strings.TrimSpace(offer.ItemID) == "" {
		return QueueClaim{}, fmt.Errorf("queue offer item_id is required")
	}
	if strings.TrimSpace(offer.Sender) == "" {
		return QueueClaim{}, fmt.Errorf("queue offer sender is required")
	}
	if strings.TrimSpace(offer.Queue) == "" {
		return QueueClaim{}, fmt.Errorf("queue offer queue is required")
	}
	if err := actor.Validate(); err != nil {
		return QueueClaim{}, fmt.Errorf("claim actor: %w", err)
	}
	address := actor.RouteAddress()
	if address == "" {
		return QueueClaim{}, fmt.Errorf("claim actor route address is required")
	}
	if ttl <= 0 {
		ttl = defaultClaimTTL
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return QueueClaim{
		ClaimID:     uuid.NewString(),
		ItemID:      offer.ItemID,
		RequestID:   offer.RequestID,
		Sender:      offer.Sender,
		Queue:       offer.Queue,
		Skill:       offer.Skill,
		AgentID:     actor.ID,
		Claimant:    address,
		RequestedAt: now,
		TTLSeconds:  int(ttl / time.Second),
	}, nil
}

// NetworkQueue publishes and queries public queue offers.
type NetworkQueue interface {
	PublishOffer(ctx context.Context, item Item) (QueueOffer, error)
	PublishState(ctx context.Context, item Item, status string) (QueueOffer, error)
	QueryOffers(ctx context.Context, filter QueueOfferFilter) ([]QueueOffer, error)
}

// QueueTransport stores and queries public queue offers.
type QueueTransport interface {
	Publish(ctx context.Context, signer *skykey.Key, offer QueueOffer) error
	Query(ctx context.Context, filter QueueOfferFilter) ([]QueueOffer, error)
}

// PublicQueue publishes minimal public queue offers for claimable tasks.
type PublicQueue struct {
	owner     *skykey.Key
	transport QueueTransport
	logger    *slog.Logger
	now       func() time.Time
}

// NewPublicQueue creates a public queue publisher/query helper.
func NewPublicQueue(owner *skykey.Key, transport QueueTransport, logger *slog.Logger) *PublicQueue {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(ioDiscard{}, nil))
	}
	return &PublicQueue{
		owner:     owner,
		transport: transport,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// NewNostrQueueTransport creates a Nostr-backed queue offer transport.
func NewNostrQueueTransport(relays []string, logger *slog.Logger) QueueTransport {
	return NewNostrQueueTransportWithTracker(relays, logger, nil)
}

// NewNostrQueueTransportWithTracker creates a Nostr-backed public queue
// transport with optional shared relay health tracking.
func NewNostrQueueTransportWithTracker(relays []string, logger *slog.Logger, tracker *link.NostrRelayTracker) QueueTransport {
	return &nostrQueueTransport{relays: relays, logger: logger, tracker: tracker}
}

// PublishOffer derives and publishes an open public offer for a queue mailbox
// item.
func (q *PublicQueue) PublishOffer(ctx context.Context, item Item) (QueueOffer, error) {
	return q.PublishState(ctx, item, queueOfferStatusOpen)
}

// PublishState publishes the current public state for a queue item. The latest
// event for one item wins during discovery.
func (q *PublicQueue) PublishState(ctx context.Context, item Item, status string) (QueueOffer, error) {
	if q == nil || q.owner == nil || !q.owner.IsPrivate() {
		return QueueOffer{}, fmt.Errorf("public queue owner key is required")
	}
	if q.transport == nil {
		return QueueOffer{}, fmt.Errorf("public queue transport is required")
	}
	offer, err := buildQueueOffer(item, q.owner.Address(), q.now(), status)
	if err != nil {
		return QueueOffer{}, err
	}
	if err := q.transport.Publish(ctx, q.owner, offer); err != nil {
		return QueueOffer{}, err
	}
	return offer, nil
}

// QueryOffers returns currently visible public queue offers.
func (q *PublicQueue) QueryOffers(ctx context.Context, filter QueueOfferFilter) ([]QueueOffer, error) {
	if q == nil || q.transport == nil {
		return nil, fmt.Errorf("public queue transport is required")
	}
	offers, err := q.transport.Query(ctx, filter)
	if err != nil {
		return nil, err
	}
	now := q.now()
	out := offers[:0]
	for _, offer := range offers {
		if !offer.Open() {
			continue
		}
		if !offer.ExpiresAt.IsZero() && offer.ExpiresAt.Before(now) {
			continue
		}
		out = append(out, offer)
	}
	return out, nil
}

func buildQueueOffer(item Item, sender string, now time.Time, status string) (QueueOffer, error) {
	if item.Scope() != ScopeSky10Network {
		return QueueOffer{}, fmt.Errorf("queue offer item %s must be sky10_network scoped", item.ID)
	}
	queue := item.QueueName()
	if queue == "" {
		return QueueOffer{}, fmt.Errorf("queue offer item %s is not claimable", item.ID)
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = queueOfferStatusOpen
	}
	offer := QueueOffer{
		ItemID:    item.ID,
		RequestID: item.RequestID,
		Sender:    sender,
		Queue:     queue,
		Skill:     strings.TrimSpace(item.TargetSkill),
		Status:    status,
		ExpiresAt: item.ExpiresAt,
		CreatedAt: now,
	}
	if item.Kind == ItemKindTaskRequest && len(item.PayloadInline) > 0 {
		var payload TaskRequestPayload
		if err := json.Unmarshal(item.PayloadInline, &payload); err == nil {
			offer.Method = strings.TrimSpace(payload.Method)
			offer.Summary = strings.TrimSpace(payload.Summary)
		}
	}
	return offer, nil
}

type nostrQueueTransport struct {
	relays  []string
	logger  *slog.Logger
	tracker *link.NostrRelayTracker
}

func (t *nostrQueueTransport) Publish(ctx context.Context, signer *skykey.Key, offer QueueOffer) error {
	if signer == nil || !signer.IsPrivate() {
		return fmt.Errorf("nostr signer must have a private key")
	}
	sk := link.NostrSecretKey(signer)
	pk, err := nostr.GetPublicKey(sk)
	if err != nil {
		return fmt.Errorf("deriving nostr public key: %w", err)
	}
	content, err := json.Marshal(offer)
	if err != nil {
		return fmt.Errorf("marshal queue offer %s: %w", offer.ItemID, err)
	}
	ev := nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Timestamp(offer.CreatedAt.Unix()),
		Kind:      link.Sky10NostrKind,
		Tags: nostr.Tags{
			{"d", publicQueueOfferDTag(offer.ItemID)},
			{"r", queueOfferRole},
			{"i", offer.Sender},
			{"m", offer.ItemID},
			{"q", offer.Queue},
			{"s", offer.Skill},
			{"x", offer.Status},
		},
		Content: string(content),
	}
	if err := ev.Sign(sk); err != nil {
		return fmt.Errorf("signing queue offer: %w", err)
	}

	ordered := t.orderedRelays()
	successes := 0
	for _, relay := range ordered {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("queue relay connect failed", "relay", relay, "error", err)
			continue
		}
		if err := r.Publish(ctx, ev); err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("queue relay publish failed", "relay", relay, "error", err)
			r.Close()
			continue
		}
		t.recordRelay(relay, time.Since(started), nil)
		successes++
		r.Close()
	}
	t.recordPublishOutcome("queue_offer", len(ordered), successes)
	if successes == 0 {
		return fmt.Errorf("failed to publish queue offer to any nostr relay")
	}
	return nil
}

func (t *nostrQueueTransport) Query(ctx context.Context, filter QueueOfferFilter) ([]QueueOffer, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultQueueQueryLimit
	}
	tagMap := nostr.TagMap{
		"r": []string{queueOfferRole},
	}
	if strings.TrimSpace(filter.Skill) != "" {
		tagMap["s"] = []string{strings.TrimSpace(filter.Skill)}
	}
	if strings.TrimSpace(filter.Queue) != "" {
		tagMap["q"] = []string{strings.TrimSpace(filter.Queue)}
	}
	query := nostr.Filter{
		Kinds: []int{link.Sky10NostrKind},
		Tags:  tagMap,
		Limit: limit,
	}

	byItem := make(map[string]QueueOffer)
	for _, relay := range t.orderedRelays() {
		started := time.Now()
		r, err := nostr.RelayConnect(ctx, relay)
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("queue relay connect failed", "relay", relay, "error", err)
			continue
		}
		events, err := r.QuerySync(ctx, query)
		r.Close()
		if err != nil {
			t.recordRelay(relay, time.Since(started), err)
			t.debug("queue relay query failed", "relay", relay, "error", err)
			continue
		}
		t.recordRelay(relay, time.Since(started), nil)
		for _, event := range events {
			var offer QueueOffer
			if err := json.Unmarshal([]byte(event.Content), &offer); err != nil {
				continue
			}
			if offer.ItemID == "" {
				offer.ItemID = nostrTagValue(event.Tags, "m")
			}
			if offer.Sender == "" {
				offer.Sender = nostrTagValue(event.Tags, "i")
			}
			if offer.Queue == "" {
				offer.Queue = nostrTagValue(event.Tags, "q")
			}
			if offer.Skill == "" {
				offer.Skill = nostrTagValue(event.Tags, "s")
			}
			if offer.Status == "" {
				offer.Status = nostrTagValue(event.Tags, "x")
			}
			if offer.ItemID == "" || offer.Queue == "" || offer.Sender == "" {
				continue
			}
			if offer.CreatedAt.IsZero() {
				offer.CreatedAt = event.CreatedAt.Time()
			}
			current, ok := byItem[offer.ItemID]
			if !ok || offer.CreatedAt.After(current.CreatedAt) {
				byItem[offer.ItemID] = offer
			}
		}
	}
	out := make([]QueueOffer, 0, len(byItem))
	for _, offer := range byItem {
		out = append(out, offer)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ItemID < out[j].ItemID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (t *nostrQueueTransport) debug(msg string, args ...interface{}) {
	if t.logger == nil {
		return
	}
	t.logger.Debug(msg, args...)
}

func (t *nostrQueueTransport) orderedRelays() []string {
	if t == nil {
		return nil
	}
	if t.tracker == nil {
		return append([]string(nil), t.relays...)
	}
	return t.tracker.Ordered(t.relays)
}

func (t *nostrQueueTransport) recordRelay(relay string, latency time.Duration, err error) {
	if t == nil || t.tracker == nil {
		return
	}
	t.tracker.Record(relay, latency, err)
}

func (t *nostrQueueTransport) recordPublishOutcome(operation string, attempts, successes int) {
	if t == nil || t.tracker == nil {
		return
	}
	t.tracker.RecordPublishOutcome(operation, attempts, successes, link.DefaultNostrPublishQuorum(attempts))
}

func publicQueueOfferDTag(itemID string) string {
	return "mailbox:queue:offer:" + itemID
}
