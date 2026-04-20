package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestChatWebSocketHermesBridgeStreamsResponses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := startChatWebSocketTestServer(t, ctx, "host-hermes-stream", false)
	guest := startChatWebSocketTestServer(t, ctx, "guest-hermes-stream", true)
	if host.port == guest.port {
		t.Fatalf("host and guest ports must differ: both = %d", host.port)
	}

	waitForHTTPHealth(t, host.baseURL)
	waitForHTTPHealth(t, guest.baseURL)

	hermesAPI := newFakeHermesServer(t, fakeHermesServerConfig{
		responsesChunks: []string{"hel", "lo"},
	})
	bridge := startHermesBridge(t, guest.baseURL, hermesAPI.server.URL, "hermes")

	waitForAgentRegistered(t, guest, "hermes", bridge)
	waitForBridgeSSESubscription(t, guest, bridge)

	sessionAConn := dialChatSession(t, guest.baseURL, "hermes", "session-a")
	defer sessionAConn.Close(websocket.StatusNormalClosure, "")
	sessionBConn := dialChatSession(t, guest.baseURL, "hermes", "session-b")
	defer sessionBConn.Close(websocket.StatusNormalClosure, "")
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	hubSub := guest.hub.Subscribe(hubCtx, func(msg Message) bool {
		return msg.SessionID == "session-a"
	})

	readyA := readReadyEvent(t, sessionAConn)
	if readyA.SessionID != "session-a" {
		t.Fatalf("session A ready session_id = %q, want session-a", readyA.SessionID)
	}
	readyB := readReadyEvent(t, sessionBConn)
	if readyB.SessionID != "session-b" {
		t.Fatalf("session B ready session_id = %q, want session-b", readyB.SessionID)
	}

	sendChatTextRequest(t, sessionAConn, "req-stream", "hello")
	waitForHubMessage(t, hubSub, bridge)

	deltaOne, deltaOneContent := readHermesStreamEvent(t, sessionAConn, "delta")
	if deltaOne.Payload.MessageType != "delta" {
		t.Fatalf("first delta message_type = %q, want delta", deltaOne.Payload.MessageType)
	}
	if deltaOneContent.Text != "hel" {
		t.Fatalf("first delta text = %q, want hel", deltaOneContent.Text)
	}
	if deltaOneContent.StreamID == "" {
		t.Fatal("first delta stream_id is empty")
	}
	if deltaOneContent.ClientRequestID != "req-stream" {
		t.Fatalf("first delta client_request_id = %q, want req-stream", deltaOneContent.ClientRequestID)
	}

	_, deltaTwoContent := readHermesStreamEvent(t, sessionAConn, "delta")
	if deltaTwoContent.Text != "lo" {
		t.Fatalf("second delta text = %q, want lo", deltaTwoContent.Text)
	}
	if deltaTwoContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("second delta stream_id = %q, want %q", deltaTwoContent.StreamID, deltaOneContent.StreamID)
	}
	if deltaTwoContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("second delta client_request_id = %q, want %q", deltaTwoContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	messageEvent, messageContent := readHermesStreamEvent(t, sessionAConn, "message")
	if messageEvent.Payload.MessageType != "text" {
		t.Fatalf("final message type = %q, want text", messageEvent.Payload.MessageType)
	}
	if messageContent.Text != "hello" {
		t.Fatalf("final message text = %q, want hello", messageContent.Text)
	}
	if messageContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("final message stream_id = %q, want %q", messageContent.StreamID, deltaOneContent.StreamID)
	}
	if messageContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("final message client_request_id = %q, want %q", messageContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	doneEvent, doneContent := readHermesStreamEvent(t, sessionAConn, "done")
	if doneEvent.Payload.MessageType != "done" {
		t.Fatalf("done message type = %q, want done", doneEvent.Payload.MessageType)
	}
	if doneContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("done stream_id = %q, want %q", doneContent.StreamID, deltaOneContent.StreamID)
	}
	if doneContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("done client_request_id = %q, want %q", doneContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	assertNoStreamEvent(t, sessionBConn, 250*time.Millisecond)

	stats := hermesAPI.stats()
	if stats.responsesHits == 0 {
		t.Fatal("expected Hermes /responses streaming request")
	}
	if stats.chatHits != 0 {
		t.Fatalf("chat completions hits = %d, want 0 for responses stream path", stats.chatHits)
	}
	requestBody := decodeJSONMap(t, stats.lastResponsesBody)
	if stream, _ := requestBody["stream"].(bool); !stream {
		t.Fatalf("responses request stream = %v, want true", requestBody["stream"])
	}
	if got := requestBody["conversation"]; got != "session-a" {
		t.Fatalf("responses request conversation = %v, want session-a", got)
	}
}

func TestChatWebSocketHermesBridgeFallsBackToChatCompletions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := startChatWebSocketTestServer(t, ctx, "host-hermes-fallback", false)
	guest := startChatWebSocketTestServer(t, ctx, "guest-hermes-fallback", true)
	if host.port == guest.port {
		t.Fatalf("host and guest ports must differ: both = %d", host.port)
	}

	waitForHTTPHealth(t, host.baseURL)
	waitForHTTPHealth(t, guest.baseURL)

	hermesAPI := newFakeHermesServer(t, fakeHermesServerConfig{
		responsesStatus: http.StatusNotFound,
		chatChunks:      []string{"hi", "!"},
	})
	bridge := startHermesBridge(t, guest.baseURL, hermesAPI.server.URL, "hermes-fallback")

	waitForAgentRegistered(t, guest, "hermes-fallback", bridge)
	waitForBridgeSSESubscription(t, guest, bridge)

	sessionConn := dialChatSession(t, guest.baseURL, "hermes-fallback", "session-fallback")
	defer sessionConn.Close(websocket.StatusNormalClosure, "")
	hubCtx, hubCancel := context.WithCancel(context.Background())
	defer hubCancel()
	hubSub := guest.hub.Subscribe(hubCtx, func(msg Message) bool {
		return msg.SessionID == "session-fallback"
	})

	ready := readReadyEvent(t, sessionConn)
	if ready.SessionID != "session-fallback" {
		t.Fatalf("ready session_id = %q, want session-fallback", ready.SessionID)
	}

	sendChatTextRequest(t, sessionConn, "req-fallback", "hi there")
	waitForHubMessage(t, hubSub, bridge)

	_, deltaOneContent := readHermesStreamEvent(t, sessionConn, "delta")
	if deltaOneContent.Text != "hi" {
		t.Fatalf("first fallback delta text = %q, want hi", deltaOneContent.Text)
	}
	_, deltaTwoContent := readHermesStreamEvent(t, sessionConn, "delta")
	if deltaTwoContent.Text != "!" {
		t.Fatalf("second fallback delta text = %q, want !", deltaTwoContent.Text)
	}
	if deltaTwoContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("fallback delta stream_id = %q, want %q", deltaTwoContent.StreamID, deltaOneContent.StreamID)
	}
	if deltaTwoContent.ClientRequestID != "req-fallback" {
		t.Fatalf("fallback delta client_request_id = %q, want req-fallback", deltaTwoContent.ClientRequestID)
	}

	messageEvent, messageContent := readHermesStreamEvent(t, sessionConn, "message")
	if messageEvent.Payload.MessageType != "text" {
		t.Fatalf("fallback final message type = %q, want text", messageEvent.Payload.MessageType)
	}
	if messageContent.Text != "hi!" {
		t.Fatalf("fallback final message text = %q, want hi!", messageContent.Text)
	}
	if messageContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("fallback final stream_id = %q, want %q", messageContent.StreamID, deltaOneContent.StreamID)
	}
	if messageContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("fallback final client_request_id = %q, want %q", messageContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	doneEvent, doneContent := readHermesStreamEvent(t, sessionConn, "done")
	if doneEvent.Payload.MessageType != "done" {
		t.Fatalf("fallback done message type = %q, want done", doneEvent.Payload.MessageType)
	}
	if doneContent.StreamID != deltaOneContent.StreamID {
		t.Fatalf("fallback done stream_id = %q, want %q", doneContent.StreamID, deltaOneContent.StreamID)
	}
	if doneContent.ClientRequestID != deltaOneContent.ClientRequestID {
		t.Fatalf("fallback done client_request_id = %q, want %q", doneContent.ClientRequestID, deltaOneContent.ClientRequestID)
	}

	stats := hermesAPI.stats()
	if stats.responsesHits == 0 {
		t.Fatal("expected fallback path to try /responses first")
	}
	if stats.chatHits == 0 {
		t.Fatal("expected fallback path to stream via /chat/completions")
	}
	chatBody := decodeJSONMap(t, stats.lastChatBody)
	if stream, _ := chatBody["stream"].(bool); !stream {
		t.Fatalf("chat completions request stream = %v, want true", chatBody["stream"])
	}
	if _, ok := chatBody["messages"].([]interface{}); !ok {
		t.Fatalf("chat completions request messages = %T, want []interface{}", chatBody["messages"])
	}
}

func TestChatWebSocketHermesBridgeDoesNotQueueSameSessionRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host := startChatWebSocketTestServer(t, ctx, "host-hermes-overlap", false)
	guest := startChatWebSocketTestServer(t, ctx, "guest-hermes-overlap", true)
	if host.port == guest.port {
		t.Fatalf("host and guest ports must differ: both = %d", host.port)
	}

	waitForHTTPHealth(t, host.baseURL)
	waitForHTTPHealth(t, guest.baseURL)

	firstDeltaSent := make(chan struct{})
	secondDeltaSent := make(chan struct{})
	var firstDeltaOnce sync.Once
	var secondDeltaOnce sync.Once
	hermesAPI := newFakeHermesServer(t, fakeHermesServerConfig{
		responsesHandler: func(w http.ResponseWriter, flusher http.Flusher, payload map[string]interface{}) {
			input, _ := payload["input"].(string)
			switch input {
			case "first":
				writeSSE(w, flusher, "response.output_text.delta", map[string]string{"delta": "one"})
				firstDeltaOnce.Do(func() { close(firstDeltaSent) })
				select {
				case <-secondDeltaSent:
				case <-time.After(2 * time.Second):
				}
				writeSSE(w, flusher, "response.completed", map[string]interface{}{
					"response": map[string]string{"output_text": "one"},
				})
			case "second":
				select {
				case <-firstDeltaSent:
				case <-time.After(2 * time.Second):
				}
				writeSSE(w, flusher, "response.output_text.delta", map[string]string{"delta": "two"})
				secondDeltaOnce.Do(func() { close(secondDeltaSent) })
				writeSSE(w, flusher, "response.completed", map[string]interface{}{
					"response": map[string]string{"output_text": "two"},
				})
			default:
				writeSSE(w, flusher, "response.output_text.delta", map[string]string{"delta": input})
				writeSSE(w, flusher, "response.completed", map[string]interface{}{
					"response": map[string]string{"output_text": input},
				})
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		},
	})
	bridge := startHermesBridge(t, guest.baseURL, hermesAPI.server.URL, "hermes-overlap")

	waitForAgentRegistered(t, guest, "hermes-overlap", bridge)
	waitForBridgeSSESubscription(t, guest, bridge)

	sessionConn := dialChatSession(t, guest.baseURL, "hermes-overlap", "session-overlap")
	defer sessionConn.Close(websocket.StatusNormalClosure, "")

	ready := readReadyEvent(t, sessionConn)
	if ready.SessionID != "session-overlap" {
		t.Fatalf("ready session_id = %q, want session-overlap", ready.SessionID)
	}

	sendChatTextRequest(t, sessionConn, "req-first", "first")
	sendChatTextRequest(t, sessionConn, "req-second", "second")

	clientRequestIDs := make(map[string]struct{}, 2)
	for {
		event := readStreamEvent(t, sessionConn)
		content := decodeHermesStreamContent(t, event.Payload.Content)
		switch event.Event {
		case "delta":
			if content.StreamID == "" {
				t.Fatalf("delta stream_id is empty: payload=%s", string(event.Payload.Content))
			}
			if content.ClientRequestID == "" {
				t.Fatalf("delta client_request_id is empty: payload=%s", string(event.Payload.Content))
			}
			clientRequestIDs[content.ClientRequestID] = struct{}{}
			if len(clientRequestIDs) == 2 {
				stats := hermesAPI.stats()
				if stats.responsesHits != 2 {
					t.Fatalf("responses hits = %d, want 2 overlapping requests", stats.responsesHits)
				}
				return
			}
		case "done":
			if len(clientRequestIDs) < 2 {
				t.Fatalf("same-session requests were serialized; saw %d request(s) before first done", len(clientRequestIDs))
			}
		}
	}
}

type hermesBridgeProcess struct {
	cancel context.CancelFunc
	done   chan struct{}
	logs   *lockedBuffer

	mu  sync.RWMutex
	err error
}

func startHermesBridge(t *testing.T, guestBaseURL, hermesAPIBaseURL, agentName string) *hermesBridgeProcess {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "bridge.json")
	configBody, err := json.Marshal(map[string]interface{}{
		"agent_name":     agentName,
		"agent_key_name": agentName,
		"host_rpc_url":   guestBaseURL + "/rpc",
		"skills":         []string{"code"},
	})
	if err != nil {
		t.Fatalf("marshal Hermes bridge config: %v", err)
	}
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatalf("write Hermes bridge config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	logs := &lockedBuffer{}
	cmd := exec.CommandContext(ctx, "python3", hermesBridgeScriptPath(t))
	cmd.Env = append(
		os.Environ(),
		"PYTHONUNBUFFERED=1",
		"SKY10_BRIDGE_CONFIG_PATH="+configPath,
		"HERMES_API_BASE_URL="+hermesAPIBaseURL+"/v1",
	)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start Hermes bridge: %v", err)
	}

	process := &hermesBridgeProcess{
		cancel: cancel,
		done:   make(chan struct{}),
		logs:   logs,
	}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()

	t.Cleanup(func() {
		process.stop(t)
	})

	return process
}

func (p *hermesBridgeProcess) stop(t *testing.T) {
	t.Helper()

	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Hermes bridge to stop\nlogs:\n%s", p.logs.String())
	}
}

func (p *hermesBridgeProcess) exited() (error, bool) {
	select {
	case <-p.done:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.err, true
	default:
		return nil, false
	}
}

func hermesBridgeScriptPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "sandbox", "templates", "hermes-sky10-bridge.py"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat Hermes bridge script %q: %v", path, err)
	}
	return path
}

func waitForAgentRegistered(t *testing.T, guest chatWebSocketTestServer, agentName string, bridge *hermesBridgeProcess) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if info := guest.registry.Resolve(agentName); info != nil {
			return
		}
		if err, exited := bridge.exited(); exited {
			t.Fatalf("Hermes bridge exited before registration: %v\nlogs:\n%s", err, bridge.logs.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Hermes agent %q to register\nlogs:\n%s", agentName, bridge.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForBridgeSSESubscription(t *testing.T, guest chatWebSocketTestServer, bridge *hermesBridgeProcess) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if guest.server != nil && guest.server.SubscriberCount() > 0 {
			return
		}
		if err, exited := bridge.exited(); exited {
			t.Fatalf("Hermes bridge exited before SSE subscription: %v\nlogs:\n%s", err, bridge.logs.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Hermes bridge SSE subscription\nlogs:\n%s", bridge.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForHubMessage(t *testing.T, sub <-chan Message, bridge *hermesBridgeProcess) Message {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case msg, ok := <-sub:
			if !ok {
				t.Fatalf("hub subscription closed before streamed Hermes reply\nlogs:\n%s", bridge.logs.String())
			}
			return msg
		case <-deadline.C:
			t.Fatalf("timed out waiting for Hermes reply on message hub\nlogs:\n%s", bridge.logs.String())
		case <-time.After(25 * time.Millisecond):
			if err, exited := bridge.exited(); exited {
				t.Fatalf("Hermes bridge exited before publishing reply: %v\nlogs:\n%s", err, bridge.logs.String())
			}
		}
	}
}

func sendChatTextRequest(t *testing.T, conn *websocket.Conn, requestID, text string) {
	t.Helper()

	params, err := json.Marshal(map[string]interface{}{
		"message_type": "chat",
		"content": map[string]interface{}{
			"client_request_id": requestID,
			"parts": []map[string]string{{
				"type": "text",
				"text": text,
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal websocket params: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := wsjson.Write(ctx, conn, chatWSRequest{
		Type:   "req",
		ID:     requestID,
		Method: "message.send",
		Params: params,
	}); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	var resp chatWSResponse
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read websocket response: %v", err)
	}
	if resp.Type != "res" {
		t.Fatalf("websocket response type = %q, want res", resp.Type)
	}
	if resp.Error != nil {
		t.Fatalf("websocket response error = %+v", resp.Error)
	}
}

type hermesStreamContent struct {
	Text            string `json:"text,omitempty"`
	StreamID        string `json:"stream_id,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
}

func decodeHermesStreamContent(t *testing.T, payload json.RawMessage) hermesStreamContent {
	t.Helper()

	var content hermesStreamContent
	if len(payload) == 0 {
		return content
	}
	if err := json.Unmarshal(payload, &content); err != nil {
		t.Fatalf("unmarshal stream content: %v", err)
	}
	return content
}

func readHermesStreamEvent(t *testing.T, conn *websocket.Conn, wantEvent string) (streamEvent, hermesStreamContent) {
	t.Helper()

	event := readStreamEvent(t, conn)
	if event.Event != wantEvent {
		t.Fatalf("stream event = %q, want %q, payload=%s", event.Event, wantEvent, string(event.Payload.Content))
	}

	return event, decodeHermesStreamContent(t, event.Payload.Content)
}

type fakeHermesResponsesHandler func(http.ResponseWriter, http.Flusher, map[string]interface{})

type fakeHermesServerConfig struct {
	responsesStatus  int
	responsesChunks  []string
	chatChunks       []string
	responsesHandler fakeHermesResponsesHandler
}

type fakeHermesServer struct {
	server *httptest.Server

	mu                sync.Mutex
	responsesStatus   int
	responsesChunks   []string
	chatChunks        []string
	responsesHandler  fakeHermesResponsesHandler
	responsesHits     int
	chatHits          int
	lastResponsesBody string
	lastChatBody      string
}

type fakeHermesServerStats struct {
	responsesHits     int
	chatHits          int
	lastResponsesBody string
	lastChatBody      string
}

func newFakeHermesServer(t *testing.T, cfg fakeHermesServerConfig) *fakeHermesServer {
	t.Helper()

	server := &fakeHermesServer{
		responsesStatus:  cfg.responsesStatus,
		responsesChunks:  append([]string(nil), cfg.responsesChunks...),
		chatChunks:       append([]string(nil), cfg.chatChunks...),
		responsesHandler: cfg.responsesHandler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("POST /v1/responses", server.handleResponses)
	mux.HandleFunc("POST /v1/chat/completions", server.handleChatCompletions)
	server.server = httptest.NewServer(mux)
	t.Cleanup(server.server.Close)
	return server
}

func (s *fakeHermesServer) handleResponses(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	s.mu.Lock()
	s.responsesHits++
	s.lastResponsesBody = string(body)
	status := s.responsesStatus
	chunks := append([]string(nil), s.responsesChunks...)
	handler := s.responsesHandler
	s.mu.Unlock()

	var payload map[string]interface{}
	_ = json.Unmarshal(body, &payload)

	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"responses unavailable"}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if handler != nil {
		handler(w, flusher, payload)
		return
	}

	for _, chunk := range chunks {
		writeSSE(w, flusher, "response.output_text.delta", map[string]string{"delta": chunk})
	}
	writeSSE(w, flusher, "response.completed", map[string]interface{}{
		"response": map[string]string{
			"output_text": strings.Join(chunks, ""),
		},
	})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *fakeHermesServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	s.mu.Lock()
	s.chatHits++
	s.lastChatBody = string(body)
	chunks := append([]string(nil), s.chatChunks...)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	for _, chunk := range chunks {
		writeSSE(w, flusher, "", map[string]interface{}{
			"choices": []map[string]interface{}{{
				"delta": map[string]string{
					"content": chunk,
				},
			}},
		})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *fakeHermesServer) stats() fakeHermesServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fakeHermesServerStats{
		responsesHits:     s.responsesHits,
		chatHits:          s.chatHits,
		lastResponsesBody: s.lastResponsesBody,
		lastChatBody:      s.lastChatBody,
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
	flusher.Flush()
}

func decodeJSONMap(t *testing.T, body string) map[string]interface{} {
	t.Helper()

	if strings.TrimSpace(body) == "" {
		t.Fatal("expected non-empty JSON body")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("unmarshal JSON body %q: %v", body, err)
	}
	return payload
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
