package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIAdapterChatCompletions(t *testing.T) {
	var gotPath, gotAuth string
	var gotReq struct {
		Model               string        `json:"model"`
		Messages            []ChatMessage `json:"messages"`
		MaxTokens           int           `json:"max_tokens"`
		MaxCompletionTokens int           `json:"max_completion_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1778100000,
			"model":"gpt-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer srv.Close()

	adapter := NewOpenAIAdapter(OpenAIAdapterOptions{
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-test",
	})
	resp, err := adapter.ChatCompletions(context.Background(), ChatCompletionRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 11,
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotReq.Model != "gpt-test" {
		t.Fatalf("request model = %q", gotReq.Model)
	}
	if gotReq.MaxTokens != 0 || gotReq.MaxCompletionTokens != 11 {
		t.Fatalf("request token fields max_tokens=%d max_completion_tokens=%d", gotReq.MaxTokens, gotReq.MaxCompletionTokens)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("response content = %q", resp.Choices[0].Message.Content)
	}
}

func TestOpenAIAdapterStreamChatCompletions(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotReq struct {
		Model               string        `json:"model"`
		Messages            []ChatMessage `json:"messages"`
		Stream              bool          `json:"stream"`
		MaxTokens           int           `json:"max_tokens"`
		MaxCompletionTokens int           `json:"max_completion_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeTestSSE(t, w, flusher, "", map[string]interface{}{
			"id":      "chatcmpl-stream",
			"object":  "chat.completion.chunk",
			"created": 1778100000,
			"model":   "gpt-test",
			"choices": []map[string]interface{}{{
				"index": 0,
				"delta": map[string]string{"role": "assistant"},
			}},
		})
		writeTestSSE(t, w, flusher, "", map[string]interface{}{
			"id":      "chatcmpl-stream",
			"object":  "chat.completion.chunk",
			"created": 1778100000,
			"model":   "gpt-test",
			"choices": []map[string]interface{}{{
				"index": 0,
				"delta": map[string]string{"content": "hello"},
			}},
		})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	adapter := NewOpenAIAdapter(OpenAIAdapterOptions{
		APIKey:  "sk-test",
		BaseURL: srv.URL,
		Model:   "gpt-test",
	})
	var chunks []ChatCompletionStreamChunk
	err := adapter.StreamChatCompletions(context.Background(), ChatCompletionRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 12,
	}, func(chunk ChatCompletionStreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatCompletions() error = %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if !gotReq.Stream {
		t.Fatal("request stream = false, want true")
	}
	if gotReq.Model != "gpt-test" {
		t.Fatalf("request model = %q", gotReq.Model)
	}
	if gotReq.MaxTokens != 0 || gotReq.MaxCompletionTokens != 12 {
		t.Fatalf("request token fields max_tokens=%d max_completion_tokens=%d", gotReq.MaxTokens, gotReq.MaxCompletionTokens)
	}
	if len(chunks) != 2 || chunks[0].Choices[0].Delta.Role != "assistant" || chunks[1].Choices[0].Delta.Content != "hello" {
		t.Fatalf("chunks = %+v", chunks)
	}
}

func TestAnthropicAdapterChatCompletions(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	var gotReq anthropicMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"hello"},{"type":"text","text":" there"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":2}
		}`))
	}))
	defer srv.Close()

	clock := func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }
	adapter := NewAnthropicAdapter(AnthropicAdapterOptions{
		APIKey:  "anthropic-test",
		BaseURL: srv.URL,
		Model:   "claude-test",
		Now:     clock,
	})
	resp, err := adapter.ChatCompletions(context.Background(), ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if gotPath != "/messages" {
		t.Fatalf("path = %q, want /messages", gotPath)
	}
	if gotKey != "anthropic-test" {
		t.Fatalf("x-api-key = %q", gotKey)
	}
	if gotVersion != DefaultAnthropicAPIVersion {
		t.Fatalf("anthropic-version = %q", gotVersion)
	}
	if gotReq.System != "be concise" {
		t.Fatalf("system = %q", gotReq.System)
	}
	if gotReq.MaxTokens != DefaultAnthropicMaxTokens {
		t.Fatalf("max_tokens = %d", gotReq.MaxTokens)
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" || gotReq.Messages[0].Content != "hi" {
		t.Fatalf("messages = %+v", gotReq.Messages)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("object = %q", resp.Object)
	}
	if resp.Created != clock().Unix() {
		t.Fatalf("created = %d", resp.Created)
	}
	if resp.Choices[0].Message.Content != "hello there" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens != 6 {
		t.Fatalf("total tokens = %d", resp.Usage.TotalTokens)
	}
}

func TestAnthropicAdapterStreamChatCompletions(t *testing.T) {
	var gotPath, gotKey, gotVersion, gotAccept string
	var gotReq anthropicMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeTestSSE(t, w, flusher, "message_start", map[string]interface{}{
			"message": map[string]interface{}{
				"id":    "msg_stream",
				"role":  "assistant",
				"model": "claude-test",
				"usage": map[string]int{"input_tokens": 4, "output_tokens": 0},
			},
		})
		writeTestSSE(t, w, flusher, "content_block_delta", map[string]interface{}{
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": "hello"},
		})
		writeTestSSE(t, w, flusher, "message_delta", map[string]interface{}{
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": 2},
		})
		writeTestSSE(t, w, flusher, "message_stop", map[string]interface{}{})
	}))
	defer srv.Close()

	clock := func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }
	adapter := NewAnthropicAdapter(AnthropicAdapterOptions{
		APIKey:  "anthropic-test",
		BaseURL: srv.URL,
		Model:   "claude-test",
		Now:     clock,
	})
	var chunks []ChatCompletionStreamChunk
	err := adapter.StreamChatCompletions(context.Background(), ChatCompletionRequest{
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		Messages: []ChatMessage{
			{Role: "system", Content: "be concise"},
			{Role: "user", Content: "hi"},
		},
	}, func(chunk ChatCompletionStreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatCompletions() error = %v", err)
	}
	if gotPath != "/messages" {
		t.Fatalf("path = %q, want /messages", gotPath)
	}
	if gotKey != "anthropic-test" {
		t.Fatalf("x-api-key = %q", gotKey)
	}
	if gotVersion != DefaultAnthropicAPIVersion {
		t.Fatalf("anthropic-version = %q", gotVersion)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if !gotReq.Stream {
		t.Fatal("request stream = false, want true")
	}
	if len(chunks) != 4 {
		t.Fatalf("chunk count = %d, chunks = %+v", len(chunks), chunks)
	}
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Fatalf("first chunk = %+v", chunks[0])
	}
	if chunks[1].Choices[0].Delta.Content != "hello" {
		t.Fatalf("content chunk = %+v", chunks[1])
	}
	if chunks[2].Choices[0].FinishReason == nil || *chunks[2].Choices[0].FinishReason != "stop" {
		t.Fatalf("finish chunk = %+v", chunks[2])
	}
	if chunks[3].Usage == nil || chunks[3].Usage.TotalTokens != 6 {
		t.Fatalf("usage chunk = %+v", chunks[3])
	}
}

func TestNewNativeAdapterRejectsVenice(t *testing.T) {
	_, err := NewNativeAdapter(Connection{
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth:     AuthConfig{Method: AuthMethodX402},
	}, AdapterOptions{APIKey: "ignored"})
	if err == nil {
		t.Fatal("NewNativeAdapter() error = nil, want venice rejection")
	}
}

func writeTestSSE(t *testing.T, w http.ResponseWriter, flusher http.Flusher, event string, payload interface{}) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal sse payload: %v", err)
	}
	if strings.TrimSpace(event) != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			t.Fatalf("write sse event: %v", err)
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		t.Fatalf("write sse data: %v", err)
	}
	flusher.Flush()
}
