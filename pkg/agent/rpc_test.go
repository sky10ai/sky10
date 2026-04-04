package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRPCDispatchPrefix(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := NewRPCHandler(r, nil)

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
	h := NewRPCHandler(r, nil)
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
	h := NewRPCHandler(r, nil)

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

	h := NewRPCHandler(r, emit)
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

func TestRPCHeartbeat(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := NewRPCHandler(r, nil)
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
	h := NewRPCHandler(r, nil)
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
	h := NewRPCHandler(r, nil)
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
	h := NewRPCHandler(r, nil)
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
