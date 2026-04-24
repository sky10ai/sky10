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

func TestRunChatSmokeReportsAckFirstAndFinalEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var gotPath string
	var gotRequest chatWSRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := wsjson.Write(ctx, conn, chatWSEvent{Type: "event", Event: "session.ready"}); err != nil {
			t.Errorf("write ready: %v", err)
			return
		}
		if err := wsjson.Read(ctx, conn, &gotRequest); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := wsjson.Write(ctx, conn, chatWSResponse{
			Type: "res",
			ID:   gotRequest.ID,
			Result: SendResult{
				ID:     "M-smoke",
				Status: "sent",
			},
		}); err != nil {
			t.Errorf("write response: %v", err)
			return
		}
		writeSmokeEvent(t, ctx, conn, "delta", `{"text":"o","stream_id":"s1"}`)
		writeSmokeEvent(t, ctx, conn, "message", `{"text":"ok","stream_id":"s1"}`)
	}))
	defer server.Close()

	report, err := RunChatSmoke(ctx, SmokeOptions{
		BaseURL:       server.URL,
		Agents:        []AgentInfo{{ID: "A-coder", Name: "coder", DeviceID: "D-local"}},
		Message:       "ping",
		Timeout:       time.Second,
		ReadyTimeout:  time.Second,
		SessionPrefix: "test-smoke",
		Concurrency:   1,
	})
	if err != nil {
		t.Fatalf("RunChatSmoke: %v", err)
	}
	if !report.OK() {
		t.Fatalf("report OK = false: %+v", report.Results)
	}
	if len(report.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(report.Results))
	}
	result := report.Results[0]
	if !result.OK {
		t.Fatalf("result OK = false: %+v", result)
	}
	if gotPath != "/rpc/agents/coder/chat" {
		t.Fatalf("websocket path = %q, want /rpc/agents/coder/chat", gotPath)
	}
	if result.FirstEvent != "delta" || result.FinalEvent != "message" {
		t.Fatalf("events = %q/%q, want delta/message", result.FirstEvent, result.FinalEvent)
	}
	if result.SendStatus != "sent" || result.MessageID != "M-smoke" {
		t.Fatalf("send result = %q/%q, want sent/M-smoke", result.SendStatus, result.MessageID)
	}
	if result.ResponseSnippet != "ok" {
		t.Fatalf("response snippet = %q, want ok", result.ResponseSnippet)
	}

	var params chatWSSendParams
	if err := json.Unmarshal(gotRequest.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	var content ChatContent
	if err := json.Unmarshal(params.Content, &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content.Text != "ping" {
		t.Fatalf("content text = %q, want ping", content.Text)
	}
	if !strings.HasPrefix(content.ClientRequestID, "test-smoke-") {
		t.Fatalf("client request id = %q, want test-smoke prefix", content.ClientRequestID)
	}
}

func TestRunChatSmokeRecordsAgentErrorEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		if err := wsjson.Write(ctx, conn, chatWSEvent{Type: "event", Event: "session.ready"}); err != nil {
			t.Errorf("write ready: %v", err)
			return
		}
		var req chatWSRequest
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := wsjson.Write(ctx, conn, chatWSResponse{
			Type:   "res",
			ID:     req.ID,
			Result: SendResult{ID: "M-error", Status: "sent"},
		}); err != nil {
			t.Errorf("write response: %v", err)
			return
		}
		writeSmokeEvent(t, ctx, conn, "error", `{"error":"model timed out"}`)
	}))
	defer server.Close()

	report, err := RunChatSmoke(ctx, SmokeOptions{
		BaseURL:      server.URL,
		Agents:       []AgentInfo{{ID: "A-coder", Name: "coder"}},
		Timeout:      time.Second,
		ReadyTimeout: time.Second,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatalf("RunChatSmoke: %v", err)
	}
	if report.OK() {
		t.Fatalf("report OK = true, want false")
	}
	result := report.Results[0]
	if result.OK {
		t.Fatalf("result OK = true, want false")
	}
	if result.Stage != "response" {
		t.Fatalf("stage = %q, want response", result.Stage)
	}
	if result.Error != "model timed out" {
		t.Fatalf("error = %q, want model timed out", result.Error)
	}
}

func TestChatSmokeWebSocketURL(t *testing.T) {
	got, err := ChatSmokeWebSocketURL("http://127.0.0.1:9101", "agent/name", "session 1")
	if err != nil {
		t.Fatalf("ChatSmokeWebSocketURL: %v", err)
	}
	want := "ws://127.0.0.1:9101/rpc/agents/agent%2Fname/chat?session_id=session+1"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func writeSmokeEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, event string, content string) {
	t.Helper()
	payload := ChatStreamMessage{
		SessionID:   "session",
		MessageType: event,
		Content:     json.RawMessage(content),
	}
	if err := wsjson.Write(ctx, conn, chatWSEvent{
		Type:    "event",
		Event:   event,
		Payload: payload,
	}); err != nil {
		t.Fatalf("write %s event: %v", event, err)
	}
}
