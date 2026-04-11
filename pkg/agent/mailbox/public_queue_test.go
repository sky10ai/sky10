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

type memoryQueueTransport struct {
	mu     sync.RWMutex
	offers map[string]QueueOffer
}

func newMemoryQueueTransport() *memoryQueueTransport {
	return &memoryQueueTransport{offers: make(map[string]QueueOffer)}
}

func (t *memoryQueueTransport) Publish(_ context.Context, _ *skykey.Key, offer QueueOffer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.offers[offer.ItemID] = offer
	return nil
}

func (t *memoryQueueTransport) Query(_ context.Context, filter QueueOfferFilter) ([]QueueOffer, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
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
