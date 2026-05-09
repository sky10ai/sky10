package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
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
	buf, err := json.Marshal(openAIRequestFromChat(req))
	if err != nil {
		return nil, fmt.Errorf("encode openai request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai chat completions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerHTTPError("openai", resp)
	}
	var out ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
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
	buf, err := json.Marshal(openAIRequestFromChat(req))
	if err != nil {
		return fmt.Errorf("encode openai stream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai chat completions stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerHTTPError("openai", resp)
	}
	return scanServerSentEvents(ctx, resp.Body, func(event serverSentEvent) error {
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

func providerHTTPError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s upstream returned HTTP %d: %s", provider, resp.StatusCode, msg)
}
