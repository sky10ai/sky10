package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeMeteredServiceBackend struct {
	params       MeteredServiceCallParams
	streamParams MeteredServiceCallParams
	streamChunks [][]byte
	result       *MeteredServiceCallResult
	err          error
}

func (b *fakeMeteredServiceBackend) CallMeteredService(_ context.Context, params MeteredServiceCallParams) (*MeteredServiceCallResult, error) {
	b.params = params
	if b.err != nil {
		return nil, b.err
	}
	return b.result, nil
}

func (b *fakeMeteredServiceBackend) StreamMeteredService(_ context.Context, params MeteredServiceCallParams, send func([]byte) error) (*MeteredServiceCallResult, error) {
	b.streamParams = params
	if b.err != nil {
		return nil, b.err
	}
	for _, chunk := range b.streamChunks {
		if len(chunk) == 0 || send == nil {
			continue
		}
		if err := send(chunk); err != nil {
			return nil, err
		}
	}
	return b.result, nil
}

func TestVeniceAdapterChatCompletionsCallsMeteredService(t *testing.T) {
	t.Parallel()

	fake := &fakeMeteredServiceBackend{result: veniceTestCompletion(t, "anthropic/opus-4-7", "alpha-stream response")}
	adapter := NewVeniceAdapter(VeniceAdapterOptions{
		Backend:      fake,
		BaseURL:      DefaultVeniceBaseURL,
		Model:        "anthropic/opus-4-7",
		ServiceID:    "venice-ai",
		MaxPriceUSDC: "0.020",
		AgentID:      "guest-agent",
	})

	resp, err := adapter.ChatCompletions(context.Background(), ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Say hi."}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions: %v", err)
	}
	if resp.Model != "anthropic/opus-4-7" {
		t.Fatalf("resp.Model = %q", resp.Model)
	}
	if fake.params.AgentID != "guest-agent" {
		t.Fatalf("AgentID = %q", fake.params.AgentID)
	}
	if fake.params.ServiceID != "venice-ai" {
		t.Fatalf("ServiceID = %q", fake.params.ServiceID)
	}
	if fake.params.Path != "/api/v1/chat/completions" {
		t.Fatalf("Path = %q", fake.params.Path)
	}
	if fake.params.Method != "POST" {
		t.Fatalf("Method = %q", fake.params.Method)
	}
	if fake.params.MaxPriceUSDC != "0.020" {
		t.Fatalf("MaxPriceUSDC = %q", fake.params.MaxPriceUSDC)
	}
	if !strings.HasPrefix(fake.params.PaymentNonce, "llm-") {
		t.Fatalf("PaymentNonce = %q", fake.params.PaymentNonce)
	}
	if fake.params.Headers["Content-Type"] != "application/json" || fake.params.Headers["Accept"] != "application/json" {
		t.Fatalf("Headers = %+v", fake.params.Headers)
	}

	var upstream ChatCompletionRequest
	if err := json.Unmarshal(fake.params.Body, &upstream); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if upstream.Model != "anthropic/opus-4-7" {
		t.Fatalf("upstream.Model = %q", upstream.Model)
	}
	if upstream.Stream {
		t.Fatal("upstream.Stream = true, want false because x402 backend returns buffered responses")
	}
}

func TestVeniceAdapterStreamChatCompletionsUsesMeteredStream(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("alpha-stream beta-stream gamma-stream delta-stream ", 8)
	fake := &fakeMeteredServiceBackend{
		result: veniceTestCompletion(t, DefaultVeniceModel, content),
		streamChunks: [][]byte{
			veniceTestStreamData(t, ChatCompletionStreamChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: 123,
				Model:   DefaultVeniceModel,
				Choices: []ChatStreamChoice{{
					Index: 0,
					Delta: ChatDelta{Role: "assistant"},
				}},
			}),
			veniceTestStreamData(t, ChatCompletionStreamChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: 123,
				Model:   DefaultVeniceModel,
				Choices: []ChatStreamChoice{{
					Index: 0,
					Delta: ChatDelta{Content: content[:120]},
				}},
			}),
			veniceTestStreamData(t, ChatCompletionStreamChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: 123,
				Model:   DefaultVeniceModel,
				Choices: []ChatStreamChoice{{
					Index: 0,
					Delta: ChatDelta{Content: content[120:]},
				}},
			}),
			veniceTestStreamData(t, ChatCompletionStreamChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: 123,
				Model:   DefaultVeniceModel,
				Choices: []ChatStreamChoice{{
					Index:        0,
					Delta:        ChatDelta{},
					FinishReason: stringPtr("stop"),
				}},
			}),
			veniceTestStreamData(t, ChatCompletionStreamChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: 123,
				Model:   DefaultVeniceModel,
				Choices: []ChatStreamChoice{},
				Usage:   &ChatUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
			}),
			[]byte("data: [DONE]\n\n"),
		},
	}
	adapter := NewVeniceAdapter(VeniceAdapterOptions{
		Backend: fake,
		Now:     func() time.Time { return time.Unix(123, 0).UTC() },
	})

	var chunks []ChatCompletionStreamChunk
	err := adapter.StreamChatCompletions(context.Background(), ChatCompletionRequest{
		Model:         DefaultVeniceModel,
		Stream:        true,
		StreamOptions: &ChatStreamOptions{IncludeUsage: true},
		Messages:      []ChatMessage{{Role: "user", Content: "stream it"}},
	}, func(chunk ChatCompletionStreamChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatCompletions: %v", err)
	}
	if len(chunks) < 4 {
		t.Fatalf("len(chunks) = %d, want role, multiple content chunks, finish, usage", len(chunks))
	}
	contentChunks := 0
	finished := false
	hasUsage := false
	var got strings.Builder
	for _, chunk := range chunks {
		if chunk.Usage != nil {
			hasUsage = true
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				contentChunks++
				got.WriteString(choice.Delta.Content)
			}
			if choice.FinishReason != nil && *choice.FinishReason == "stop" {
				finished = true
			}
		}
	}
	if contentChunks < 2 {
		t.Fatalf("contentChunks = %d, want at least 2", contentChunks)
	}
	if got.String() != content {
		t.Fatalf("streamed content mismatch")
	}
	var upstream ChatCompletionRequest
	if err := json.Unmarshal(fake.streamParams.Body, &upstream); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if !upstream.Stream || upstream.StreamOptions == nil || !upstream.StreamOptions.IncludeUsage {
		t.Fatalf("upstream stream fields = stream:%v stream_options:%+v, want true stream request with usage", upstream.Stream, upstream.StreamOptions)
	}
	if !finished {
		t.Fatal("missing finish chunk")
	}
	if !hasUsage {
		t.Fatal("missing usage chunk")
	}
}

func TestVeniceAdapterErrorsWithoutBackend(t *testing.T) {
	t.Parallel()

	adapter := NewVeniceAdapter(VeniceAdapterOptions{})
	_, err := adapter.ChatCompletions(context.Background(), ChatCompletionRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "x402 backend is not configured") {
		t.Fatalf("err = %v, want x402 backend error", err)
	}
}

func TestVeniceChatCompletionsPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "default api base", baseURL: "https://api.venice.ai/api/v1", want: "/api/v1/chat/completions"},
		{name: "root venice host", baseURL: "https://api.venice.ai", want: "/api/v1/chat/completions"},
		{name: "already endpoint", baseURL: "https://api.venice.ai/api/v1/chat/completions", want: "/api/v1/chat/completions"},
		{name: "custom base path", baseURL: "https://example.com/custom/v1", want: "/custom/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := veniceChatCompletionsPath(tt.baseURL); got != tt.want {
				t.Fatalf("veniceChatCompletionsPath(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func veniceTestCompletion(t *testing.T, model, content string) *MeteredServiceCallResult {
	t.Helper()
	body, err := json.Marshal(ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: 123,
		Model:   model,
		Choices: []ChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: "stop",
		}},
		Usage: &ChatUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &MeteredServiceCallResult{Status: 200, Body: body}
}

func veniceTestStreamData(t *testing.T, chunk ChatCompletionStreamChunk) []byte {
	t.Helper()
	raw, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("data: " + string(raw) + "\n\n")
}

func stringPtr(value string) *string {
	return &value
}
