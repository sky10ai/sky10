package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skykey "github.com/sky10/sky10/pkg/key"
)

func newTestRPCHandler(t *testing.T, r *Registry, emit Emitter) *RPCHandler {
	t.Helper()
	owner, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate owner key: %v", err)
	}
	return NewRPCHandler(r, owner, emit)
}

func TestRPCDispatchPrefix(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)

	_, _, handled := h.Dispatch(context.Background(), "skyfs.list", nil)
	if handled {
		t.Error("non-agent method should not be handled")
	}

	_, _, handled = h.Dispatch(context.Background(), "agent.status", nil)
	if !handled {
		t.Error("agent.status should be handled")
	}
}

func TestRPCRegisterAndList(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	params, _ := json.Marshal(RegisterParams{
		Name:   "coder",
		Skills: []string{"code"},
	})
	result, err, handled := h.Dispatch(ctx, "agent.register", params)
	if !handled || err != nil {
		t.Fatalf("register: handled=%v, err=%v", handled, err)
	}
	reg := result.(RegisterResult)
	if reg.Status != "registered" {
		t.Errorf("status = %s, want registered", reg.Status)
	}
	if reg.AgentID == "" {
		t.Error("agent_id is empty")
	}
	if len(reg.AgentID) != 18 { // A- + 16
		t.Errorf("agent_id length = %d, want 18", len(reg.AgentID))
	}

	// List.
	result, err, _ = h.Dispatch(ctx, "agent.list", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	listResult := result.(map[string]interface{})
	if listResult["count"].(int) != 1 {
		t.Errorf("count = %v, want 1", listResult["count"])
	}
}

func TestRPCRegisterListsToolsAndStatusCapabilities(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	params, _ := json.Marshal(RegisterParams{
		Name: "media",
		Tools: []AgentToolSpec{{
			Name:        "media.accent.convert",
			Capability:  "media.accent.convert",
			Description: "Convert media accent.",
		}},
	})
	if _, err, handled := h.Dispatch(ctx, "agent.register", params); !handled || err != nil {
		t.Fatalf("register: handled=%v, err=%v", handled, err)
	}

	result, err, _ := h.Dispatch(ctx, "agent.list", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	agents := result.(map[string]interface{})["agents"].([]AgentInfo)
	if len(agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(agents))
	}
	if len(agents[0].Tools) != 1 || agents[0].Tools[0].Name != "media.accent.convert" {
		t.Fatalf("tools = %#v, want media accent tool", agents[0].Tools)
	}
	if !slices.Contains(agents[0].Skills, "media.accent.convert") {
		t.Fatalf("skills = %#v, want media accent capability", agents[0].Skills)
	}

	statusRaw, err, _ := h.Dispatch(ctx, "agent.status", nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	tools := statusRaw.(map[string]interface{})["tools"].([]string)
	if !slices.Contains(tools, "media.accent.convert") {
		t.Fatalf("status tools = %#v, want media accent tool", tools)
	}
}

func TestRPCRegisterMissingName(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)

	params, _ := json.Marshal(RegisterParams{})
	_, err, handled := h.Dispatch(context.Background(), "agent.register", params)
	if !handled {
		t.Fatal("should be handled")
	}
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestRPCSend(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()

	var emitted []interface{}
	emit := func(event string, data interface{}) {
		emitted = append(emitted, data)
	}

	h := newTestRPCHandler(t, r, emit)
	ctx := context.Background()

	// Register an agent so there's a local target.
	regParams, _ := json.Marshal(RegisterParams{Name: "coder"})
	h.Dispatch(ctx, "agent.register", regParams)

	// Send a message to the agent.
	sendParams, _ := json.Marshal(SendParams{
		To:        "coder",
		SessionID: "session-1",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello agent"}`),
	})
	result, err, handled := h.Dispatch(ctx, "agent.send", sendParams)
	if !handled || err != nil {
		t.Fatalf("send: handled=%v, err=%v", handled, err)
	}

	// Check we got a message ID back.
	sent := result.(SendResult)
	if sent.Status != "sent" {
		t.Errorf("status = %s, want sent", sent.Status)
	}
	if sent.ID == "" {
		t.Error("message id is empty")
	}
	if sent.Delivery.LiveTransport != "local_registry" {
		t.Fatalf("live transport = %q, want local_registry", sent.Delivery.LiveTransport)
	}
	if sent.Delivery.Status != "sent" {
		t.Fatalf("delivery status = %q, want sent", sent.Delivery.Status)
	}

	// Check SSE event was emitted (register + message = at least 2 events).
	found := false
	for _, e := range emitted {
		if msg, ok := e.(Message); ok && msg.To == "coder" {
			found = true
		}
	}
	if !found {
		t.Error("expected agent.message SSE event for coder")
	}
}

func TestRPCRegisterDrainsQueuedMailboxMessages(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	var mu sync.Mutex
	var emitted []Message
	emit := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			mu.Lock()
			emitted = append(emitted, msg)
			mu.Unlock()
		}
	}
	h := newTestRPCHandler(t, r, emit)
	router := NewRouter(r, makeTestNode(t), emit, r.DeviceID(), nil)
	router.SetMailbox(newTestMailboxStore(t))
	h.SetRouter(router)

	queued := Message{
		ID:        "msg-late-agent",
		SessionID: "session-1",
		From:      "D-deviceBB",
		To:        "coder",
		DeviceID:  r.DeviceID(),
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello later"}`),
		Timestamp: time.Now().UTC(),
	}
	result, err := router.routeIncoming(context.Background(), queued)
	if err != nil {
		t.Fatalf("queue incoming: %v", err)
	}
	queuedResult := result.(SendResult)
	if queuedResult.Status != "queued" {
		t.Fatalf("status = %s, want queued", queuedResult.Status)
	}
	if queuedResult.Delivery.DurableTransport != "private_mailbox" {
		t.Fatalf("durable transport = %q, want private_mailbox", queuedResult.Delivery.DurableTransport)
	}
	mu.Lock()
	emitted = nil
	mu.Unlock()

	regParams, _ := json.Marshal(RegisterParams{Name: "coder"})
	if _, err, handled := h.Dispatch(context.Background(), "agent.register", regParams); !handled || err != nil {
		t.Fatalf("register: handled=%v err=%v", handled, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(emitted)
		mu.Unlock()
		if count > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 1 {
		t.Fatalf("emitted %d messages after register, want 1", len(emitted))
	}
	if emitted[0].ID != queued.ID || emitted[0].To != "coder" {
		t.Fatalf("drained message = %+v, want id=%s to=coder", emitted[0], queued.ID)
	}
}

func TestRPCMailboxSendListAndGet(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	params, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindApprovalRequest,
		"request_id":      "req-approval-rpc-1",
		"idempotency_key": "approval-rpc-1",
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"action":  "approve_payment",
			"summary": "Approve payment",
		},
	})
	raw, err, handled := h.Dispatch(context.Background(), "agent.mailbox.send", params)
	if !handled || err != nil {
		t.Fatalf("mailbox.send: handled=%v err=%v", handled, err)
	}
	record := raw.(map[string]interface{})["item"].(agentmailbox.Record)
	if record.Item.Kind != agentmailbox.ItemKindApprovalRequest {
		t.Fatalf("kind = %s, want %s", record.Item.Kind, agentmailbox.ItemKindApprovalRequest)
	}
	delivery := raw.(map[string]interface{})["delivery"].(DeliveryMetadata)
	if delivery.Policy != DeliveryPolicyMailboxBacked {
		t.Fatalf("delivery policy = %q, want %q", delivery.Policy, DeliveryPolicyMailboxBacked)
	}
	if delivery.Status != "queued" {
		t.Fatalf("delivery status = %q, want queued", delivery.Status)
	}
	if delivery.DurableTransport != "private_mailbox" {
		t.Fatalf("durable transport = %q, want private_mailbox", delivery.DurableTransport)
	}

	listRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listInbox", mustJSON(t, map[string]string{"principal_id": "human:alice"}))
	if err != nil {
		t.Fatalf("mailbox.listInbox: %v", err)
	}
	list := listRaw.(map[string]interface{})
	if list["count"].(int) != 1 {
		t.Fatalf("count = %v, want 1", list["count"])
	}

	getRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.get", mustJSON(t, map[string]string{
		"item_id":        record.Item.ID,
		"principal_id":   "human:alice",
		"principal_kind": agentmailbox.PrincipalKindHuman,
	}))
	if err != nil {
		t.Fatalf("mailbox.get: %v", err)
	}
	got := getRaw.(map[string]interface{})
	if !got["found"].(bool) {
		t.Fatal("expected mailbox item to be found")
	}
	getDelivery := got["delivery"].(DeliveryMetadata)
	if getDelivery.Policy != DeliveryPolicyMailboxBacked {
		t.Fatalf("get delivery policy = %q, want %q", getDelivery.Policy, DeliveryPolicyMailboxBacked)
	}
	if getDelivery.DurableTransport != "private_mailbox" {
		t.Fatalf("get durable transport = %q, want private_mailbox", getDelivery.DurableTransport)
	}
	if getDelivery.MailboxItemID != record.Item.ID {
		t.Fatalf("get mailbox item id = %q, want %q", getDelivery.MailboxItemID, record.Item.ID)
	}
}

func TestRPCSendQueuedIncludesDeliveryMetadata(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	mailboxStore := newTestMailboxStore(t)
	h.SetMailbox(mailboxStore)
	router := NewRouter(r, makeTestNode(t), nil, r.DeviceID(), nil)
	router.SetPrivateDeviceMembership(
		func(deviceID string) bool { return deviceID == "D-deviceBB" },
		func() bool { return true },
	)
	router.SetMailbox(mailboxStore)
	h.SetRouter(router)

	sendParams, _ := json.Marshal(SendParams{
		To:        "researcher",
		DeviceID:  "D-deviceBB",
		SessionID: "session-queued",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"queue this"}`),
	})
	result, err, handled := h.Dispatch(context.Background(), "agent.send", sendParams)
	if !handled || err != nil {
		t.Fatalf("send queued: handled=%v err=%v", handled, err)
	}

	queued := result.(SendResult)
	if queued.Status != "queued" {
		t.Fatalf("status = %q, want queued", queued.Status)
	}
	if queued.MailboxItemID == "" {
		t.Fatal("expected mailbox item id")
	}
	if queued.Delivery.Policy != DeliveryPolicyMailboxBacked {
		t.Fatalf("policy = %q, want %q", queued.Delivery.Policy, DeliveryPolicyMailboxBacked)
	}
	if queued.Delivery.LiveTransport != "skylink" {
		t.Fatalf("live transport = %q, want skylink", queued.Delivery.LiveTransport)
	}
	if queued.Delivery.DurableTransport != "private_mailbox" {
		t.Fatalf("durable transport = %q, want private_mailbox", queued.Delivery.DurableTransport)
	}
	if queued.Delivery.LastEvent != agentmailbox.EventTypeDeliveryFailed {
		t.Fatalf("last event = %q, want %q", queued.Delivery.LastEvent, agentmailbox.EventTypeDeliveryFailed)
	}
}

func TestRPCSendFailsFastForNonMemberDevice(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	mailboxStore := newTestMailboxStore(t)
	h.SetMailbox(mailboxStore)
	router := NewRouter(r, makeTestNode(t), nil, r.DeviceID(), nil)
	router.SetPrivateDeviceMembership(
		func(deviceID string) bool { return deviceID == "D-deviceBB" },
		func() bool { return true },
	)
	router.SetMailbox(mailboxStore)
	h.SetRouter(router)

	sendParams, _ := json.Marshal(SendParams{
		To:        "researcher",
		DeviceID:  "D-missing0",
		SessionID: "session-non-member",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"do not queue this"}`),
	})
	_, err, handled := h.Dispatch(context.Background(), "agent.send", sendParams)
	if !handled {
		t.Fatal("expected agent.send to be handled")
	}
	if err == nil {
		t.Fatal("expected non-member device send to fail")
	}
	if !strings.Contains(err.Error(), "not part of this identity") {
		t.Fatalf("err = %v, want non-member identity error", err)
	}
	if outbox := mailboxStore.ListOutbox(r.DeviceID()); len(outbox) != 0 {
		t.Fatalf("outbox len = %d, want 0", len(outbox))
	}
}

func TestRPCStatusWithMailboxShowsMailboxBackedAgentSendPolicy(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	mailboxStore := newTestMailboxStore(t)
	h.SetMailbox(mailboxStore)
	router := NewRouter(r, makeTestNode(t), nil, r.DeviceID(), nil)
	router.SetMailbox(mailboxStore)
	h.SetRouter(router)

	result, err, _ := h.Dispatch(context.Background(), "agent.status", nil)
	if err != nil {
		t.Fatalf("status with mailbox: %v", err)
	}
	status := result.(map[string]interface{})
	policies := status["delivery_policies"].(map[string]DeliveryPolicyDescription)
	if policies["agent_send"].Policy != DeliveryPolicyMailboxBacked {
		t.Fatalf("agent_send policy = %q, want %q", policies["agent_send"].Policy, DeliveryPolicyMailboxBacked)
	}
	if policies["agent_send"].DurableTransport != "private_mailbox" {
		t.Fatalf("agent_send durable transport = %q, want private_mailbox", policies["agent_send"].DurableTransport)
	}
}

func TestRPCMailboxViewsIncludeOwnerAndRegisteredAgents(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)

	if _, err := r.Register(RegisterParams{Name: "researcher", Skills: []string{"research"}}, "A-research0000000"); err != nil {
		t.Fatal(err)
	}

	raw, err, handled := h.Dispatch(context.Background(), "agent.mailbox.views", nil)
	if !handled || err != nil {
		t.Fatalf("mailbox.views: handled=%v err=%v", handled, err)
	}
	result := raw.(map[string]interface{})
	if result["count"].(int) != 2 {
		t.Fatalf("count = %v, want 2", result["count"])
	}
	views := result["views"].([]mailboxView)
	if views[0].Role != mailboxViewRoleHuman {
		t.Fatalf("default role = %s, want %s", views[0].Role, mailboxViewRoleHuman)
	}
	if views[1].Principal.ID != "A-research0000000" {
		t.Fatalf("agent view principal = %s, want A-research0000000", views[1].Principal.ID)
	}
}

func TestRPCMailboxGetHidesUnrelatedPrincipal(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	params, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindMessage,
		"idempotency_key": "msg-private-1",
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"text": "private",
		},
	})
	raw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", params)
	if err != nil {
		t.Fatal(err)
	}
	record := raw.(map[string]interface{})["item"].(agentmailbox.Record)

	getRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.get", mustJSON(t, map[string]string{
		"item_id":        record.Item.ID,
		"principal_id":   "human:bob",
		"principal_kind": agentmailbox.PrincipalKindHuman,
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := getRaw.(map[string]interface{})
	if got["found"].(bool) {
		t.Fatal("unrelated principal should not see mailbox item")
	}
}

func TestRPCMailboxListInboxFiltersByRequestAndReply(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	rootSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindMessage,
		"request_id":      "req-filter-root",
		"idempotency_key": "msg-filter-root",
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"text": "root",
		},
	})
	rootRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", rootSend)
	if err != nil {
		t.Fatal(err)
	}
	root := rootRaw.(map[string]interface{})["item"].(agentmailbox.Record)

	replySend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindMessage,
		"request_id":      "req-filter-reply",
		"idempotency_key": "msg-filter-reply",
		"reply_to":        root.Item.ID,
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"text": "reply",
		},
	})
	if _, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", replySend); err != nil {
		t.Fatal(err)
	}

	requestListRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listInbox", mustJSON(t, map[string]string{
		"principal_id":   "human:alice",
		"principal_kind": agentmailbox.PrincipalKindHuman,
		"request_id":     "req-filter-root",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if requestListRaw.(map[string]interface{})["count"].(int) != 1 {
		t.Fatalf("request filter count = %v, want 1", requestListRaw.(map[string]interface{})["count"])
	}

	replyListRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listInbox", mustJSON(t, map[string]string{
		"principal_id":   "human:alice",
		"principal_kind": agentmailbox.PrincipalKindHuman,
		"reply_to":       root.Item.ID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	items := replyListRaw.(map[string]interface{})["items"].([]agentmailbox.Record)
	if len(items) != 1 {
		t.Fatalf("reply filter count = %d, want 1", len(items))
	}
	if items[0].Item.ReplyTo != root.Item.ID {
		t.Fatalf("reply_to = %s, want %s", items[0].Item.ReplyTo, root.Item.ID)
	}
}

func TestRPCMailboxApproveAndClaim(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	approvalSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindApprovalRequest,
		"request_id":      "req-approval-rpc-2",
		"idempotency_key": "approval-rpc-2",
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"action":  "approve_payment",
			"summary": "Approve payment",
		},
	})
	raw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", approvalSend)
	if err != nil {
		t.Fatalf("mailbox.send approval: %v", err)
	}
	approval := raw.(map[string]interface{})["item"].(agentmailbox.Record)

	approveRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.approve", mustJSON(t, map[string]string{"item_id": approval.Item.ID, "actor_id": "human:alice"}))
	if err != nil {
		t.Fatalf("mailbox.approve: %v", err)
	}
	approved := approveRaw.(map[string]interface{})["item"].(agentmailbox.Record)
	if approved.State != agentmailbox.StateApproved {
		t.Fatalf("state = %s, want %s", approved.State, agentmailbox.StateApproved)
	}

	taskSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "req-task-rpc-1",
		"idempotency_key": "task-rpc-1",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method":  "research",
			"summary": "deep query",
		},
	})
	raw, err, _ = h.Dispatch(context.Background(), "agent.mailbox.send", taskSend)
	if err != nil {
		t.Fatalf("mailbox.send task: %v", err)
	}
	task := raw.(map[string]interface{})["item"].(agentmailbox.Record)

	claimRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.claim", mustJSON(t, map[string]any{"item_id": task.Item.ID, "actor_id": "agent:worker", "actor_kind": agentmailbox.PrincipalKindLocalAgent}))
	if err != nil {
		t.Fatalf("mailbox.claim: %v", err)
	}
	claimed := claimRaw.(map[string]interface{})["item"].(agentmailbox.Record)
	if claimed.State != agentmailbox.StateClaimed {
		t.Fatalf("claim state = %s, want %s", claimed.State, agentmailbox.StateClaimed)
	}
}

func TestRPCMailboxListQueueScopedToAgentSkills(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	if _, err := r.Register(RegisterParams{Name: "researcher", Skills: []string{"research"}}, "A-research0100000"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-coder010000000"); err != nil {
		t.Fatal(err)
	}

	taskSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "req-task-rpc-queue-scope",
		"idempotency_key": "task-rpc-queue-scope",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method": "research",
		},
	})
	if _, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", taskSend); err != nil {
		t.Fatal(err)
	}

	researchRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listQueue", mustJSON(t, map[string]string{
		"principal_id":   "A-research0100000",
		"principal_kind": agentmailbox.PrincipalKindLocalAgent,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if researchRaw.(map[string]interface{})["count"].(int) != 1 {
		t.Fatalf("research queue count = %v, want 1", researchRaw.(map[string]interface{})["count"])
	}

	coderRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listQueue", mustJSON(t, map[string]string{
		"principal_id":   "A-coder010000000",
		"principal_kind": agentmailbox.PrincipalKindLocalAgent,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if coderRaw.(map[string]interface{})["count"].(int) != 0 {
		t.Fatalf("coder queue count = %v, want 0", coderRaw.(map[string]interface{})["count"])
	}
}

func TestRPCMailboxApproveRejectsWrongRecipient(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	approvalSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindApprovalRequest,
		"request_id":      "req-approval-rpc-auth",
		"idempotency_key": "approval-rpc-auth",
		"to": map[string]any{
			"id":    "human:alice",
			"kind":  agentmailbox.PrincipalKindHuman,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"payload": map[string]any{
			"action":  "approve_payment",
			"summary": "Approve payment",
		},
	})
	raw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", approvalSend)
	if err != nil {
		t.Fatal(err)
	}
	approval := raw.(map[string]interface{})["item"].(agentmailbox.Record)

	if _, err, _ := h.Dispatch(context.Background(), "agent.mailbox.approve", mustJSON(t, map[string]string{
		"item_id":     approval.Item.ID,
		"actor_id":    "human:bob",
		"actor_kind":  agentmailbox.PrincipalKindHuman,
		"decision_id": "reject-me",
	})); err == nil {
		t.Fatal("wrong recipient should not be able to approve")
	}
}

func TestRPCMailboxClaimRejectsHumanActor(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	h.SetMailbox(newTestMailboxStore(t))

	taskSend, _ := json.Marshal(map[string]any{
		"kind":            agentmailbox.ItemKindTaskRequest,
		"request_id":      "req-task-rpc-human-claim",
		"idempotency_key": "task-rpc-human-claim",
		"to": map[string]any{
			"id":    "queue:research",
			"kind":  agentmailbox.PrincipalKindCapabilityQueue,
			"scope": agentmailbox.ScopePrivateNetwork,
		},
		"target_skill": "research",
		"payload": map[string]any{
			"method": "research",
		},
	})
	raw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.send", taskSend)
	if err != nil {
		t.Fatal(err)
	}
	task := raw.(map[string]interface{})["item"].(agentmailbox.Record)

	if _, err, _ := h.Dispatch(context.Background(), "agent.mailbox.claim", mustJSON(t, map[string]any{
		"item_id":    task.Item.ID,
		"actor_id":   "human:alice",
		"actor_kind": agentmailbox.PrincipalKindHuman,
	})); err == nil {
		t.Fatal("human actor should not be able to claim queue work")
	}
}

func TestRPCMailboxRetryDrainsQueuedLocalMessage(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	var (
		mu      sync.Mutex
		emitted []Message
	)
	emit := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			mu.Lock()
			emitted = append(emitted, msg)
			mu.Unlock()
		}
	}

	h := newTestRPCHandler(t, r, emit)
	mailboxStore := newTestMailboxStore(t)
	h.SetMailbox(mailboxStore)
	router := NewRouter(r, makeTestNode(t), emit, r.DeviceID(), nil)
	router.SetMailbox(mailboxStore)
	h.SetRouter(router)

	queued := Message{
		ID:        "msg-retry-rpc",
		SessionID: "session-1",
		From:      "D-deviceBB",
		To:        "coder",
		DeviceID:  r.DeviceID(),
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello later"}`),
		Timestamp: time.Now().UTC(),
	}
	result, err := router.routeIncoming(context.Background(), queued)
	if err != nil {
		t.Fatalf("queue incoming: %v", err)
	}
	itemID := result.(SendResult).MailboxItemID
	if itemID == "" {
		t.Fatal("expected mailbox_item_id for queued message")
	}

	if _, err := r.Register(RegisterParams{Name: "coder"}, "A-coder00000000000"); err != nil {
		t.Fatalf("register local agent: %v", err)
	}
	retryRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.retry", mustJSON(t, map[string]any{
		"item_id":    itemID,
		"actor_id":   "A-coder00000000000",
		"actor_kind": agentmailbox.PrincipalKindLocalAgent,
	}))
	if err != nil {
		t.Fatalf("mailbox.retry: %v", err)
	}
	retried := retryRaw.(map[string]interface{})["item"].(agentmailbox.Record)
	if retried.State != agentmailbox.StateDelivered {
		t.Fatalf("retry state = %s, want %s", retried.State, agentmailbox.StateDelivered)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 2 {
		t.Fatalf("emitted %d messages, want 2 (queue + retry delivery)", len(emitted))
	}
	if emitted[len(emitted)-1].ID != queued.ID {
		t.Fatalf("delivered message id = %s, want %s", emitted[len(emitted)-1].ID, queued.ID)
	}
}

func TestRPCMailboxRetryReplaysDeadLetteredLocalMessage(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	mailboxStore := newTestMailboxStore(t)
	h.SetMailbox(mailboxStore)
	router := NewRouter(r, makeTestNode(t), nil, r.DeviceID(), nil)
	router.SetMailbox(mailboxStore)
	h.SetRouter(router)

	queued := Message{
		ID:        "msg-replay-rpc",
		SessionID: "session-replay",
		From:      "D-deviceBB",
		To:        "coder",
		DeviceID:  r.DeviceID(),
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello replay"}`),
		Timestamp: time.Now().UTC(),
	}
	result, err := router.routeIncoming(context.Background(), queued)
	if err != nil {
		t.Fatalf("queue incoming: %v", err)
	}
	itemID := result.(SendResult).MailboxItemID
	if itemID == "" {
		t.Fatal("expected mailbox_item_id for queued message")
	}
	if _, err := mailboxStore.AppendEvent(context.Background(), agentmailbox.Event{
		ItemID: itemID,
		Type:   agentmailbox.EventTypeDeadLettered,
		Actor: agentmailbox.Principal{
			ID:    "human:alice",
			Kind:  agentmailbox.PrincipalKindHuman,
			Scope: agentmailbox.ScopePrivateNetwork,
		},
	}); err != nil {
		t.Fatalf("dead-letter item: %v", err)
	}

	if _, err := r.Register(RegisterParams{Name: "coder"}, "A-coder00000000000"); err != nil {
		t.Fatalf("register local agent: %v", err)
	}
	retryRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.retry", mustJSON(t, map[string]any{
		"item_id":    itemID,
		"actor_id":   "A-coder00000000000",
		"actor_kind": agentmailbox.PrincipalKindLocalAgent,
	}))
	if err != nil {
		t.Fatalf("mailbox.retry replay: %v", err)
	}
	replayed := retryRaw.(map[string]interface{})["item"].(agentmailbox.Record)
	if replayed.State != agentmailbox.StateDelivered {
		t.Fatalf("retry state = %s, want %s", replayed.State, agentmailbox.StateDelivered)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRPCHeartbeat(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	// Register.
	regParams, _ := json.Marshal(RegisterParams{Name: "coder"})
	result, _, _ := h.Dispatch(ctx, "agent.register", regParams)
	agentID := result.(RegisterResult).AgentID

	// Heartbeat.
	hbParams, _ := json.Marshal(map[string]string{"agent_id": agentID})
	_, err, handled := h.Dispatch(ctx, "agent.heartbeat", hbParams)
	if !handled || err != nil {
		t.Fatalf("heartbeat: handled=%v, err=%v", handled, err)
	}

	// Heartbeat for missing agent.
	hbParams, _ = json.Marshal(map[string]string{"agent_id": "A-missing000000000"})
	_, err, _ = h.Dispatch(ctx, "agent.heartbeat", hbParams)
	if err != ErrAgentNotFound {
		t.Errorf("heartbeat missing: err = %v, want ErrAgentNotFound", err)
	}
}

func TestRPCDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	regParams, _ := json.Marshal(RegisterParams{Name: "tmp"})
	result, _, _ := h.Dispatch(ctx, "agent.register", regParams)
	agentID := result.(RegisterResult).AgentID

	deregParams, _ := json.Marshal(DeregisterParams{AgentID: agentID})
	_, err, _ := h.Dispatch(ctx, "agent.deregister", deregParams)
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if r.Len() != 0 {
		t.Errorf("registry len = %d, want 0", r.Len())
	}
}

func TestRPCDiscover(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	r.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-coder00000000000")
	r.Register(RegisterParams{Name: "tester", Skills: []string{"test"}}, "A-tester0000000000")

	params, _ := json.Marshal(map[string]string{"skill": "code"})
	result, err, _ := h.Dispatch(ctx, "agent.discover", params)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	disc := result.(map[string]interface{})
	if disc["count"].(int) != 1 {
		t.Errorf("discover count = %v, want 1", disc["count"])
	}
}

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	r.Register(RegisterParams{
		Name:   "coder",
		Skills: []string{"code", "test"},
	}, "A-coder00000000000")

	result, err, _ := h.Dispatch(ctx, "agent.status", nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	status := result.(map[string]interface{})
	if status["agents"].(int) != 1 {
		t.Errorf("agents = %v, want 1", status["agents"])
	}
	policies := status["delivery_policies"].(map[string]DeliveryPolicyDescription)
	if policies["agent_send"].Policy != DeliveryPolicyLiveOnly {
		t.Fatalf("agent_send policy = %q, want %q", policies["agent_send"].Policy, DeliveryPolicyLiveOnly)
	}
}

func TestRPCRegisterDeterministicAfterDeregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	params, _ := json.Marshal(RegisterParams{
		Name:    "Claude Code",
		KeyName: "claude-code",
		Skills:  []string{"code"},
	})
	firstRaw, err, handled := h.Dispatch(ctx, "agent.register", params)
	if !handled || err != nil {
		t.Fatalf("first register: handled=%v, err=%v", handled, err)
	}
	first := firstRaw.(RegisterResult)

	deregParams, _ := json.Marshal(DeregisterParams{AgentID: first.AgentID})
	if _, err, _ := h.Dispatch(ctx, "agent.deregister", deregParams); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	secondRaw, err, handled := h.Dispatch(ctx, "agent.register", params)
	if !handled || err != nil {
		t.Fatalf("second register: handled=%v, err=%v", handled, err)
	}
	second := secondRaw.(RegisterResult)

	if first.AgentID != second.AgentID {
		t.Fatalf("agent ID changed after deregister/re-register: %s != %s", first.AgentID, second.AgentID)
	}
}

func TestRPCRegisterUsesKeyNameForStableIdentityAcrossRename(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := newTestRPCHandler(t, r, nil)
	ctx := context.Background()

	firstParams, _ := json.Marshal(RegisterParams{
		Name:    "Claude Code",
		KeyName: "claude-code",
	})
	firstRaw, err, handled := h.Dispatch(ctx, "agent.register", firstParams)
	if !handled || err != nil {
		t.Fatalf("first register: handled=%v, err=%v", handled, err)
	}
	first := firstRaw.(RegisterResult)

	deregParams, _ := json.Marshal(DeregisterParams{AgentID: first.AgentID})
	if _, err, _ := h.Dispatch(ctx, "agent.deregister", deregParams); err != nil {
		t.Fatalf("deregister: %v", err)
	}

	secondParams, _ := json.Marshal(RegisterParams{
		Name:    "Claude",
		KeyName: "claude-code",
	})
	secondRaw, err, handled := h.Dispatch(ctx, "agent.register", secondParams)
	if !handled || err != nil {
		t.Fatalf("second register: handled=%v, err=%v", handled, err)
	}
	second := secondRaw.(RegisterResult)

	if first.AgentID != second.AgentID {
		t.Fatalf("agent ID changed across display-name rename: %s != %s", first.AgentID, second.AgentID)
	}
}
