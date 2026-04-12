package agent

import (
	"context"
	"testing"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

func TestRPCQueueDiscover(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	queueTransport := newTestQueueTransport()
	relayTransport := newTestRelayTransport()

	senderRegistry := NewRegistry("D-sender", "sender", nil)
	senderHandler := newTestRPCHandler(t, senderRegistry, nil)
	senderStore := newTestMailboxStore(t)
	senderRouter := NewRouter(senderRegistry, nil, nil, senderRegistry.DeviceID(), nil)
	senderRouter.SetMailbox(senderStore)
	senderRouter.SetNetworkRelay(agentmailbox.NewRelayDropbox(senderHandler.owner, relayTransport, nil))
	senderRouter.SetNetworkQueue(agentmailbox.NewPublicQueue(senderHandler.owner, queueTransport, nil))
	senderHandler.SetMailbox(senderStore)
	senderHandler.SetRouter(senderRouter)

	workerRegistry := NewRegistry("D-worker", "worker", nil)
	workerHandler := newTestRPCHandler(t, workerRegistry, nil)
	workerStore := newTestMailboxStore(t)
	workerRouter := NewRouter(workerRegistry, nil, nil, workerRegistry.DeviceID(), nil)
	workerRouter.SetMailbox(workerStore)
	workerRouter.SetNetworkRelay(agentmailbox.NewRelayDropbox(workerHandler.owner, relayTransport, nil))
	workerRouter.SetNetworkQueue(agentmailbox.NewPublicQueue(workerHandler.owner, queueTransport, nil))
	workerHandler.SetMailbox(workerStore)
	workerHandler.SetRouter(workerRouter)

	sendRaw, err, handled := senderHandler.Dispatch(ctx, "agent.mailbox.send", mustJSON(t, map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "rpc-queue-discover",
		"idempotency_key": "rpc-queue-discover",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopeSky10Network,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method":  "research.web",
			"summary": "Investigate a target",
		},
	}))
	if !handled || err != nil {
		t.Fatalf("mailbox.send queue offer: handled=%v err=%v", handled, err)
	}
	sent := sendRaw.(map[string]interface{})["item"].(agentmailbox.Record)

	discoverRaw, err, handled := workerHandler.Dispatch(ctx, "agent.queue.discover", mustJSON(t, map[string]any{
		"skill": "research",
	}))
	if !handled || err != nil {
		t.Fatalf("queue.discover: handled=%v err=%v", handled, err)
	}
	result := discoverRaw.(map[string]interface{})
	offers := result["offers"].([]agentmailbox.QueueOffer)
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}
	if offers[0].ItemID != sent.Item.ID {
		t.Fatalf("offer item id = %q, want %q", offers[0].ItemID, sent.Item.ID)
	}
}

func TestRPCQueueClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	queueTransport := newTestQueueTransport()
	relayTransport := newTestRelayTransport()

	senderRegistry := NewRegistry("D-sender", "sender", nil)
	senderHandler := newTestRPCHandler(t, senderRegistry, nil)
	senderStore := newTestMailboxStore(t)
	senderRouter := NewRouter(senderRegistry, nil, nil, senderRegistry.DeviceID(), nil)
	senderRouter.SetMailbox(senderStore)
	senderRouter.SetNetworkRelay(agentmailbox.NewRelayDropbox(senderHandler.owner, relayTransport, nil))
	senderRouter.SetNetworkQueue(agentmailbox.NewPublicQueue(senderHandler.owner, queueTransport, nil))
	senderHandler.SetMailbox(senderStore)
	senderHandler.SetRouter(senderRouter)

	workerRegistry := NewRegistry("D-worker", "worker", nil)
	workerHandler := newTestRPCHandler(t, workerRegistry, nil)
	workerStore := newTestMailboxStore(t)
	workerRouter := NewRouter(workerRegistry, nil, nil, workerRegistry.DeviceID(), nil)
	workerRouter.SetMailbox(workerStore)
	workerRouter.SetNetworkRelay(agentmailbox.NewRelayDropbox(workerHandler.owner, relayTransport, nil))
	workerRouter.SetNetworkQueue(agentmailbox.NewPublicQueue(workerHandler.owner, queueTransport, nil))
	workerHandler.SetMailbox(workerStore)
	workerHandler.SetRouter(workerRouter)

	if _, err, handled := senderHandler.Dispatch(ctx, "agent.mailbox.send", mustJSON(t, map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "rpc-queue-claim",
		"idempotency_key": "rpc-queue-claim",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopeSky10Network,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method": "research.compare",
		},
	})); !handled || err != nil {
		t.Fatalf("mailbox.send queue task: handled=%v err=%v", handled, err)
	}

	discoverRaw, err, handled := workerHandler.Dispatch(ctx, "agent.queue.discover", mustJSON(t, map[string]any{
		"skill": "research",
	}))
	if !handled || err != nil {
		t.Fatalf("queue.discover: handled=%v err=%v", handled, err)
	}
	offers := discoverRaw.(map[string]interface{})["offers"].([]agentmailbox.QueueOffer)
	if len(offers) != 1 {
		t.Fatalf("offer count = %d, want 1", len(offers))
	}

	claimRaw, err, handled := workerHandler.Dispatch(ctx, "agent.queue.claim", mustJSON(t, map[string]any{
		"offer":       offers[0],
		"actor_id":    "agent:worker",
		"ttl_seconds": int(time.Minute / time.Second),
	}))
	if !handled || err != nil {
		t.Fatalf("queue.claim: handled=%v err=%v", handled, err)
	}
	claim := claimRaw.(map[string]interface{})["claim"].(agentmailbox.QueueClaim)
	if claim.AgentID != "agent:worker" {
		t.Fatalf("claim agent id = %q, want agent:worker", claim.AgentID)
	}
	if claim.Claimant != workerHandler.owner.Address() {
		t.Fatalf("claim claimant = %q, want %q", claim.Claimant, workerHandler.owner.Address())
	}

	if err := senderRouter.PollNetworkRelay(ctx); err != nil {
		t.Fatalf("sender poll relay: %v", err)
	}

	taskRecord, ok := senderStore.Get(offers[0].ItemID)
	if !ok {
		t.Fatalf("task %s missing after claim", offers[0].ItemID)
	}
	if taskRecord.State != agentmailbox.StateAssigned {
		t.Fatalf("task state = %s, want %s", taskRecord.State, agentmailbox.StateAssigned)
	}

	assignments := senderStore.ListReplies(taskRecord.Item.ID)
	if len(assignments) != 1 {
		t.Fatalf("assignment count = %d, want 1", len(assignments))
	}
	if assignments[0].Item.To == nil || assignments[0].Item.To.ID != "agent:worker" {
		t.Fatalf("assignment recipient = %+v, want agent:worker", assignments[0].Item.To)
	}

	if err := workerRouter.PollNetworkRelay(ctx); err != nil {
		t.Fatalf("worker poll relay: %v", err)
	}
	if _, ok := workerStore.Get(assignments[0].Item.ID); !ok {
		t.Fatalf("worker assignment %s missing after relay poll", assignments[0].Item.ID)
	}
}
