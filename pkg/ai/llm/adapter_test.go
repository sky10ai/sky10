package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIAdapterChatCompletions(t *testing.T) {
	var gotPath, gotAuth string
	var gotReq ChatCompletionRequest
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
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
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
	if resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("response content = %q", resp.Choices[0].Message.Content)
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
