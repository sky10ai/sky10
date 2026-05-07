package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeHostBackend struct {
	got ChatCompletionRequest
}

func (b *fakeHostBackend) ChatCompletions(_ context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	b.got = req
	return &ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: 123,
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: "hello from sky10",
			},
			FinishReason: "stop",
		}},
		Usage: &ChatUsage{
			PromptTokens:     3,
			CompletionTokens: 4,
			TotalTokens:      7,
		},
	}, nil
}

type fakeStreamingHostBackend struct {
	calledChat   bool
	calledStream bool
	got          ChatCompletionRequest
	err          error
}

func (b *fakeStreamingHostBackend) ChatCompletions(_ context.Context, _ ChatCompletionRequest) (*ChatCompletionResponse, error) {
	b.calledChat = true
	return nil, errors.New("non-streaming path should not be called")
}

func (b *fakeStreamingHostBackend) StreamChatCompletions(_ context.Context, req ChatCompletionRequest, send func(ChatCompletionStreamChunk) error) error {
	b.calledStream = true
	b.got = req
	if err := send(ChatCompletionStreamChunk{
		ID:      "chatcmpl-stream-test",
		Created: 123,
		Model:   req.Model,
		Choices: []ChatStreamChoice{{
			Index: 0,
			Delta: ChatDelta{
				Role: "assistant",
			},
		}},
	}); err != nil {
		return err
	}
	if err := send(ChatCompletionStreamChunk{
		ID:      "chatcmpl-stream-test",
		Created: 123,
		Model:   req.Model,
		Choices: []ChatStreamChoice{{
			Index: 0,
			Delta: ChatDelta{
				Content: "hello",
			},
		}},
	}); err != nil {
		return err
	}
	return b.err
}

func TestHostChatCompletions(t *testing.T) {
	t.Parallel()

	backend := &fakeHostBackend{}
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: backend,
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-test",
		"stream_options":{"include_usage":true},
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"input_text","text":"world"}]}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if backend.got.Model != "sky10-test" {
		t.Fatalf("backend model = %q", backend.got.Model)
	}
	if backend.got.Messages[0].Content != "hello\n\nworld" {
		t.Fatalf("backend content = %q", backend.got.Messages[0].Content)
	}
	var got ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Object != "chat.completion" || got.Choices[0].Message.Content != "hello from sky10" {
		t.Fatalf("response = %+v", got)
	}
}

func TestHostChatCompletionsStreamsSSE(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: &fakeHostBackend{},
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-test",
		"stream":true,
		"stream_options":{"include_usage":true},
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"object":"chat.completion.chunk"`) {
		t.Fatalf("stream body missing chunk: %s", body)
	}
	if !strings.Contains(body, `"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}`) {
		t.Fatalf("stream body missing usage chunk: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream body missing done: %s", body)
	}
}

func TestHostChatCompletionsForwardsStreamingBackend(t *testing.T) {
	t.Parallel()

	backend := &fakeStreamingHostBackend{}
	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: backend,
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-stream",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if backend.calledChat {
		t.Fatal("non-streaming backend path was called")
	}
	if !backend.calledStream {
		t.Fatal("streaming backend path was not called")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"role":"assistant"`) || !strings.Contains(body, `"content":"hello"`) {
		t.Fatalf("stream body missing forwarded chunks: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream body did not terminate with DONE: %s", body)
	}
}

func TestHostChatCompletionsStreamsBackendErrorsAndDone(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{
		Backend: &fakeStreamingHostBackend{err: errors.New("upstream broke")},
		Now:     func() time.Time { return time.Unix(123, 0) },
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-stream",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "upstream broke") {
		t.Fatalf("stream body missing error: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream body did not terminate with DONE: %s", body)
	}
}

func TestHostChatCompletionsStreamsMissingBackendAsSSE(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-test",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ErrHostBackendNotConfigured.Error()) {
		t.Fatalf("stream body missing backend error: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream body did not terminate with DONE: %s", body)
	}
}

func TestHostChatCompletionsReturnsOpenAIErrorWhenBackendMissing(t *testing.T) {
	t.Parallel()

	handler := NewHostHTTPHandler(HostHTTPOptions{})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"sky10-test",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), `"code":"backend_not_configured"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHostModelsListStoreConnections(t *testing.T) {
	t.Parallel()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "openai-work",
		Label:        "OpenAI Work",
		Provider:     ProviderOpenAI,
		BaseURL:      DefaultOpenAIBaseURL,
		DefaultModel: "gpt-5.5",
		Auth:         AuthConfig{Method: AuthMethodAPIKey, APIKeyEnv: "OPENAI_API_KEY"},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}

	handler := NewHostHTTPHandler(HostHTTPOptions{
		ModelLister: StoreModelLister(store),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.HandleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"openai-work"`) || !strings.Contains(body, `"id":"gpt-5.5"`) {
		t.Fatalf("models body = %s", body)
	}
}
