package agent

import (
	"context"
	"encoding/json"
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
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("status = %s, want sent", m["status"])
	}
	if m["id"] == "" {
		t.Error("message id is empty")
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
	if result.(map[string]string)["status"] != "queued" {
		t.Fatalf("status = %s, want queued", result.(map[string]string)["status"])
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

	listRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.listInbox", mustJSON(t, map[string]string{"principal_id": "human:alice"}))
	if err != nil {
		t.Fatalf("mailbox.listInbox: %v", err)
	}
	list := listRaw.(map[string]interface{})
	if list["count"].(int) != 1 {
		t.Fatalf("count = %v, want 1", list["count"])
	}

	getRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.get", mustJSON(t, map[string]string{"item_id": record.Item.ID}))
	if err != nil {
		t.Fatalf("mailbox.get: %v", err)
	}
	got := getRaw.(map[string]interface{})
	if !got["found"].(bool) {
		t.Fatal("expected mailbox item to be found")
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
	itemID := result.(map[string]string)["mailbox_item_id"]
	if itemID == "" {
		t.Fatal("expected mailbox_item_id for queued message")
	}

	if _, err := r.Register(RegisterParams{Name: "coder"}, "A-coder00000000000"); err != nil {
		t.Fatalf("register local agent: %v", err)
	}
	retryRaw, err, _ := h.Dispatch(context.Background(), "agent.mailbox.retry", mustJSON(t, map[string]string{"item_id": itemID}))
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
