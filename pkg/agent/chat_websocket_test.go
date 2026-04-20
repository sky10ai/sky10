package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestChatWebSocketValidatesRequests(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry()
	handler := newTestRPCHandler(t, registry, nil)
	hub := NewMessageHub()

	registerAgentForChatTest(t, handler)

	chatHandler := NewChatWebSocketHandler(registry, handler, hub, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rpc/agents/{agent}/chat", chatHandler.HandleChat)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	missingSessionResp, err := http.Get(srv.URL + "/rpc/agents/coder/chat")
	if err != nil {
		t.Fatalf("missing session request: %v", err)
	}
	defer missingSessionResp.Body.Close()
	if missingSessionResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing session status = %d, want %d", missingSessionResp.StatusCode, http.StatusBadRequest)
	}

	unknownAgentResp, err := http.Get(srv.URL + "/rpc/agents/unknown/chat?session_id=session-1")
	if err != nil {
		t.Fatalf("unknown agent request: %v", err)
	}
	defer unknownAgentResp.Body.Close()
	if unknownAgentResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent status = %d, want %d", unknownAgentResp.StatusCode, http.StatusNotFound)
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rpc/agents/coder/chat?session_id=session-1"
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var ready chatWSEvent
	if err := wsjson.Read(ctx, conn, &ready); err != nil {
		t.Fatalf("read ready event: %v", err)
	}
	if ready.Event != "session.ready" {
		t.Fatalf("ready event = %q, want session.ready", ready.Event)
	}

	tests := []struct {
		name       string
		request    chatWSRequest
		wantCode   string
		wantSubstr string
	}{
		{
			name: "bad request type",
			request: chatWSRequest{
				Type:   "event",
				ID:     "req-bad-type",
				Method: "message.send",
				Params: json.RawMessage(`{"content":{"parts":[{"type":"text","text":"hello"}]}}`),
			},
			wantCode:   "bad_request",
			wantSubstr: "type must be req",
		},
		{
			name: "unsupported method",
			request: chatWSRequest{
				Type:   "req",
				ID:     "req-bad-method",
				Method: "message.delete",
				Params: json.RawMessage(`{"content":{"parts":[{"type":"text","text":"hello"}]}}`),
			},
			wantCode:   "method_not_found",
			wantSubstr: "method must be message.send",
		},
		{
			name: "empty content",
			request: chatWSRequest{
				Type:   "req",
				ID:     "req-empty",
				Method: "message.send",
				Params: json.RawMessage(`{"content":{"parts":[]}}`),
			},
			wantCode:   "invalid_content",
			wantSubstr: "content is required",
		},
		{
			name: "invalid non-text content",
			request: chatWSRequest{
				Type:   "req",
				ID:     "req-image",
				Method: "message.send",
				Params: json.RawMessage(`{"content":{"parts":[{"type":"image","text":"hello"}]}}`),
			},
			wantCode:   "invalid_content",
			wantSubstr: "unsupported content part type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := wsjson.Write(ctx, conn, tc.request); err != nil {
				t.Fatalf("write request: %v", err)
			}
			var resp chatWSResponse
			if err := wsjson.Read(ctx, conn, &resp); err != nil {
				t.Fatalf("read response: %v", err)
			}
			if resp.Error == nil {
				t.Fatal("expected websocket error response")
			}
			if resp.Error.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", resp.Error.Code, tc.wantCode)
			}
			if !strings.Contains(resp.Error.Message, tc.wantSubstr) {
				t.Fatalf("error message = %q, want substring %q", resp.Error.Message, tc.wantSubstr)
			}
		})
	}

	if err := wsjson.Write(ctx, conn, chatWSRequest{
		Type:   "req",
		ID:     "req-ok",
		Method: "message.send",
		Params: json.RawMessage(`{"message_type":"chat","content":{"parts":[{"type":"text","text":"hello"}]}}`),
	}); err != nil {
		t.Fatalf("write valid request: %v", err)
	}
	var okResp chatWSResponse
	if err := wsjson.Read(ctx, conn, &okResp); err != nil {
		t.Fatalf("read valid response: %v", err)
	}
	if okResp.Error != nil {
		t.Fatalf("valid response error = %+v", okResp.Error)
	}
}

func TestChatWebSocketResolvesRoutedAgentLists(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry()
	handler := newTestRPCHandler(t, registry, nil)
	hub := NewMessageHub()

	chatHandler := NewChatWebSocketHandler(registry, handler, hub, nil)
	chatHandler.listAgents = func(context.Context) ([]AgentInfo, error) {
		return []AgentInfo{{
			ID:         "A-remote1234567890",
			Name:       "hermes-guest",
			DeviceID:   "D-remote1234567890",
			DeviceName: "lima-hermes-guest",
			Skills:     []string{"code"},
			Status:     "connected",
		}}, nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /rpc/agents/{agent}/chat", chatHandler.HandleChat)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/rpc/agents/hermes-guest/chat?session_id=session-remote"
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var ready chatWSEvent
	if err := wsjson.Read(ctx, conn, &ready); err != nil {
		t.Fatalf("read ready event: %v", err)
	}
	if ready.Event != "session.ready" {
		t.Fatalf("ready event = %q, want session.ready", ready.Event)
	}
}

func registerAgentForChatTest(t *testing.T, handler *RPCHandler) RegisterResult {
	t.Helper()

	params, err := json.Marshal(RegisterParams{Name: "coder", Skills: []string{"code"}})
	if err != nil {
		t.Fatalf("marshal register params: %v", err)
	}
	result, err, handled := handler.Dispatch(context.Background(), "agent.register", params)
	if !handled || err != nil {
		t.Fatalf("register: handled=%v err=%v", handled, err)
	}
	return result.(RegisterResult)
}
