package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestAgent starts a test HTTP server that responds to JSON-RPC calls.
// Returns the server and its URL.
func newTestAgent(t *testing.T, handler func(method string, params json.RawMessage) (interface{}, error)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
			ID      int64           `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result, err := handler(req.Method, req.Params)

		resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		if err != nil {
			resp["error"] = map[string]interface{}{"code": -32000, "message": err.Error()}
		} else {
			resp["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRPCDispatchPrefix(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)

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
	agent := newTestAgent(t, func(method string, _ json.RawMessage) (interface{}, error) {
		if method == "ping" {
			return map[string]string{"status": "ok"}, nil
		}
		return nil, fmt.Errorf("unknown method: %s", method)
	})

	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)
	ctx := context.Background()

	// Register.
	params, _ := json.Marshal(RegisterParams{
		Name:         "coder",
		Endpoint:     agent.URL,
		Capabilities: []string{"code"},
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

func TestRPCRegisterUnreachable(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)

	params, _ := json.Marshal(RegisterParams{
		Name:     "ghost",
		Endpoint: "http://localhost:1/rpc", // unreachable
	})
	_, err, handled := h.Dispatch(context.Background(), "agent.register", params)
	if !handled {
		t.Fatal("should be handled")
	}
	if err == nil {
		t.Fatal("expected error for unreachable agent")
	}
}

func TestRPCCallAgent(t *testing.T) {
	t.Parallel()
	agent := newTestAgent(t, func(method string, params json.RawMessage) (interface{}, error) {
		switch method {
		case "ping":
			return map[string]string{"status": "ok"}, nil
		case "search":
			return map[string]string{"answer": "42"}, nil
		default:
			return nil, fmt.Errorf("unknown: %s", method)
		}
	})

	r := newTestRegistry()
	caller := NewCaller()
	h := NewRPCHandler(r, caller, nil)
	ctx := context.Background()

	// Register agent.
	regParams, _ := json.Marshal(RegisterParams{
		Name:     "researcher",
		Endpoint: agent.URL,
		Methods:  []MethodSpec{{Name: "search"}},
	})
	result, err, _ := h.Dispatch(ctx, "agent.register", regParams)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	agentID := result.(RegisterResult).AgentID

	// Call by name.
	callParams, _ := json.Marshal(CallParams{
		Agent:  "researcher",
		Method: "search",
	})
	result, err, _ = h.Dispatch(ctx, "agent.call", callParams)
	if err != nil {
		t.Fatalf("call by name: %v", err)
	}
	cr := result.(*CallResult)
	if cr.Error != "" {
		t.Fatalf("call error: %s", cr.Error)
	}
	if cr.Result == nil {
		t.Fatal("result is nil")
	}

	// Call by ID.
	callParams, _ = json.Marshal(CallParams{
		Agent:  agentID,
		Method: "search",
	})
	result, err, _ = h.Dispatch(ctx, "agent.call", callParams)
	if err != nil {
		t.Fatalf("call by ID: %v", err)
	}
	cr = result.(*CallResult)
	if cr.Error != "" {
		t.Fatalf("call error: %s", cr.Error)
	}
}

func TestRPCCallAgentNotFound(t *testing.T) {
	t.Parallel()
	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)

	params, _ := json.Marshal(CallParams{Agent: "missing", Method: "search"})
	_, err, _ := h.Dispatch(context.Background(), "agent.call", params)
	if err != ErrAgentNotFound {
		t.Errorf("err = %v, want ErrAgentNotFound", err)
	}
}

func TestRPCDeregister(t *testing.T) {
	t.Parallel()
	agent := newTestAgent(t, func(method string, _ json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "ok"}, nil
	})

	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)
	ctx := context.Background()

	regParams, _ := json.Marshal(RegisterParams{Name: "tmp", Endpoint: agent.URL})
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

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	agent := newTestAgent(t, func(string, json.RawMessage) (interface{}, error) {
		return map[string]string{"status": "ok"}, nil
	})

	r := newTestRegistry()
	h := NewRPCHandler(r, NewCaller(), nil)
	ctx := context.Background()

	regParams, _ := json.Marshal(RegisterParams{
		Name:         "coder",
		Endpoint:     agent.URL,
		Capabilities: []string{"code", "test"},
	})
	h.Dispatch(ctx, "agent.register", regParams)

	result, err, _ := h.Dispatch(ctx, "agent.status", nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	status := result.(map[string]interface{})
	if status["agents"].(int) != 1 {
		t.Errorf("agents = %v, want 1", status["agents"])
	}
}
