//go:build integration

package integration

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

func TestIntegrationMailboxRelayDeliveryAfterRecipientRestart(t *testing.T) {
	bin := buildSky10Binary(t)
	relay := startTestNostrRelay(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"), "--nostr-relay", relay.URL())
	nodeBHome := filepath.Join(base, "node-b")
	nodeB := startProcessNode(t, bin, "node-b", nodeBHome, "--nostr-relay", relay.URL())

	infoA := identityInfo(t, nodeA.home)
	infoB := identityInfo(t, nodeB.home)
	waitForNostrSubscription(t, nodeA.home, "mailbox:"+infoA.Address)
	waitForNostrSubscription(t, nodeB.home, "mailbox:"+infoB.Address)

	nodeB.cancel()
	_ = nodeB.cmd.Wait()

	send := rpcCall[rpcMailboxRecordResult](t, nodeA.home, "agent.mailbox.send", map[string]any{
		"kind":            agentmailbox.ItemKindMessage,
		"request_id":      "integration-mailbox-relay",
		"idempotency_key": "integration-mailbox-relay",
		"to": map[string]any{
			"id":         "agent:worker-b",
			"kind":       agentmailbox.PrincipalKindNetworkAgent,
			"scope":      agentmailbox.ScopeSky10Network,
			"route_hint": infoB.Address,
		},
		"payload": map[string]any{
			"text": "relay fallback",
		},
	})

	if send.Item.Item.ID == "" {
		t.Fatal("mailbox send did not return an item id")
	}

	senderHandoff := waitForMailboxRecord(t, nodeA.home, infoA.Address, agentmailbox.PrincipalKindHuman, send.Item.Item.ID, func(result rpcMailboxRecordResult) bool {
		return result.Delivery.Status == "handed_off" && result.Delivery.LastTransport == "nostr_dropbox"
	})
	if senderHandoff.Delivery.LastEvent != agentmailbox.EventTypeHandedOff {
		t.Fatalf("sender last event = %q, want %q", senderHandoff.Delivery.LastEvent, agentmailbox.EventTypeHandedOff)
	}

	nodeB = startProcessNode(t, bin, "node-b", nodeBHome, "--nostr-relay", relay.URL())
	waitForNostrSubscription(t, nodeB.home, "mailbox:"+infoB.Address)

	received := waitForMailboxRecord(t, nodeB.home, "agent:worker-b", agentmailbox.PrincipalKindNetworkAgent, send.Item.Item.ID, func(result rpcMailboxRecordResult) bool {
		return result.Found && result.Item.Item.ID == send.Item.Item.ID
	})
	if received.Item.Item.RequestID != "integration-mailbox-relay" {
		t.Fatalf("recipient request id = %q, want integration-mailbox-relay", received.Item.Item.RequestID)
	}

	delivered := waitForMailboxRecord(t, nodeA.home, infoA.Address, agentmailbox.PrincipalKindHuman, send.Item.Item.ID, func(result rpcMailboxRecordResult) bool {
		return result.Delivery.Status == "delivered" && result.Delivery.LastTransport == "nostr_dropbox"
	})
	if delivered.Delivery.LastEvent != agentmailbox.EventTypeDelivered {
		t.Fatalf("sender delivery event = %q, want %q", delivered.Delivery.LastEvent, agentmailbox.EventTypeDelivered)
	}
}

func TestIntegrationPublicQueueCacheSurvivesRelayOutage(t *testing.T) {
	bin := buildSky10Binary(t)
	relay := startTestNostrRelay(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"), "--nostr-relay", relay.URL())
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--nostr-relay", relay.URL())
	nodeC := startProcessNode(t, bin, "node-c", filepath.Join(base, "node-c"), "--nostr-relay", relay.URL())

	waitForNostrSubscription(t, nodeB.home, "queue-offers")
	waitForNostrSubscription(t, nodeC.home, "queue-offers")

	send := rpcCall[rpcMailboxRecordResult](t, nodeA.home, "agent.mailbox.send", map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "integration-queue-cache",
		"idempotency_key": "integration-queue-cache",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopeSky10Network,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method":  "research.web",
			"summary": "warm the cache",
		},
	})

	offersB := waitForQueueOffers(t, nodeB.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})
	offersC := waitForQueueOffers(t, nodeC.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})
	if offersB.Offers[0].ItemID != send.Item.Item.ID {
		t.Fatalf("node B offer item id = %q, want %q", offersB.Offers[0].ItemID, send.Item.Item.ID)
	}
	if offersC.Offers[0].ItemID != send.Item.Item.ID {
		t.Fatalf("node C offer item id = %q, want %q", offersC.Offers[0].ItemID, send.Item.Item.ID)
	}

	relay.Close()

	cachedB := waitForQueueOffers(t, nodeB.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})
	cachedC := waitForQueueOffers(t, nodeC.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})
	if cachedB.Offers[0].ItemID != send.Item.Item.ID {
		t.Fatalf("cached node B offer item id = %q, want %q", cachedB.Offers[0].ItemID, send.Item.Item.ID)
	}
	if cachedC.Offers[0].ItemID != send.Item.Item.ID {
		t.Fatalf("cached node C offer item id = %q, want %q", cachedC.Offers[0].ItemID, send.Item.Item.ID)
	}
}

func TestIntegrationPublicQueueConcurrentClaimsOneWinner(t *testing.T) {
	bin := buildSky10Binary(t)
	relay := startTestNostrRelay(t)
	base := t.TempDir()

	nodeA := startProcessNode(t, bin, "node-a", filepath.Join(base, "node-a"), "--nostr-relay", relay.URL())
	nodeB := startProcessNode(t, bin, "node-b", filepath.Join(base, "node-b"), "--nostr-relay", relay.URL())
	nodeC := startProcessNode(t, bin, "node-c", filepath.Join(base, "node-c"), "--nostr-relay", relay.URL())

	infoA := identityInfo(t, nodeA.home)
	waitForNostrSubscription(t, nodeB.home, "queue-offers")
	waitForNostrSubscription(t, nodeC.home, "queue-offers")

	send := rpcCall[rpcMailboxRecordResult](t, nodeA.home, "agent.mailbox.send", map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "integration-queue-claims",
		"idempotency_key": "integration-queue-claims",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopeSky10Network,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method":  "research.compare",
			"summary": "pick one worker",
		},
	})

	offerB := waitForQueueOffers(t, nodeB.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})
	offerC := waitForQueueOffers(t, nodeC.home, "research", "", func(result rpcQueueDiscoverResult) bool {
		return result.Count == 1
	})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	claim := func(home, actorID string, offer agentmailbox.QueueOffer) {
		defer wg.Done()
		var result rpcQueueClaimResult
		if err := rpcCallInto(home, "agent.queue.claim", map[string]any{
			"offer":    offer,
			"actor_id": actorID,
		}, &result); err != nil {
			errs <- err
			return
		}
		if result.Claim.AgentID != actorID {
			errs <- fmt.Errorf("claim agent id = %q, want %q", result.Claim.AgentID, actorID)
		}
	}

	wg.Add(2)
	go claim(nodeB.home, "agent:worker-b", offerB.Offers[0])
	go claim(nodeC.home, "agent:worker-c", offerC.Offers[0])
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	task := waitForMailboxRecord(t, nodeA.home, infoA.Address, agentmailbox.PrincipalKindHuman, send.Item.Item.ID, func(result rpcMailboxRecordResult) bool {
		return result.Found && result.Item.State == agentmailbox.StateAssigned
	})
	if task.Item.State != agentmailbox.StateAssigned {
		t.Fatalf("task state = %s, want %s", task.Item.State, agentmailbox.StateAssigned)
	}
	var (
		assignmentID string
		winnerActor  string
	)
	for i := len(task.Item.Events) - 1; i >= 0; i-- {
		event := task.Item.Events[i]
		if event.Type != agentmailbox.EventTypeAssigned {
			continue
		}
		assignmentID = event.Meta["assignment_item_id"]
		winnerActor = event.Actor.ID
		break
	}
	if assignmentID == "" {
		t.Fatalf("assigned task missing assignment item id: %+v", task.Item.Events)
	}
	if winnerActor == "" {
		t.Fatalf("assigned task missing winner actor: %+v", task.Item.Events)
	}

	winnerHome := nodeB.home
	loserHome := nodeC.home
	loserActor := "agent:worker-c"
	if winnerActor == "agent:worker-c" {
		winnerHome, loserHome = loserHome, winnerHome
		loserActor = "agent:worker-b"
	} else if winnerActor != "agent:worker-b" {
		t.Fatalf("unexpected winner %q", winnerActor)
	}

	received := waitForMailboxRecord(t, winnerHome, winnerActor, agentmailbox.PrincipalKindNetworkAgent, assignmentID, func(result rpcMailboxRecordResult) bool {
		return result.Found && result.Item.Item.ID == assignmentID
	})
	if received.Item.Item.ReplyTo != send.Item.Item.ID {
		t.Fatalf("winner reply_to = %q, want %q", received.Item.Item.ReplyTo, send.Item.Item.ID)
	}

	ensureMailboxRecordMissing(t, loserHome, loserActor, agentmailbox.PrincipalKindNetworkAgent, assignmentID, 2*time.Second)
}
