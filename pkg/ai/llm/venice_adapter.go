package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type VeniceAdapterOptions struct {
	Backend      MeteredServiceBackend
	BaseURL      string
	Model        string
	ServiceID    string
	MaxPriceUSDC string
	AgentID      string
	Now          func() time.Time
}

// VeniceAdapter calls Venice's OpenAI-compatible chat endpoint through the
// daemon's x402 metered-service backend. It never talks to Venice directly:
// payment, SIWX, receipts, and host/guest forwarding live below this adapter.
type VeniceAdapter struct {
	backend      MeteredServiceBackend
	baseURL      string
	model        string
	serviceID    string
	maxPriceUSDC string
	agentID      string
	now          func() time.Time
}

func NewVeniceAdapter(opts VeniceAdapterOptions) *VeniceAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultVeniceBaseURL
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultVeniceModel
	}
	serviceID := strings.TrimSpace(opts.ServiceID)
	if serviceID == "" {
		serviceID = DefaultVeniceX402Service
	}
	maxPriceUSDC := strings.TrimSpace(opts.MaxPriceUSDC)
	if maxPriceUSDC == "" {
		maxPriceUSDC = DefaultVeniceMaxPriceUSDC
	}
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		agentID = DefaultMeteredServiceAgentID
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &VeniceAdapter{
		backend:      opts.Backend,
		baseURL:      baseURL,
		model:        model,
		serviceID:    serviceID,
		maxPriceUSDC: maxPriceUSDC,
		agentID:      agentID,
		now:          now,
	}
}

func (a *VeniceAdapter) ChatCompletions(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if a == nil {
		return nil, fmt.Errorf("venice adapter is nil")
	}
	if a.backend == nil {
		return nil, fmt.Errorf("venice x402 backend is not configured")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("messages are required")
	}
	req.Stream = false
	req.StreamOptions = nil
	if strings.TrimSpace(req.Model) == "" {
		req.Model = a.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode venice chat completions request: %w", err)
	}
	result, err := a.backend.CallMeteredService(ctx, MeteredServiceCallParams{
		AgentID:   a.agentID,
		ServiceID: a.serviceID,
		Path:      veniceChatCompletionsPath(a.baseURL),
		Method:    http.MethodPost,
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
		Body:         body,
		MaxPriceUSDC: a.maxPriceUSDC,
		PaymentNonce: "llm-" + uuid.NewString(),
	})
	if err != nil {
		return nil, fmt.Errorf("venice x402 call: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("venice x402 call returned nil result")
	}
	if result.Status < 200 || result.Status >= 300 {
		return nil, fmt.Errorf("venice upstream returned HTTP %d: %s", result.Status, strings.TrimSpace(string(result.Body)))
	}
	var out ChatCompletionResponse
	if err := json.Unmarshal(result.Body, &out); err != nil {
		return nil, fmt.Errorf("decode venice chat completions response: %w", err)
	}
	if out.Model == "" {
		out.Model = req.Model
	}
	return &out, nil
}

func (a *VeniceAdapter) StreamChatCompletions(ctx context.Context, req ChatCompletionRequest, send func(ChatCompletionStreamChunk) error) error {
	if a == nil {
		return fmt.Errorf("venice adapter is nil")
	}
	req.Stream = false
	resp, err := a.ChatCompletions(ctx, req)
	if err != nil {
		return err
	}
	resp = normalizeCompletionResponse(resp, req.Model, a.now().Unix())
	for _, choice := range resp.Choices {
		if err := send(ChatCompletionStreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Created: resp.Created,
			Model:   resp.Model,
			Choices: []ChatStreamChoice{{
				Index: choice.Index,
				Delta: ChatDelta{
					Role: firstNonEmpty(choice.Message.Role, "assistant"),
				},
			}},
		}); err != nil {
			return err
		}
		for _, part := range splitStreamContent(choice.Message.Content) {
			if err := send(ChatCompletionStreamChunk{
				ID:      resp.ID,
				Object:  "chat.completion.chunk",
				Created: resp.Created,
				Model:   resp.Model,
				Choices: []ChatStreamChoice{{
					Index: choice.Index,
					Delta: ChatDelta{Content: part},
				}},
			}); err != nil {
				return err
			}
		}
		finishReason := firstNonEmpty(choice.FinishReason, "stop")
		if err := send(ChatCompletionStreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Created: resp.Created,
			Model:   resp.Model,
			Choices: []ChatStreamChoice{{
				Index:        choice.Index,
				Delta:        ChatDelta{},
				FinishReason: &finishReason,
			}},
		}); err != nil {
			return err
		}
	}
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage && resp.Usage != nil {
		return send(ChatCompletionStreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Created: resp.Created,
			Model:   resp.Model,
			Choices: []ChatStreamChoice{},
			Usage:   resp.Usage,
		})
	}
	return nil
}

func veniceChatCompletionsPath(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "/api/v1/chat/completions"
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" || path == "/" {
		path = "/api/v1"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasSuffix(path, "/chat/completions") {
		return path
	}
	return path + "/chat/completions"
}

func splitStreamContent(text string) []string {
	if text == "" {
		return nil
	}
	const target = 96
	var parts []string
	for len(text) > target {
		cut := target
		if idx := strings.LastIndexAny(text[:target], " \n\t"); idx > 0 {
			cut = idx + 1
		}
		parts = append(parts, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
}
