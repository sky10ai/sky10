package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if a.backend == nil {
		return fmt.Errorf("venice x402 backend is not configured")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages are required")
	}
	req.Stream = true
	if strings.TrimSpace(req.Model) == "" {
		req.Model = a.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode venice chat completions stream request: %w", err)
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, err := a.backend.StreamMeteredService(ctx, MeteredServiceCallParams{
			AgentID:   a.agentID,
			ServiceID: a.serviceID,
			Path:      veniceChatCompletionsPath(a.baseURL),
			Method:    http.MethodPost,
			Headers: map[string]string{
				"Accept":       "text/event-stream",
				"Content-Type": "application/json",
			},
			Body:         body,
			MaxPriceUSDC: a.maxPriceUSDC,
			PaymentNonce: "llm-" + uuid.NewString(),
		}, func(chunk []byte) error {
			if len(chunk) == 0 {
				return nil
			}
			_, writeErr := pw.Write(chunk)
			return writeErr
		})
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		errCh <- err
	}()

	scanErr := scanServerSentEvents(ctx, pr, func(event serverSentEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var chunk ChatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode venice stream chunk: %w", err)
		}
		return send(chunk)
	})
	if scanErr != nil {
		_ = pr.CloseWithError(scanErr)
	}
	streamErr := <-errCh
	if scanErr != nil {
		return scanErr
	}
	return streamErr
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
