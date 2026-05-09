package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type OpenAIAdapterOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

// OpenAIAdapter calls OpenAI's native Chat Completions endpoint.
type OpenAIAdapter struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

type openAIChatCompletionRequest struct {
	Model               string             `json:"model,omitempty"`
	Messages            []ChatMessage      `json:"messages"`
	Stream              bool               `json:"stream,omitempty"`
	StreamOptions       *ChatStreamOptions `json:"stream_options,omitempty"`
	MaxCompletionTokens int                `json:"max_completion_tokens,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	Stop                []string           `json:"stop,omitempty"`
}

func NewOpenAIAdapter(opts OpenAIAdapterOptions) *OpenAIAdapter {
	client := providerHTTPClient(opts.HTTPClient)
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	model := opts.Model
	if model == "" {
		model = DefaultOpenAIModel
	}
	return &OpenAIAdapter{
		apiKey:  opts.APIKey,
		baseURL: baseURL,
		model:   model,
		client:  client,
	}
}

func (a *OpenAIAdapter) ChatCompletions(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if a == nil {
		return nil, fmt.Errorf("openai adapter is nil")
	}
	if a.apiKey == "" {
		return nil, fmt.Errorf("openai api key is required")
	}
	if req.Stream {
		return nil, ErrStreamingNotImplemented
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages are required")
	}
	if req.Model == "" {
		req.Model = a.model
	}
	var out ChatCompletionResponse
	if err := providerDoJSON(ctx, a.client, providerRequestOptions{
		Provider:  "openai",
		Operation: "openai chat completions",
		URL:       a.baseURL + "/chat/completions",
		Accept:    "application/json",
		Headers: map[string]string{
			"Authorization": "Bearer " + a.apiKey,
		},
		Body: openAIRequestFromChat(req),
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *OpenAIAdapter) StreamChatCompletions(ctx context.Context, req ChatCompletionRequest, send func(ChatCompletionStreamChunk) error) error {
	if a == nil {
		return fmt.Errorf("openai adapter is nil")
	}
	if a.apiKey == "" {
		return fmt.Errorf("openai api key is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages are required")
	}
	if req.Model == "" {
		req.Model = a.model
	}
	req.Stream = true
	body, err := providerDoStream(ctx, a.client, providerRequestOptions{
		Provider:  "openai",
		Operation: "openai chat completions stream",
		URL:       a.baseURL + "/chat/completions",
		Accept:    "text/event-stream",
		Headers: map[string]string{
			"Authorization": "Bearer " + a.apiKey,
		},
		Body: openAIRequestFromChat(req),
	})
	if err != nil {
		return err
	}
	defer body.Close()
	return scanServerSentEvents(ctx, body, func(event serverSentEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var chunk ChatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode openai stream chunk: %w", err)
		}
		return send(chunk)
	})
}

func openAIRequestFromChat(req ChatCompletionRequest) openAIChatCompletionRequest {
	return openAIChatCompletionRequest{
		Model:               req.Model,
		Messages:            req.Messages,
		Stream:              req.Stream,
		StreamOptions:       req.StreamOptions,
		MaxCompletionTokens: req.MaxTokens,
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stop:                req.Stop,
	}
}
