package mailbox

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestRelayDropboxHandoffAndReceiptRoundTrip(t *testing.T) {
	t.Parallel()

	alice, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	transport := newMemoryRelayTransport()
	aliceRelay := NewRelayDropbox(alice, transport, nil)
	bobRelay := NewRelayDropbox(bob, transport, nil)

	item := Item{
		ID:             "item-1",
		Kind:           ItemKindMessage,
		From:           Principal{ID: alice.Address(), Kind: PrincipalKindHuman, Scope: ScopeSky10Network, RouteHint: alice.Address()},
		To:             &Principal{ID: "agent:bob", Kind: PrincipalKindNetworkAgent, Scope: ScopeSky10Network, RouteHint: bob.Address()},
		IdempotencyKey: "item-1",
		PayloadInline:  []byte(`{"text":"secret hello"}`),
	}
	if _, err := aliceRelay.HandoffItem(context.Background(), item); err != nil {
		t.Fatal(err)
	}

	raw := transport.firstEvent(relayRecordTypeItem, bob.Address())
	if raw == nil {
		t.Fatal("expected sealed relay event for bob")
	}
	if strings.Contains(string(raw.Payload), "secret hello") {
		t.Fatal("relay payload should not expose plaintext")
	}

	inbound, err := bobRelay.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inbound) != 1 || inbound[0].Item == nil {
		t.Fatalf("inbound = %+v, want one item", inbound)
	}
	if inbound[0].Item.ID != item.ID {
		t.Fatalf("inbound item id = %s, want %s", inbound[0].Item.ID, item.ID)
	}

	if err := bobRelay.PublishDeliveryReceipt(context.Background(), alice.Address(), RelayDeliveryReceipt{
		ItemID:      item.ID,
		HandoffID:   item.ID,
		DeliveredBy: bob.Address(),
		DeliveredAt: time.Unix(1, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	aliceInbound, err := aliceRelay.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceInbound) != 1 || aliceInbound[0].Receipt == nil {
		t.Fatalf("alice inbound = %+v, want one receipt", aliceInbound)
	}
	if aliceInbound[0].Receipt.ItemID != item.ID {
		t.Fatalf("receipt item id = %s, want %s", aliceInbound[0].Receipt.ItemID, item.ID)
	}
}

type memoryRelayTransport struct {
	mu     sync.RWMutex
	events map[string]RelayTransportEvent
}

func newMemoryRelayTransport() *memoryRelayTransport {
	return &memoryRelayTransport{events: make(map[string]RelayTransportEvent)}
}

func (t *memoryRelayTransport) Publish(_ context.Context, _ *skykey.Key, event RelayTransportEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	stored := event
	stored.ID = event.DTag
	stored.Payload = append([]byte(nil), event.Payload...)
	t.events[event.DTag] = stored
	return nil
}

func (t *memoryRelayTransport) Query(_ context.Context, recipient, recordType string, limit int) ([]RelayTransportEvent, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]RelayTransportEvent, 0)
	for _, event := range t.events {
		if event.Recipient != recipient || event.RecordType != recordType {
			continue
		}
		cp := event
		cp.Payload = append([]byte(nil), event.Payload...)
		out = append(out, cp)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (t *memoryRelayTransport) firstEvent(recordType, recipient string) *RelayTransportEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, event := range t.events {
		if event.RecordType == recordType && event.Recipient == recipient {
			cp := event
			cp.Payload = append([]byte(nil), event.Payload...)
			return &cp
		}
	}
	return nil
}
