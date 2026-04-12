package mailbox

import (
	"context"
	"sync"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestPublicQueueFiltersAssignedAndExpiredOffers(t *testing.T) {
	t.Parallel()

	owner, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	transport := newMemoryQueueTransport()
	queue := NewPublicQueue(owner, transport, nil)
	queue.now = func() time.Time { return time.Unix(100, 0).UTC() }

	openItem := Item{
		ID:             "offer-open",
		Kind:           ItemKindTaskRequest,
		From:           Principal{ID: owner.Address(), Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: owner.Address()},
		TargetSkill:    "research",
		RequestID:      "request-open",
		IdempotencyKey: "request-open",
		PayloadInline:  []byte(`{"method":"research.web","summary":"open"}`),
		ExpiresAt:      time.Unix(200, 0).UTC(),
	}
	if _, err := queue.PublishOffer(context.Background(), openItem); err != nil {
		t.Fatal(err)
	}

	assignedItem := Item{
		ID:             "offer-assigned",
		Kind:           ItemKindTaskRequest,
		From:           Principal{ID: owner.Address(), Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: owner.Address()},
		TargetSkill:    "research",
		RequestID:      "request-assigned",
		IdempotencyKey: "request-assigned",
		ExpiresAt:      time.Unix(200, 0).UTC(),
	}
	if _, err := queue.PublishState(context.Background(), assignedItem, queueOfferStatusAssigned); err != nil {
		t.Fatal(err)
	}

	expiredItem := Item{
		ID:             "offer-expired",
		Kind:           ItemKindTaskRequest,
		From:           Principal{ID: owner.Address(), Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: owner.Address()},
		TargetSkill:    "research",
		RequestID:      "request-expired",
		IdempotencyKey: "request-expired",
		ExpiresAt:      time.Unix(50, 0).UTC(),
	}
	if _, err := queue.PublishOffer(context.Background(), expiredItem); err != nil {
		t.Fatal(err)
	}

	offers, err := queue.QueryOffers(context.Background(), QueueOfferFilter{Skill: "research"})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(offers))
	}
	if offers[0].ItemID != openItem.ID {
		t.Fatalf("offer item_id = %s, want %s", offers[0].ItemID, openItem.ID)
	}
	if offers[0].Method != "research.web" || offers[0].Summary != "open" {
		t.Fatalf("offer payload projection = %+v", offers[0])
	}
}

func TestNewQueueClaimRequiresRouteAddress(t *testing.T) {
	t.Parallel()

	offer := QueueOffer{
		ItemID: "offer-1",
		Sender: "sky10qsender",
		Queue:  "skill:research",
	}
	_, err := NewQueueClaim(offer, Principal{
		ID:    "agent:worker",
		Kind:  PrincipalKindNetworkAgent,
		Scope: ScopeSky10Network,
	}, time.Minute, time.Unix(1, 0).UTC())
	if err == nil {
		t.Fatal("expected route address validation error")
	}
}

func TestPublicQueueSubscriptionCachesOffers(t *testing.T) {
	t.Parallel()

	viewerKey, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	publisherKey, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	transport := newMemoryQueueTransport()
	viewer := NewPublicQueue(viewerKey, transport, nil)
	publisher := NewPublicQueue(publisherKey, transport, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = viewer.RunSubscription(ctx) }()
	waitForQueueSubscribers(t, transport, 1)

	item := Item{
		ID:             "offer-live",
		Kind:           ItemKindTaskRequest,
		From:           Principal{ID: publisherKey.Address(), Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: publisherKey.Address()},
		TargetSkill:    "research",
		RequestID:      "request-live",
		IdempotencyKey: "request-live",
		PayloadInline:  []byte(`{"method":"research.web","summary":"live"}`),
		ExpiresAt:      time.Now().Add(time.Hour).UTC(),
	}
	if _, err := publisher.PublishOffer(context.Background(), item); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		offers, err := viewer.QueryOffers(context.Background(), QueueOfferFilter{Skill: "research"})
		if err != nil {
			t.Fatal(err)
		}
		if len(offers) == 1 && offers[0].ItemID == item.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for subscribed queue offer, got %+v", offers)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := transport.queryCount(); got != 1 {
		t.Fatalf("queue query count = %d, want 1 initial prime only", got)
	}
}

type memoryQueueTransport struct {
	mu          sync.RWMutex
	offers      map[string]QueueOffer
	queries     int
	subscribers []memoryQueueSubscriber
}

func newMemoryQueueTransport() *memoryQueueTransport {
	return &memoryQueueTransport{offers: make(map[string]QueueOffer)}
}

func (t *memoryQueueTransport) Publish(_ context.Context, _ *skykey.Key, offer QueueOffer) error {
	t.mu.Lock()
	subscribers := append([]memoryQueueSubscriber(nil), t.subscribers...)
	t.offers[offer.ItemID] = offer
	t.mu.Unlock()
	for _, sub := range subscribers {
		if sub.filter.Skill != "" && sub.filter.Skill != offer.Skill {
			continue
		}
		if sub.filter.Queue != "" && sub.filter.Queue != offer.Queue {
			continue
		}
		if sub.handler != nil {
			_ = sub.handler(offer)
		}
	}
	return nil
}

func (t *memoryQueueTransport) Query(_ context.Context, filter QueueOfferFilter) ([]QueueOffer, error) {
	t.mu.Lock()
	t.queries++
	defer t.mu.Unlock()
	out := make([]QueueOffer, 0, len(t.offers))
	for _, offer := range t.offers {
		if filter.Skill != "" && offer.Skill != filter.Skill {
			continue
		}
		if filter.Queue != "" && offer.Queue != filter.Queue {
			continue
		}
		out = append(out, offer)
	}
	return out, nil
}

func (t *memoryQueueTransport) Subscribe(ctx context.Context, filter QueueOfferFilter, handler func(QueueOffer) error) error {
	if handler == nil {
		return nil
	}
	t.mu.Lock()
	t.subscribers = append(t.subscribers, memoryQueueSubscriber{
		filter:  filter,
		handler: handler,
	})
	t.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (t *memoryQueueTransport) queryCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.queries
}

func (t *memoryQueueTransport) subscriberCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.subscribers)
}

type memoryQueueSubscriber struct {
	filter  QueueOfferFilter
	handler func(QueueOffer) error
}

func waitForQueueSubscribers(t *testing.T, transport *memoryQueueTransport, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for transport.subscriberCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("queue subscribers = %d, want at least %d", transport.subscriberCount(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
