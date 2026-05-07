package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var ErrStreamingNotImplemented = errors.New("llm: streaming is not implemented for this adapter")

// ChatAdapter is the guest provider boundary for one AI connection.
// Host HTTP and websocket wrappers should translate their transport
// shape into this normalized chat shape before choosing a provider.
type ChatAdapter interface {
	ChatCompletions(context.Context, ChatCompletionRequest) (*ChatCompletionResponse, error)
}

// StreamingChatAdapter can emit OpenAI-compatible chat completion chunks.
type StreamingChatAdapter interface {
	ChatAdapter
	StreamChatCompletions(context.Context, ChatCompletionRequest, func(ChatCompletionStreamChunk) error) error
}

// AdapterOptions supplies runtime-only adapter dependencies. API keys are not
// persisted in Connection; callers either pass APIKey directly or let the
// adapter read Connection.Auth.APIKeyEnv.
type AdapterOptions struct {
	APIKey           string
	HTTPClient       *http.Client
	BaseURL          string
	Model            string
	AnthropicVersion string
	Now              func() time.Time
}

// NewNativeAdapter constructs the native HTTP adapter for providers that do
// not need x402. Venice is intentionally excluded because it is routed through
// pkg/x402, not direct bearer-token HTTP.
func NewNativeAdapter(connection Connection, opts AdapterOptions) (ChatAdapter, error) {
	apiKey := opts.APIKey
	if apiKey == "" && connection.Auth.APIKeyEnv != "" {
		apiKey = os.Getenv(connection.Auth.APIKeyEnv)
	}
	if apiKey == "" && connection.Auth.SecretRef != "" {
		return nil, fmt.Errorf("%s secret_ref %q must be resolved before constructing the native adapter", connection.Provider, connection.Auth.SecretRef)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s api key is required", connection.Provider)
	}
	switch connection.Provider {
	case ProviderOpenAI:
		return NewOpenAIAdapter(OpenAIAdapterOptions{
			APIKey:     apiKey,
			BaseURL:    firstNonEmpty(opts.BaseURL, connection.BaseURL),
			Model:      firstNonEmpty(opts.Model, connection.DefaultModel),
			HTTPClient: opts.HTTPClient,
		}), nil
	case ProviderAnthropic:
		return NewAnthropicAdapter(AnthropicAdapterOptions{
			APIKey:     apiKey,
			BaseURL:    firstNonEmpty(opts.BaseURL, connection.BaseURL),
			Model:      firstNonEmpty(opts.Model, connection.DefaultModel),
			Version:    firstNonEmpty(opts.AnthropicVersion, connection.Auth.APIVersion),
			HTTPClient: opts.HTTPClient,
			Now:        opts.Now,
		}), nil
	case ProviderVenice:
		return nil, errors.New("venice uses the x402 adapter, not the native HTTP adapter")
	default:
		return nil, fmt.Errorf("provider %q is not supported", connection.Provider)
	}
}

// ChatCompletionRequest is the normalized host request shape. It mirrors
// the useful core of OpenAI Chat Completions so Hermes/OpenClaw-style clients
// can be translated with minimal loss.
type ChatCompletionRequest struct {
	Model         string             `json:"model,omitempty"`
	Messages      []ChatMessage      `json:"messages"`
	Stream        bool               `json:"stream,omitempty"`
	StreamOptions *ChatStreamOptions `json:"stream_options,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	Stop          []string           `json:"stop,omitempty"`
}

// ChatMessage is one text chat message in the normalized request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// UnmarshalJSON accepts both the common string content form and the structured
// text-part form used by newer OpenAI-compatible clients.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		m.Content = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil {
		m.Content = text
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw.Content, &parts); err == nil {
		textParts := make([]string, 0, len(parts))
		for _, part := range parts {
			partType := strings.TrimSpace(part.Type)
			if partType != "" && partType != "text" && partType != "input_text" {
				continue
			}
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		}
		m.Content = strings.Join(textParts, "\n\n")
		return nil
	}
	var objectPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw.Content, &objectPart); err == nil && strings.TrimSpace(objectPart.Text) != "" {
		m.Content = objectPart.Text
		return nil
	}
	return fmt.Errorf("content must be a string or text parts")
}

// ChatCompletionResponse is the normalized response shape returned by native
// adapters. It is intentionally OpenAI-compatible so the guest HTTP wrapper can
// write it directly for non-streaming /v1/chat/completions calls.
type ChatCompletionResponse struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// ChatCompletionStreamChunk mirrors OpenAI's streaming chunk shape.
type ChatCompletionStreamChunk struct {
	ID      string             `json:"id,omitempty"`
	Object  string             `json:"object,omitempty"`
	Created int64              `json:"created,omitempty"`
	Model   string             `json:"model,omitempty"`
	Choices []ChatStreamChoice `json:"choices"`
	Usage   *ChatUsage         `json:"usage,omitempty"`
}

type ChatStreamChoice struct {
	Index        int       `json:"index"`
	Delta        ChatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason,omitempty"`
}

type ChatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
