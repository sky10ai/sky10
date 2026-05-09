package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionChatBackendRoutesOpenAIConnectionWithSecretRef(t *testing.T) {
	var gotAuth string
	var gotReq ChatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]
		}`))
	}))
	defer srv.Close()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "openai-work",
		Label:        "OpenAI Work",
		Provider:     ProviderOpenAI,
		BaseURL:      srv.URL,
		DefaultModel: "gpt-default",
		Auth: AuthConfig{
			Method:    AuthMethodAPIKey,
			SecretRef: "openai-secret",
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	backend := NewConnectionChatBackend(store, ConnectionChatBackendOptions{
		SecretResolver: SecretResolverFunc(func(_ context.Context, ref string) (string, error) {
			if ref != "openai-secret" {
				t.Fatalf("secret ref = %q", ref)
			}
			return "sk-secret", nil
		}),
	})
	resp, err := backend.ChatCompletions(context.Background(), ChatCompletionRequest{
		Model:    "openai-work/gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotReq.Model != "gpt-test" {
		t.Fatalf("request model = %q", gotReq.Model)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("response content = %q", resp.Choices[0].Message.Content)
	}
}

func TestConnectionChatBackendRoutesAnthropicProviderPrefix(t *testing.T) {
	var gotReq anthropicMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "anthropic-secret" {
			t.Fatalf("x-api-key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":4,"output_tokens":2}
		}`))
	}))
	defer srv.Close()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "anthropic-work",
		Label:        "Anthropic Work",
		Provider:     ProviderAnthropic,
		BaseURL:      srv.URL,
		DefaultModel: "claude-default",
		Auth: AuthConfig{
			Method:     AuthMethodAPIKey,
			SecretRef:  "anthropic-secret",
			APIVersion: DefaultAnthropicAPIVersion,
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	backend := NewConnectionChatBackend(store, ConnectionChatBackendOptions{
		SecretResolver: SecretResolverFunc(func(_ context.Context, ref string) (string, error) {
			if ref != "anthropic-secret" {
				t.Fatalf("secret ref = %q", ref)
			}
			return "anthropic-secret", nil
		}),
	})
	_, err := backend.ChatCompletions(context.Background(), ChatCompletionRequest{
		Model:    "anthropic/claude-test",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if gotReq.Model != "claude-test" {
		t.Fatalf("request model = %q", gotReq.Model)
	}
}

func TestConnectionChatBackendRoutesStreamingOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !req.Stream {
			t.Fatal("stream = false, want true")
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
				"delta": map[string]string{"content": "hello"},
			}},
		})
	}))
	defer srv.Close()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:       "openai-work",
		Label:    "OpenAI Work",
		Provider: ProviderOpenAI,
		BaseURL:  srv.URL,
		Auth:     AuthConfig{Method: AuthMethodAPIKey, SecretRef: "openai-secret"},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	backend := NewConnectionChatBackend(store, ConnectionChatBackendOptions{
		SecretResolver: SecretResolverFunc(func(context.Context, string) (string, error) {
			return "sk-secret", nil
		}),
	})
	var chunks []ChatCompletionStreamChunk
	err := backend.StreamChatCompletions(context.Background(), ChatCompletionRequest{
		Model:    "openai-work/gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(chunk ChatCompletionStreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatCompletions() error = %v", err)
	}
	if len(chunks) != 1 || chunks[0].Choices[0].Delta.Content != "hello" {
		t.Fatalf("chunks = %+v", chunks)
	}
}

func TestConnectionChatBackendRoutesVeniceThroughMeteredService(t *testing.T) {
	t.Parallel()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:           "bovilus-venice",
		Label:        "Bovilus Venice",
		Provider:     ProviderVenice,
		BaseURL:      DefaultVeniceBaseURL,
		DefaultModel: DefaultVeniceModel,
		Models:       []string{"anthropic/opus-4-7"},
		Auth: AuthConfig{
			Method:       AuthMethodX402,
			ServiceID:    DefaultVeniceX402Service,
			Network:      "base",
			MaxPriceUSDC: "0.020",
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	fake := &fakeMeteredServiceBackend{result: veniceTestCompletion(t, "anthropic/opus-4-7", "hello from venice")}
	backend := NewConnectionChatBackend(store, ConnectionChatBackendOptions{
		MeteredService: fake,
		MeteredAgentID: "guest-agent",
	})

	resp, err := backend.ChatCompletions(context.Background(), ChatCompletionRequest{
		Model:    "bovilus-venice/anthropic/opus-4-7",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if resp.Choices[0].Message.Content != "hello from venice" {
		t.Fatalf("response content = %q", resp.Choices[0].Message.Content)
	}
	if fake.params.AgentID != "guest-agent" {
		t.Fatalf("AgentID = %q", fake.params.AgentID)
	}
	var upstream ChatCompletionRequest
	if err := json.Unmarshal(fake.params.Body, &upstream); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if upstream.Model != "anthropic/opus-4-7" {
		t.Fatalf("upstream model = %q", upstream.Model)
	}
}

func TestConnectionChatBackendRejectsVeniceWithoutMeteredService(t *testing.T) {
	t.Parallel()

	store := NewStore(filepath.Join(t.TempDir(), "connections.json"))
	if _, err := store.Upsert(context.Background(), Connection{
		ID:       "venice-live",
		Label:    "Venice Live",
		Provider: ProviderVenice,
		BaseURL:  DefaultVeniceBaseURL,
		Auth: AuthConfig{
			Method:       AuthMethodX402,
			ServiceID:    DefaultVeniceX402Service,
			Network:      "base",
			MaxPriceUSDC: "0.020",
		},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	backend := NewConnectionChatBackend(store, ConnectionChatBackendOptions{})

	_, err := backend.ChatCompletions(context.Background(), ChatCompletionRequest{
		Model:    "venice-live/" + DefaultVeniceModel,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "x402 backend is not configured") {
		t.Fatalf("err = %v, want x402 backend error", err)
	}
}
