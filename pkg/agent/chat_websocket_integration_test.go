package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestChatWebSocketHostGuestIntegration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := startChatWebSocketTestServer(t, ctx, "host", false)
	guest := startChatWebSocketTestServer(t, ctx, "guest", true)

	if host.port == guest.port {
		t.Fatalf("host and guest ports must differ: both = %d", host.port)
	}

	waitForHTTPHealth(t, host.baseURL)
	waitForHTTPHealth(t, guest.baseURL)

	postRPC(t, guest.baseURL, "agent.register", RegisterParams{
		Name:   "coder",
		Skills: []string{"code"},
	}, nil)

	hostWSURL := "ws" + strings.TrimPrefix(host.baseURL, "http") + "/rpc/agents/coder/chat?session_id=host-check"
	hostDialCtx, hostDialCancel := context.WithTimeout(context.Background(), time.Second)
	defer hostDialCancel()
	hostConn, hostResp, hostErr := websocket.Dial(hostDialCtx, hostWSURL, nil)
	if hostResp != nil && hostResp.Body != nil {
		defer hostResp.Body.Close()
	}
	if hostConn != nil {
		hostConn.Close(websocket.StatusNormalClosure, "")
	}
	if hostErr == nil {
		t.Fatal("expected host websocket dial to fail because the route is guest-only")
	}
	if hostResp != nil && hostResp.StatusCode != http.StatusNotFound {
		t.Fatalf("host websocket status = %d, want 404", hostResp.StatusCode)
	}

	sessionAConn := dialChatSession(t, guest.baseURL, "coder", "session-a")
	defer sessionAConn.Close(websocket.StatusNormalClosure, "")
	sessionBConn := dialChatSession(t, guest.baseURL, "coder", "session-b")
	defer sessionBConn.Close(websocket.StatusNormalClosure, "")

	readyA := readReadyEvent(t, sessionAConn)
	if readyA.SessionID != "session-a" {
		t.Fatalf("session A ready session_id = %q, want session-a", readyA.SessionID)
	}
	if readyA.Agent.ID == "" || readyA.Agent.Name != "coder" {
		t.Fatalf("session A ready agent = %+v, want registered coder", readyA.Agent)
	}
	readyB := readReadyEvent(t, sessionBConn)
	if readyB.SessionID != "session-b" {
		t.Fatalf("session B ready session_id = %q, want session-b", readyB.SessionID)
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer sendCancel()
	if err := wsjson.Write(sendCtx, sessionAConn, chatWSRequest{
		Type:   "req",
		ID:     "req-1",
		Method: "message.send",
		Params: json.RawMessage(`{"message_type":"chat","content":{"parts":[{"type":"text","text":"hello"},{"type":"image","filename":"diagram.png","media_type":"image/png","source":{"type":"base64","filename":"diagram.png","media_type":"image/png","data":"iVBORw0KGgo="}}]}}`),
	}); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}
	var sendResp chatWSResponse
	if err := wsjson.Read(sendCtx, sessionAConn, &sendResp); err != nil {
		t.Fatalf("read websocket response: %v", err)
	}
	if sendResp.Type != "res" {
		t.Fatalf("send response type = %q, want res", sendResp.Type)
	}
	if sendResp.Error != nil {
		t.Fatalf("send response error = %+v", sendResp.Error)
	}

	guest.hub.Publish(Message{
		ID:        "delta-1",
		SessionID: "session-a",
		Type:      "delta",
		Content:   json.RawMessage(`{"parts":[{"type":"text","text":"hel"}]}`),
		Timestamp: time.Now().UTC(),
	})
	guest.hub.Publish(Message{
		ID:        "message-1",
		SessionID: "session-a",
		Type:      "message",
		Content:   json.RawMessage(`{"parts":[{"type":"text","text":"hello"},{"type":"file","filename":"artifact.txt","media_type":"text/plain","source":{"type":"url","filename":"artifact.txt","media_type":"text/plain","url":"https://example.com/artifact.txt"}}]}`),
		Timestamp: time.Now().UTC(),
	})
	guest.hub.Publish(Message{
		ID:        "done-1",
		SessionID: "session-a",
		Type:      "done",
		Content:   json.RawMessage(`{"parts":[{"type":"text","text":"done"}]}`),
		Timestamp: time.Now().UTC(),
	})

	deltaEvent := readStreamEvent(t, sessionAConn)
	if deltaEvent.Event != "delta" {
		t.Fatalf("first stream event = %q, want delta", deltaEvent.Event)
	}
	if deltaEvent.Payload.SessionID != "session-a" {
		t.Fatalf("delta session_id = %q, want session-a", deltaEvent.Payload.SessionID)
	}

	messageEvent := readStreamEvent(t, sessionAConn)
	if messageEvent.Event != "message" {
		t.Fatalf("second stream event = %q, want message", messageEvent.Event)
	}
	if messageEvent.Payload.SessionID != "session-a" {
		t.Fatalf("message session_id = %q, want session-a", messageEvent.Payload.SessionID)
	}
	var messageContent ChatContent
	if err := json.Unmarshal(messageEvent.Payload.Content, &messageContent); err != nil {
		t.Fatalf("unmarshal message content: %v", err)
	}
	if len(messageContent.Parts) != 2 {
		t.Fatalf("message parts = %d, want 2", len(messageContent.Parts))
	}
	if got := messageContent.Parts[1].Source; got == nil || got.URL != "https://example.com/artifact.txt" {
		t.Fatalf("message file source = %+v, want artifact URL", got)
	}

	doneEvent := readStreamEvent(t, sessionAConn)
	if doneEvent.Event != "done" {
		t.Fatalf("third stream event = %q, want done", doneEvent.Event)
	}
	if doneEvent.Payload.SessionID != "session-a" {
		t.Fatalf("done session_id = %q, want session-a", doneEvent.Payload.SessionID)
	}

	assertNoStreamEvent(t, sessionBConn, 250*time.Millisecond)
}

type chatWebSocketTestServer struct {
	baseURL  string
	port     int
	server   *skyrpc.Server
	hub      *MessageHub
	registry *Registry
}

func startChatWebSocketTestServer(t *testing.T, ctx context.Context, name string, withChatRoute bool) chatWebSocketTestServer {
	t.Helper()

	port := freePort(t)
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("sky10-%s-%d.sock", strings.ReplaceAll(name, "/", "-"), time.Now().UnixNano()))
	server := skyrpc.NewServer(sockPath, "test", nil)
	registry := NewRegistry("D-"+name, name, nil)
	hub := NewMessageHub()
	emit := func(event string, data interface{}) {
		server.Emit(event, data)
		if event != "agent.message" {
			return
		}
		msg, ok := data.(Message)
		if !ok || strings.TrimSpace(msg.To) != registry.DeviceID() {
			return
		}
		hub.Publish(msg)
	}
	handler := newTestRPCHandler(t, registry, emit)
	router := NewRouter(registry, nil, emit, registry.DeviceID(), nil)
	handler.SetRouter(router)
	server.RegisterHandler(handler)
	if withChatRoute {
		server.HandleHTTP("GET /rpc/agents/{agent}/chat", NewChatWebSocketHandler(registry, handler, hub, nil).HandleChat)
	}

	rpcErrCh := make(chan error, 1)
	go func() {
		rpcErrCh <- server.Serve(ctx)
	}()

	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- server.ServeHTTP(ctx, port)
	}()
	t.Cleanup(func() {
		select {
		case err := <-rpcErrCh:
			if err != nil {
				t.Fatalf("Serve returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for RPC server shutdown")
		}
		select {
		case err := <-httpErrCh:
			if err != nil {
				t.Fatalf("ServeHTTP returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for HTTP server shutdown")
		}
	})

	return chatWebSocketTestServer{
		baseURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
		port:     port,
		server:   server,
		hub:      hub,
		registry: registry,
	}
}

func dialChatSession(t *testing.T, baseURL, agentName, sessionID string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/rpc/agents/" + agentName + "/chat?session_id=" + sessionID
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("websocket dial for %s: %v", sessionID, err)
	}
	return conn
}

type readyPayload struct {
	SessionID string `json:"session_id"`
	Agent     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
}

func readReadyEvent(t *testing.T, conn *websocket.Conn) readyPayload {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var event chatWSEvent
	if err := wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatalf("read ready event: %v", err)
	}
	if event.Event != "session.ready" {
		t.Fatalf("event = %q, want session.ready", event.Event)
	}

	body, err := json.Marshal(event.Payload)
	if err != nil {
		t.Fatalf("marshal ready payload: %v", err)
	}
	var payload readyPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal ready payload: %v", err)
	}
	return payload
}

type streamEvent struct {
	Event   string
	Payload ChatStreamMessage
}

func readStreamEvent(t *testing.T, conn *websocket.Conn) streamEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var event chatWSEvent
	if err := wsjson.Read(ctx, conn, &event); err != nil {
		t.Fatalf("read stream event: %v", err)
	}
	body, err := json.Marshal(event.Payload)
	if err != nil {
		t.Fatalf("marshal stream payload: %v", err)
	}
	var payload ChatStreamMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal stream payload: %v", err)
	}
	return streamEvent{Event: event.Event, Payload: payload}
}

func assertNoStreamEvent(t *testing.T, conn *websocket.Conn, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var event chatWSEvent
	err := wsjson.Read(ctx, conn, &event)
	if err == nil {
		t.Fatalf("unexpected stream event: %+v", event)
	}
	errText := err.Error()
	if !strings.Contains(errText, "context deadline exceeded") && !strings.Contains(errText, "use of closed network connection") {
		t.Fatalf("read error = %v, want timeout-related read error", err)
	}
}

func postRPC(t *testing.T, baseURL, method string, params interface{}, out interface{}) {
	t.Helper()

	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}

	res, err := http.Post(baseURL+"/rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", method, err)
	}
	defer res.Body.Close()

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("%s rpc error: %s", method, rpcResp.Error.Message)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		t.Fatalf("unmarshal %s result: %v", method, err)
	}
}

func waitForHTTPHealth(t *testing.T, baseURL string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get(baseURL + "/health")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTP server at %s did not become healthy", baseURL)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
