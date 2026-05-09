package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConvertResponsesRequestToChatStringInput(t *testing.T) {
	t.Parallel()

	temperature := 0.2
	chatReq, err := ConvertResponsesRequestToChat(ResponsesRequest{
		Model:           "sky10-test",
		Input:           json.RawMessage(`"hello from responses"`),
		Instructions:    "be concise",
		MaxOutputTokens: 42,
		Temperature:     &temperature,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ConvertResponsesRequestToChat() error = %v", err)
	}
	if chatReq.Model != "sky10-test" || !chatReq.Stream || chatReq.MaxTokens != 42 {
		t.Fatalf("chat request fields = %+v", chatReq)
	}
	if chatReq.Temperature == nil || *chatReq.Temperature != temperature {
		t.Fatalf("temperature = %v", chatReq.Temperature)
	}
	if len(chatReq.Messages) != 2 {
		t.Fatalf("messages = %+v", chatReq.Messages)
	}
	if chatReq.Messages[0].Role != "system" || chatReq.Messages[0].Content != "be concise" {
		t.Fatalf("system message = %+v", chatReq.Messages[0])
	}
	if chatReq.Messages[1].Role != "user" || chatReq.Messages[1].Content != "hello from responses" {
		t.Fatalf("user message = %+v", chatReq.Messages[1])
	}
}

func TestConvertResponsesRequestToChatMessageInput(t *testing.T) {
	t.Parallel()

	chatReq, err := ConvertResponsesRequestToChat(ResponsesRequest{
		Model: "sky10-test",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"text","text":"world"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prior answer"}]}
		]`),
	})
	if err != nil {
		t.Fatalf("ConvertResponsesRequestToChat() error = %v", err)
	}
	if len(chatReq.Messages) != 2 {
		t.Fatalf("messages = %+v", chatReq.Messages)
	}
	if chatReq.Messages[0].Role != "user" || chatReq.Messages[0].Content != "hello\n\nworld" {
		t.Fatalf("first message = %+v", chatReq.Messages[0])
	}
	if chatReq.Messages[1].Role != "assistant" || chatReq.Messages[1].Content != "prior answer" {
		t.Fatalf("second message = %+v", chatReq.Messages[1])
	}
}

func TestConvertResponsesRequestToChatRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()

	_, err := ConvertResponsesRequestToChat(ResponsesRequest{
		Model: "sky10-test",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}
		]`),
	})
	if err == nil {
		t.Fatal("ConvertResponsesRequestToChat() error = nil, want unsupported input error")
	}
	if !strings.Contains(err.Error(), "input_image") {
		t.Fatalf("error = %v", err)
	}
}

func TestHostResponsesNonStreaming(t *testing.T) {
	t.Parallel()

	backend := &fakeHostBackend{}
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: backend,
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"sky10-test",
		"instructions":"be concise",
		"max_output_tokens":42,
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"text","text":"world"}]}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if backend.got.Model != "sky10-test" || backend.got.MaxTokens != 42 {
		t.Fatalf("backend request = %+v", backend.got)
	}
	if len(backend.got.Messages) != 2 {
		t.Fatalf("backend messages = %+v", backend.got.Messages)
	}
	if backend.got.Messages[0].Role != "system" || backend.got.Messages[0].Content != "be concise" {
		t.Fatalf("system message = %+v", backend.got.Messages[0])
	}
	if backend.got.Messages[1].Role != "user" || backend.got.Messages[1].Content != "hello\n\nworld" {
		t.Fatalf("user message = %+v", backend.got.Messages[1])
	}

	var got ResponsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Object != "response" || got.Status != "completed" {
		t.Fatalf("response = %+v", got)
	}
	if len(got.Output) != 1 || got.Output[0].Content[0].Text != "hello from sky10" {
		t.Fatalf("output = %+v", got.Output)
	}
	if got.Usage == nil || got.Usage.InputTokens != 3 || got.Usage.OutputTokens != 4 || got.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", got.Usage)
	}
}

func TestHostResponsesStreamsSSE(t *testing.T) {
	t.Parallel()

	backend := &fakeStreamingHostBackend{}
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: backend,
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"sky10-stream",
		"stream":true,
		"input":"hello"
	}`))
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q", got)
	}
	if backend.calledChat {
		t.Fatal("non-streaming backend path was called")
	}
	if !backend.calledStream {
		t.Fatal("streaming backend path was not called")
	}
	if !backend.got.Stream || len(backend.got.Messages) != 1 || backend.got.Messages[0].Content != "hello" {
		t.Fatalf("backend request = %+v", backend.got)
	}

	events := collectResponseSSEEvents(t, rec.Body.String())
	if !hasResponseEvent(events, "response.created") {
		t.Fatalf("missing response.created event: %s", rec.Body.String())
	}
	if !hasResponseEvent(events, "response.output_text.delta") {
		t.Fatalf("missing response.output_text.delta event: %s", rec.Body.String())
	}
	if !hasResponseEvent(events, "response.completed") {
		t.Fatalf("missing response.completed event: %s", rec.Body.String())
	}
	if !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("stream body did not terminate with DONE: %s", rec.Body.String())
	}
	if got := responseDeltaText(t, events); got != "hello" {
		t.Fatalf("delta text = %q", got)
	}
}

func TestHostResponsesStreamsBackendErrorsAndDone(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: &fakeStreamingHostBackend{err: errTestResponseUpstream},
		Now:     func() time.Time { return time.Unix(123, 0) },
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"sky10-stream",
		"stream":true,
		"input":"hello"
	}`))
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, errTestResponseUpstream.Error()) {
		t.Fatalf("stream body missing error: %s", body)
	}
	if !strings.Contains(body, "event: error") {
		t.Fatalf("stream body missing error event: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream body did not terminate with DONE: %s", body)
	}
}

func TestHostResponsesStopsOnClientCancellation(t *testing.T) {
	t.Parallel()

	backend := &cancelAwareStreamingBackend{}
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: backend,
		Now:     func() time.Time { return time.Unix(123, 0) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"sky10-stream",
		"stream":true,
		"input":"hello"
	}`)).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if backend.err != context.Canceled {
		t.Fatalf("backend err = %v, want context.Canceled", backend.err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "event: response.completed") {
		t.Fatalf("stream completed after cancellation: %s", body)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream wrote DONE after cancellation: %s", body)
	}
}

func TestHostResponsesReturnsOpenAIErrorForInvalidInput(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{Backend: &fakeHostBackend{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"sky10-test",
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) || !strings.Contains(rec.Body.String(), "input_image") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

var errTestResponseUpstream = errors.New("upstream broke")

func collectResponseSSEEvents(t *testing.T, body string) []serverSentEvent {
	t.Helper()

	var events []serverSentEvent
	if err := scanServerSentEvents(context.Background(), strings.NewReader(body), func(event serverSentEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("scan events: %v", err)
	}
	return events
}

func hasResponseEvent(events []serverSentEvent, eventName string) bool {
	for _, event := range events {
		if event.Event == eventName {
			return true
		}
	}
	return false
}

func responseDeltaText(t *testing.T, events []serverSentEvent) string {
	t.Helper()

	var out strings.Builder
	for _, event := range events {
		if event.Event != "response.output_text.delta" {
			continue
		}
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			t.Fatalf("decode delta event: %v", err)
		}
		out.WriteString(payload.Delta)
	}
	return out.String()
}

type cancelAwareStreamingBackend struct {
	err error
}

func (b *cancelAwareStreamingBackend) ChatCompletions(context.Context, ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return nil, errors.New("non-streaming path should not be called")
}

func (b *cancelAwareStreamingBackend) StreamChatCompletions(ctx context.Context, _ ChatCompletionRequest, _ func(ChatCompletionStreamChunk) error) error {
	<-ctx.Done()
	b.err = ctx.Err()
	return b.err
}
