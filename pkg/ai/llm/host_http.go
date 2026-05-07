package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const hostMaxRequestBytes = 64 << 20

var ErrHostBackendNotConfigured = errors.New("llm host chat backend is not configured")

// HostHTTPHandler exposes the client-facing OpenAI-compatible HTTP
// surface. It owns only the host protocol contract; provider routing and
// agent routing plug in behind ChatAdapter later.
type HostHTTPHandler struct {
	mu          sync.RWMutex
	backend     ChatAdapter
	modelLister ModelLister
	now         func() time.Time
}

type HostHTTPOptions struct {
	Backend     ChatAdapter
	ModelLister ModelLister
	Now         func() time.Time
}

// ModelLister returns the OpenAI-compatible model list visible to host
// clients.
type ModelLister func(context.Context) ([]HostModel, error)

type HostModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by"`
}

type hostModelList struct {
	Object string      `json:"object"`
	Data   []HostModel `json:"data"`
}

type openAIErrorResponse struct {
	Error openAIError `json:"error"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

// NewHostHTTPHandler creates the OpenAI-compatible HTTP handler.
func NewHostHTTPHandler(opts HostHTTPOptions) *HostHTTPHandler {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &HostHTTPHandler{
		backend:     opts.Backend,
		modelLister: opts.ModelLister,
		now:         now,
	}
}

// SetBackend swaps the chat backend without remounting HTTP routes.
func (h *HostHTTPHandler) SetBackend(backend ChatAdapter) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.backend = backend
}

// HandleHealth is a lightweight readiness endpoint for clients that expect an
// OpenAI-like base URL to expose a health check.
func (h *HostHTTPHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowHostMethod(w, r, http.MethodGet) {
		return
	}
	writeHostJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleModels implements GET /v1/models.
func (h *HostHTTPHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if !allowHostMethod(w, r, http.MethodGet) {
		return
	}

	models := []HostModel{}
	if h != nil && h.modelLister != nil {
		listed, err := h.modelLister(r.Context())
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "model_list_failed", err.Error(), "")
			return
		}
		models = listed
	}
	writeHostJSON(w, http.StatusOK, hostModelList{
		Object: "list",
		Data:   models,
	})
}

// HandleChatCompletions implements POST /v1/chat/completions.
func (h *HostHTTPHandler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !allowHostMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "backend_not_configured", ErrHostBackendNotConfigured.Error(), "")
		return
	}

	var req ChatCompletionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, hostMaxRequestBytes))
	if err := decoder.Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "invalid JSON: "+err.Error(), "")
		return
	}
	if err := validateHostChatRequest(req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error(), "")
		return
	}

	if req.Stream {
		h.handleChatCompletionsStream(w, r, req)
		return
	}

	backend := h.chatBackend()
	if backend == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "backend_not_configured", ErrHostBackendNotConfigured.Error(), "")
		return
	}
	resp, err := backend.ChatCompletions(r.Context(), req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "server_error", "chat_completion_failed", err.Error(), "")
		return
	}
	writeHostJSON(w, http.StatusOK, normalizeCompletionResponse(resp, req.Model, h.now().Unix()))
}

func (h *HostHTTPHandler) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "streaming_unsupported", "streaming unsupported", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeChunk := func(chunk ChatCompletionStreamChunk) error {
		if chunk.Object == "" {
			chunk.Object = "chat.completion.chunk"
		}
		if chunk.Created == 0 {
			chunk.Created = h.now().Unix()
		}
		if chunk.Model == "" {
			chunk.Model = req.Model
		}
		body, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	writeDone := func() {
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	backend := h.chatBackend()
	if backend == nil {
		_ = writeChunk(errorStreamChunk(req.Model, ErrHostBackendNotConfigured.Error(), h.now().Unix()))
		writeDone()
		return
	}

	if streamingBackend, ok := backend.(StreamingChatAdapter); ok {
		if err := streamingBackend.StreamChatCompletions(r.Context(), req, writeChunk); err != nil {
			_ = writeChunk(errorStreamChunk(req.Model, err.Error(), h.now().Unix()))
		}
		writeDone()
		return
	}

	resp, err := backend.ChatCompletions(r.Context(), req)
	if err != nil {
		_ = writeChunk(errorStreamChunk(req.Model, err.Error(), h.now().Unix()))
		writeDone()
		return
	}
	resp = normalizeCompletionResponse(resp, req.Model, h.now().Unix())
	for _, choice := range resp.Choices {
		if err := writeChunk(ChatCompletionStreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Created: resp.Created,
			Model:   resp.Model,
			Choices: []ChatStreamChoice{{
				Index: choice.Index,
				Delta: ChatDelta{
					Role:    firstNonEmpty(choice.Message.Role, "assistant"),
					Content: choice.Message.Content,
				},
			}},
		}); err != nil {
			return
		}
		finishReason := firstNonEmpty(choice.FinishReason, "stop")
		if err := writeChunk(ChatCompletionStreamChunk{
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
			return
		}
	}
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage && resp.Usage != nil {
		if err := writeChunk(ChatCompletionStreamChunk{
			ID:      resp.ID,
			Object:  "chat.completion.chunk",
			Created: resp.Created,
			Model:   resp.Model,
			Choices: []ChatStreamChoice{},
			Usage:   resp.Usage,
		}); err != nil {
			return
		}
	}
	writeDone()
}

func (h *HostHTTPHandler) chatBackend() ChatAdapter {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.backend
}

func validateHostChatRequest(req ChatCompletionRequest) error {
	if len(req.Messages) == 0 {
		return errors.New("messages are required")
	}
	for i, msg := range req.Messages {
		if strings.TrimSpace(msg.Role) == "" {
			return fmt.Errorf("messages[%d].role is required", i)
		}
		if strings.TrimSpace(msg.Content) == "" {
			return fmt.Errorf("messages[%d].content is required", i)
		}
	}
	return nil
}

func normalizeCompletionResponse(resp *ChatCompletionResponse, fallbackModel string, fallbackCreated int64) *ChatCompletionResponse {
	if resp == nil {
		resp = &ChatCompletionResponse{}
	}
	if resp.ID == "" {
		resp.ID = "chatcmpl-" + uuid.NewString()
	}
	if resp.Object == "" {
		resp.Object = "chat.completion"
	}
	if resp.Created == 0 {
		resp.Created = fallbackCreated
	}
	if resp.Model == "" {
		resp.Model = fallbackModel
	}
	for i := range resp.Choices {
		if resp.Choices[i].Message.Role == "" {
			resp.Choices[i].Message.Role = "assistant"
		}
		if resp.Choices[i].FinishReason == "" {
			resp.Choices[i].FinishReason = "stop"
		}
	}
	return resp
}

func errorStreamChunk(model, message string, created int64) ChatCompletionStreamChunk {
	finishReason := "error"
	return ChatCompletionStreamChunk{
		ID:      "chatcmpl-" + uuid.NewString(),
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []ChatStreamChoice{{
			Index: 0,
			Delta: ChatDelta{
				Role:    "assistant",
				Content: message,
			},
			FinishReason: &finishReason,
		}},
	}
}

func allowHostMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != method {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "method not allowed", "")
		return false
	}
	return true
}

func writeHostJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpenAIError(w http.ResponseWriter, status int, typ, code, message, param string) {
	writeHostJSON(w, status, openAIErrorResponse{
		Error: openAIError{
			Message: message,
			Type:    typ,
			Param:   param,
			Code:    code,
		},
	})
}

// StoreModelLister exposes saved connection ids and default model ids through
// the OpenAI-compatible /models list. It does not imply that a guest
// provider is wired yet.
func StoreModelLister(store *Store) ModelLister {
	return func(context.Context) ([]HostModel, error) {
		if store == nil {
			return nil, nil
		}
		connections, err := store.List()
		if err != nil {
			return nil, err
		}
		seen := make(map[string]struct{})
		models := make([]HostModel, 0, len(connections))
		add := func(id, owner string) {
			id = strings.TrimSpace(id)
			if id == "" {
				return
			}
			if _, ok := seen[id]; ok {
				return
			}
			seen[id] = struct{}{}
			models = append(models, HostModel{
				ID:      id,
				Object:  "model",
				OwnedBy: firstNonEmpty(strings.TrimSpace(owner), "sky10"),
			})
		}
		for _, connection := range connections {
			add(connection.ID, connection.Provider)
			add(connection.DefaultModel, connection.Provider)
		}
		return models, nil
	}
}
