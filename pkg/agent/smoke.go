package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

const (
	DefaultSmokeMessage         = "sky10 smoke test: reply with ok"
	DefaultSmokeTimeout         = 90 * time.Second
	DefaultSmokeReadyTimeout    = 15 * time.Second
	DefaultSmokeConcurrency     = 4
	defaultSmokeSessionIDPrefix = "sky10-smoke"
)

// SmokeOptions configures a chat websocket smoke test run.
type SmokeOptions struct {
	BaseURL       string
	Agents        []AgentInfo
	Message       string
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	Concurrency   int
	SessionPrefix string
}

// SmokeReport contains the result of one chat websocket smoke test run.
type SmokeReport struct {
	StartedAt time.Time          `json:"started_at"`
	Duration  time.Duration      `json:"duration"`
	Results   []SmokeAgentResult `json:"results"`
}

// OK reports whether every selected agent completed the smoke test.
func (r SmokeReport) OK() bool {
	if len(r.Results) == 0 {
		return false
	}
	for _, result := range r.Results {
		if !result.OK {
			return false
		}
	}
	return true
}

// FailureCount returns the number of failed agent smoke results.
func (r SmokeReport) FailureCount() int {
	var count int
	for _, result := range r.Results {
		if !result.OK {
			count++
		}
	}
	return count
}

// SmokeAgentResult captures the layer and timings for one agent.
type SmokeAgentResult struct {
	Agent             AgentInfo     `json:"agent"`
	SessionID         string        `json:"session_id"`
	WebSocketURL      string        `json:"websocket_url"`
	OK                bool          `json:"ok"`
	Stage             string        `json:"stage,omitempty"`
	Error             string        `json:"error,omitempty"`
	SendStatus        string        `json:"send_status,omitempty"`
	MessageID         string        `json:"message_id,omitempty"`
	ReadyLatency      time.Duration `json:"ready_latency,omitempty"`
	AckLatency        time.Duration `json:"ack_latency,omitempty"`
	FirstEventLatency time.Duration `json:"first_event_latency,omitempty"`
	FinalEventLatency time.Duration `json:"final_event_latency,omitempty"`
	FirstEvent        string        `json:"first_event,omitempty"`
	FinalEvent        string        `json:"final_event,omitempty"`
	ResponseSnippet   string        `json:"response_snippet,omitempty"`
	ClientRequestID   string        `json:"client_request_id,omitempty"`
	Elapsed           time.Duration `json:"elapsed"`
}

type smokeWSFrame struct {
	Type    string          `json:"type"`
	ID      interface{}     `json:"id,omitempty"`
	Event   string          `json:"event,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *chatWSError    `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// RunChatSmoke sends one chat websocket request to each configured agent and
// records the acknowledgement, first response event, and final response event.
func RunChatSmoke(ctx context.Context, opts SmokeOptions) (SmokeReport, error) {
	opts = normalizeSmokeOptions(opts)
	if strings.TrimSpace(opts.BaseURL) == "" {
		return SmokeReport{}, fmt.Errorf("base URL is required")
	}
	if len(opts.Agents) == 0 {
		return SmokeReport{}, fmt.Errorf("no agents selected")
	}

	started := time.Now()
	report := SmokeReport{
		StartedAt: started,
		Results:   make([]SmokeAgentResult, len(opts.Agents)),
	}

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for i := range opts.Agents {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				report.Results[i] = smokeContextFailure(opts.Agents[i], "schedule", ctx.Err())
				return
			}
			report.Results[i] = runSingleChatSmoke(ctx, opts, opts.Agents[i])
		}()
	}
	wg.Wait()
	report.Duration = time.Since(started)
	return report, nil
}

func normalizeSmokeOptions(opts SmokeOptions) SmokeOptions {
	opts.BaseURL = strings.TrimSpace(opts.BaseURL)
	opts.Message = strings.TrimSpace(opts.Message)
	if opts.Message == "" {
		opts.Message = DefaultSmokeMessage
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultSmokeTimeout
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = DefaultSmokeReadyTimeout
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultSmokeConcurrency
	}
	if opts.Concurrency > len(opts.Agents) && len(opts.Agents) > 0 {
		opts.Concurrency = len(opts.Agents)
	}
	opts.SessionPrefix = strings.TrimSpace(opts.SessionPrefix)
	if opts.SessionPrefix == "" {
		opts.SessionPrefix = defaultSmokeSessionIDPrefix
	}
	return opts
}

func smokeContextFailure(agent AgentInfo, stage string, err error) SmokeAgentResult {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return SmokeAgentResult{
		Agent: agent,
		Stage: stage,
		Error: msg,
	}
}

func runSingleChatSmoke(ctx context.Context, opts SmokeOptions, info AgentInfo) (result SmokeAgentResult) {
	start := time.Now()
	result = SmokeAgentResult{
		Agent:           info,
		SessionID:       opts.SessionPrefix + "-" + uuid.NewString(),
		ClientRequestID: opts.SessionPrefix + "-" + uuid.NewString(),
		Stage:           "connect",
	}
	defer func() {
		result.Elapsed = time.Since(start)
	}()

	target := smokeAgentPathValue(info)
	wsURL, err := ChatSmokeWebSocketURL(opts.BaseURL, target, result.SessionID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.WebSocketURL = wsURL

	readyCtx, readyCancel := context.WithTimeout(ctx, opts.ReadyTimeout)
	conn, resp, err := websocket.Dial(readyCtx, wsURL, nil)
	readyCancel()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		result.Error = smokeDialError(err, resp)
		return result
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	result.Stage = "ready"
	readyCtx, readyCancel = context.WithTimeout(ctx, opts.ReadyTimeout)
	if err := readSmokeReady(readyCtx, conn); err != nil {
		readyCancel()
		result.Error = err.Error()
		return result
	}
	readyCancel()
	result.ReadyLatency = time.Since(start)

	result.Stage = "send"
	if err := sendSmokeMessage(ctx, conn, result.ClientRequestID, opts.Message); err != nil {
		result.Error = err.Error()
		return result
	}

	responseCtx, responseCancel := context.WithTimeout(ctx, opts.Timeout)
	defer responseCancel()
	result.Stage = "ack"

	var acked bool
	var gotFirst bool
	var gotFinal bool
	for {
		if acked && gotFinal {
			result.OK = true
			result.Stage = ""
			return result
		}

		var frame smokeWSFrame
		if err := wsjson.Read(responseCtx, conn, &frame); err != nil {
			if !acked {
				result.Stage = "ack"
			} else if !gotFirst {
				result.Stage = "response"
			} else {
				result.Stage = "final"
			}
			result.Error = err.Error()
			return result
		}

		switch frame.Type {
		case "res":
			if !smokeFrameIDMatches(frame.ID, "smoke-1") {
				continue
			}
			if frame.Error != nil {
				result.Stage = "ack"
				result.Error = frame.Error.Message
				return result
			}
			acked = true
			result.AckLatency = time.Since(start)
			result.SendStatus, result.MessageID = smokeSendResult(frame.Result)
			if gotFinal {
				result.OK = true
				result.Stage = ""
				return result
			}
			result.Stage = "response"
		case "event":
			if !isSmokeChatEvent(frame.Event) {
				continue
			}
			if !gotFirst {
				gotFirst = true
				result.FirstEvent = frame.Event
				result.FirstEventLatency = time.Since(start)
				result.ResponseSnippet = smokePayloadSnippet(frame.Payload)
			}
			if frame.Event == "error" {
				result.Stage = "response"
				if snippet := smokePayloadSnippet(frame.Payload); snippet != "" {
					result.Error = snippet
				} else {
					result.Error = "agent returned error event"
				}
				return result
			}
			if frame.Event == "message" || frame.Event == "done" {
				gotFinal = true
				result.FinalEvent = frame.Event
				result.FinalEventLatency = time.Since(start)
				if snippet := smokePayloadSnippet(frame.Payload); snippet != "" {
					result.ResponseSnippet = snippet
				}
				if acked {
					result.OK = true
					result.Stage = ""
					return result
				}
			}
		}
	}
}

func smokeAgentPathValue(info AgentInfo) string {
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	return strings.TrimSpace(info.ID)
}

func smokeDialError(err error, resp *http.Response) string {
	if resp == nil {
		return err.Error()
	}
	return fmt.Sprintf("%s (HTTP %d)", err, resp.StatusCode)
}

func readSmokeReady(ctx context.Context, conn *websocket.Conn) error {
	for {
		var frame smokeWSFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		if frame.Type == "event" && frame.Event == "session.ready" {
			return nil
		}
	}
}

func sendSmokeMessage(ctx context.Context, conn *websocket.Conn, clientRequestID, text string) error {
	content := ChatContent{
		Text: text,
		Parts: []ChatContentPart{{
			Type: "text",
			Text: text,
		}},
		ClientRequestID: clientRequestID,
	}
	rawContent, err := content.Marshal()
	if err != nil {
		return err
	}
	params, err := json.Marshal(chatWSSendParams{
		MessageType: "chat",
		Content:     rawContent,
	})
	if err != nil {
		return err
	}
	return wsjson.Write(ctx, conn, chatWSRequest{
		Type:   "req",
		ID:     "smoke-1",
		Method: "message.send",
		Params: params,
	})
}

func smokeFrameIDMatches(raw interface{}, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case fmt.Stringer:
		return v.String() == want
	default:
		return fmt.Sprint(v) == want
	}
}

func isSmokeChatEvent(event string) bool {
	switch event {
	case "delta", "message", "done", "error":
		return true
	default:
		return false
	}
}

func smokeSendResult(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var sent SendResult
	if err := json.Unmarshal(raw, &sent); err != nil {
		return "", ""
	}
	return sent.Status, sent.ID
}

func smokePayloadSnippet(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload ChatStreamMessage
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Content) > 0 {
		if text := smokeContentSnippet(payload.Content); text != "" {
			return text
		}
	}
	return trimSmokeSnippet(string(raw))
}

func smokeContentSnippet(raw json.RawMessage) string {
	var withText struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &withText); err == nil {
		switch {
		case strings.TrimSpace(withText.Text) != "":
			return trimSmokeSnippet(withText.Text)
		case strings.TrimSpace(withText.Error) != "":
			return trimSmokeSnippet(withText.Error)
		}
	}
	var content ChatContent
	if err := json.Unmarshal(raw, &content); err == nil {
		content = content.Normalize()
		if strings.TrimSpace(content.Text) != "" {
			return trimSmokeSnippet(content.Text)
		}
	}
	return trimSmokeSnippet(string(raw))
}

func trimSmokeSnippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const max = 120
	if len(text) <= max {
		return text
	}
	return text[:max-3] + "..."
}

// ChatSmokeWebSocketURL returns the host websocket URL for an agent chat
// session under the daemon HTTP base URL.
func ChatSmokeWebSocketURL(baseURL, agentNameOrID, sessionID string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("base URL %q has no host", baseURL)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported base URL scheme %q", u.Scheme)
	}
	basePath := strings.TrimRight(u.Path, "/")
	escapedBasePath := strings.TrimRight(u.EscapedPath(), "/")
	u.Path = basePath + "/rpc/agents/" + agentNameOrID + "/chat"
	u.RawPath = escapedBasePath + "/rpc/agents/" + url.PathEscape(agentNameOrID) + "/chat"
	query := u.Query()
	query.Set("session_id", sessionID)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
