package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type AnthropicAdapterOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	Version    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// AnthropicAdapter calls Anthropic's native Messages endpoint and maps the
// response back to the normalized OpenAI-compatible chat shape.
type AnthropicAdapter struct {
	apiKey  string
	baseURL string
	model   string
	version string
	client  *http.Client
	now     func() time.Time
}

func NewAnthropicAdapter(opts AnthropicAdapterOptions) *AnthropicAdapter {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	model := opts.Model
	if model == "" {
		model = DefaultAnthropicModel
	}
	version := opts.Version
	if version == "" {
		version = DefaultAnthropicAPIVersion
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &AnthropicAdapter{
		apiKey:  opts.APIKey,
		baseURL: baseURL,
		model:   model,
		version: version,
		client:  client,
		now:     now,
	}
}

func (a *AnthropicAdapter) ChatCompletions(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if a == nil {
		return nil, fmt.Errorf("anthropic adapter is nil")
	}
	if a.apiKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}
	if req.Stream {
		return nil, ErrStreamingNotImplemented
	}
	anthropicReq, err := a.toAnthropicRequest(req)
	if err != nil {
		return nil, err
	}
	buf, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("encode anthropic request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", a.version)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic messages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerHTTPError("anthropic", resp)
	}
	var decoded anthropicMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	return a.fromAnthropicResponse(decoded), nil
}

type anthropicMessageRequest struct {
	Model         string             `json:"model"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicMessageResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Model      string                  `json:"model"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (a *AnthropicAdapter) toAnthropicRequest(req ChatCompletionRequest) (anthropicMessageRequest, error) {
	if len(req.Messages) == 0 {
		return anthropicMessageRequest{}, fmt.Errorf("messages are required")
	}
	model := req.Model
	if model == "" {
		model = a.model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultAnthropicMaxTokens
	}
	var systemParts []string
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		switch role {
		case "system", "developer":
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "user", "assistant":
			messages = append(messages, anthropicMessage{
				Role:    role,
				Content: msg.Content,
			})
		default:
			return anthropicMessageRequest{}, fmt.Errorf("anthropic adapter does not support role %q yet", msg.Role)
		}
	}
	if len(messages) == 0 {
		return anthropicMessageRequest{}, fmt.Errorf("at least one user or assistant message is required")
	}
	return anthropicMessageRequest{
		Model:         model,
		System:        strings.Join(systemParts, "\n\n"),
		Messages:      messages,
		MaxTokens:     maxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        false,
	}, nil
}

func (a *AnthropicAdapter) fromAnthropicResponse(resp anthropicMessageResponse) *ChatCompletionResponse {
	var textParts []string
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			textParts = append(textParts, block.Text)
		}
	}
	model := resp.Model
	if model == "" {
		model = a.model
	}
	return &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: a.now().UTC().Unix(),
		Model:   model,
		Choices: []ChatChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: strings.Join(textParts, ""),
			},
			FinishReason: anthropicFinishReason(resp.StopReason),
		}},
		Usage: &ChatUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

func anthropicFinishReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}
