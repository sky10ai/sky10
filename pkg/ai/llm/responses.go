package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ResponsesRequest is the normalized subset of OpenAI's Responses API that
// sky10 exposes to guest-local agent runtimes.
type ResponsesRequest struct {
	Model           string             `json:"model,omitempty"`
	Input           json.RawMessage    `json:"input,omitempty"`
	Instructions    string             `json:"instructions,omitempty"`
	Stream          bool               `json:"stream,omitempty"`
	StreamOptions   *ChatStreamOptions `json:"stream_options,omitempty"`
	MaxOutputTokens int                `json:"max_output_tokens,omitempty"`
	MaxTokens       int                `json:"max_tokens,omitempty"`
	Temperature     *float64           `json:"temperature,omitempty"`
	TopP            *float64           `json:"top_p,omitempty"`
	Stop            []string           `json:"stop,omitempty"`
}

type ResponsesResponse struct {
	ID        string                 `json:"id"`
	Object    string                 `json:"object"`
	CreatedAt int64                  `json:"created_at"`
	Status    string                 `json:"status"`
	Model     string                 `json:"model,omitempty"`
	Output    []ResponsesOutputItem  `json:"output"`
	Usage     *ResponsesUsage        `json:"usage,omitempty"`
	Error     *openAIError           `json:"error,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type ResponsesOutputItem struct {
	ID      string                   `json:"id"`
	Type    string                   `json:"type"`
	Status  string                   `json:"status"`
	Role    string                   `json:"role"`
	Content []ResponsesOutputContent `json:"content"`
}

type ResponsesOutputContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Annotations []interface{} `json:"annotations"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// ConvertResponsesRequestToChat converts the guest-local Responses API shape
// into the shared Chat Completions shape used by provider adapters.
func ConvertResponsesRequestToChat(req ResponsesRequest) (ChatCompletionRequest, error) {
	messages, err := responsesInputToChatMessages(req.Input)
	if err != nil {
		return ChatCompletionRequest{}, err
	}
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append([]ChatMessage{{
			Role:    "system",
			Content: req.Instructions,
		}}, messages...)
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = req.MaxTokens
	}

	chatReq := ChatCompletionRequest{
		Model:         req.Model,
		Messages:      messages,
		Stream:        req.Stream,
		StreamOptions: req.StreamOptions,
		MaxTokens:     maxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		Stop:          req.Stop,
	}
	if err := validateHostChatRequest(chatReq); err != nil {
		return ChatCompletionRequest{}, err
	}
	return chatReq, nil
}

func responsesInputToChatMessages(input json.RawMessage) ([]ChatMessage, error) {
	input = trimRawJSON(input)
	if len(input) == 0 || string(input) == "null" {
		return nil, errors.New("input is required")
	}

	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.New("input must not be empty")
		}
		return []ChatMessage{{Role: "user", Content: text}}, nil
	}

	if input[0] == '{' {
		msg, err := responsesInputObjectToChatMessage(0, input)
		if err != nil {
			return nil, err
		}
		return []ChatMessage{msg}, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string, message object, or message array")
	}
	if len(items) == 0 {
		return nil, errors.New("input must not be empty")
	}
	messages := make([]ChatMessage, 0, len(items))
	for i, item := range items {
		item = trimRawJSON(item)
		if len(item) == 0 || string(item) == "null" {
			return nil, fmt.Errorf("input[%d] is required", i)
		}
		var itemText string
		if err := json.Unmarshal(item, &itemText); err == nil {
			itemText = strings.TrimSpace(itemText)
			if itemText == "" {
				return nil, fmt.Errorf("input[%d] must not be empty", i)
			}
			messages = append(messages, ChatMessage{Role: "user", Content: itemText})
			continue
		}
		msg, err := responsesInputObjectToChatMessage(i, item)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func responsesInputObjectToChatMessage(index int, raw json.RawMessage) (ChatMessage, error) {
	var item struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return ChatMessage{}, fmt.Errorf("input[%d] must be an object: %w", index, err)
	}

	itemType := strings.TrimSpace(item.Type)
	role := strings.TrimSpace(strings.ToLower(item.Role))
	switch itemType {
	case "", "message":
		if role == "" {
			return ChatMessage{}, fmt.Errorf("input[%d].role is required", index)
		}
	case "input_text":
		if role == "" {
			role = "user"
		}
	case "output_text":
		if role == "" {
			role = "assistant"
		}
	default:
		return ChatMessage{}, fmt.Errorf("input[%d].type %q is not supported", index, itemType)
	}
	if !supportedResponsesRole(role) {
		return ChatMessage{}, fmt.Errorf("input[%d].role %q is not supported", index, item.Role)
	}

	content, err := responsesContentToText(item.Content, item.Text, fmt.Sprintf("input[%d].content", index))
	if err != nil {
		return ChatMessage{}, err
	}
	return ChatMessage{Role: role, Content: content}, nil
}

func responsesContentToText(raw json.RawMessage, fallbackText, path string) (string, error) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 || string(raw) == "null" {
		text := strings.TrimSpace(fallbackText)
		if text == "" {
			return "", fmt.Errorf("%s is required", path)
		}
		return text, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("%s must not be empty", path)
		}
		return text, nil
	}

	if raw[0] == '{' {
		partText, err := responsesContentPartToText(raw, path)
		if err != nil {
			return "", err
		}
		return partText, nil
	}

	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("%s must be a string, text part, or text part array", path)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%s must not be empty", path)
	}

	textParts := make([]string, 0, len(parts))
	for i, part := range parts {
		partText, err := responsesContentPartToText(part, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(partText) != "" {
			textParts = append(textParts, partText)
		}
	}
	if len(textParts) == 0 {
		return "", fmt.Errorf("%s must include at least one text part", path)
	}
	return strings.Join(textParts, "\n\n"), nil
}

func responsesContentPartToText(raw json.RawMessage, path string) (string, error) {
	raw = trimRawJSON(raw)
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("%s must not be empty", path)
		}
		return text, nil
	}

	var part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &part); err != nil {
		return "", fmt.Errorf("%s must be a text part: %w", path, err)
	}
	partType := strings.TrimSpace(part.Type)
	if !supportedResponsesTextType(partType) {
		return "", fmt.Errorf("%s.type %q is not supported", path, partType)
	}
	text = strings.TrimSpace(part.Text)
	if text == "" {
		return "", fmt.Errorf("%s.text must not be empty", path)
	}
	return text, nil
}

func supportedResponsesRole(role string) bool {
	switch role {
	case "system", "developer", "user", "assistant":
		return true
	default:
		return false
	}
}

func supportedResponsesTextType(partType string) bool {
	switch partType {
	case "", "text", "input_text", "output_text":
		return true
	default:
		return false
	}
}

func trimRawJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

// HandleResponses implements POST /v1/responses.
func (h *HostHTTPHandler) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if !allowHostMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "backend_not_configured", ErrHostBackendNotConfigured.Error(), "")
		return
	}

	var req ResponsesRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, hostMaxRequestBytes))
	if err := decoder.Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json", "invalid JSON: "+err.Error(), "")
		return
	}
	chatReq, err := ConvertResponsesRequestToChat(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error(), "")
		return
	}

	if req.Stream {
		h.handleResponsesStream(w, r, req, chatReq)
		return
	}

	backend := h.chatBackend()
	if backend == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "backend_not_configured", ErrHostBackendNotConfigured.Error(), "")
		return
	}
	resp, err := backend.ChatCompletions(r.Context(), chatReq)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "server_error", "response_failed", err.Error(), "")
		return
	}
	resp = normalizeCompletionResponse(resp, chatReq.Model, h.now().Unix())
	writeHostJSON(w, http.StatusOK, responsesFromChatCompletion(resp, h.now().Unix(), "completed"))
}

func responsesFromChatCompletion(resp *ChatCompletionResponse, createdAt int64, status string) ResponsesResponse {
	out := ResponsesResponse{
		ID:        "resp_" + uuid.NewString(),
		Object:    "response",
		CreatedAt: createdAt,
		Status:    status,
		Output:    []ResponsesOutputItem{},
	}
	if resp != nil {
		if resp.Created != 0 {
			out.CreatedAt = resp.Created
		}
		out.Model = resp.Model
		out.Usage = responsesUsageFromChat(resp.Usage)
		for _, choice := range resp.Choices {
			content := choice.Message.Content
			if strings.TrimSpace(content) == "" {
				continue
			}
			out.Output = append(out.Output, responsesOutputMessage(firstNonEmpty(choice.Message.Role, "assistant"), content))
		}
	}
	return out
}

func responsesOutputMessage(role, text string) ResponsesOutputItem {
	return ResponsesOutputItem{
		ID:     "msg_" + uuid.NewString(),
		Type:   "message",
		Status: "completed",
		Role:   firstNonEmpty(role, "assistant"),
		Content: []ResponsesOutputContent{{
			Type:        "output_text",
			Text:        text,
			Annotations: []interface{}{},
		}},
	}
}

func responsesUsageFromChat(usage *ChatUsage) *ResponsesUsage {
	if usage == nil {
		return nil
	}
	return &ResponsesUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
}

func (h *HostHTTPHandler) handleResponsesStream(w http.ResponseWriter, r *http.Request, req ResponsesRequest, chatReq ChatCompletionRequest) {
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

	responseID := "resp_" + uuid.NewString()
	itemID := "msg_" + uuid.NewString()
	createdAt := h.now().Unix()
	model := chatReq.Model
	var text strings.Builder
	var usage *ResponsesUsage

	writeEvent := func(event string, value interface{}) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
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
	writeError := func(code, message string) {
		_ = writeEvent("error", openAIErrorResponse{
			Error: openAIError{
				Message: message,
				Type:    "server_error",
				Code:    code,
			},
		})
	}

	if err := writeEvent("response.created", map[string]interface{}{
		"type": "response.created",
		"response": ResponsesResponse{
			ID:        responseID,
			Object:    "response",
			CreatedAt: createdAt,
			Status:    "in_progress",
			Model:     model,
			Output:    []ResponsesOutputItem{},
		},
	}); err != nil {
		return
	}

	writeDelta := func(delta string) error {
		if delta == "" {
			return nil
		}
		text.WriteString(delta)
		return writeEvent("response.output_text.delta", map[string]interface{}{
			"type":          "response.output_text.delta",
			"response_id":   responseID,
			"item_id":       itemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         delta,
		})
	}

	backend := h.chatBackend()
	if backend == nil {
		writeError("backend_not_configured", ErrHostBackendNotConfigured.Error())
		writeDone()
		return
	}

	if streamingBackend, ok := backend.(StreamingChatAdapter); ok {
		err := streamingBackend.StreamChatCompletions(r.Context(), chatReq, func(chunk ChatCompletionStreamChunk) error {
			if strings.TrimSpace(chunk.Model) != "" {
				model = chunk.Model
			}
			if chunk.Usage != nil {
				usage = responsesUsageFromChat(chunk.Usage)
			}
			for _, choice := range chunk.Choices {
				if err := writeDelta(choice.Delta.Content); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			writeError("response_failed", err.Error())
			writeDone()
			return
		}
	} else {
		resp, err := backend.ChatCompletions(r.Context(), chatReq)
		if err != nil {
			writeError("response_failed", err.Error())
			writeDone()
			return
		}
		resp = normalizeCompletionResponse(resp, chatReq.Model, createdAt)
		model = resp.Model
		usage = responsesUsageFromChat(resp.Usage)
		for _, choice := range resp.Choices {
			if err := writeDelta(choice.Message.Content); err != nil {
				return
			}
		}
	}

	finalText := text.String()
	completed := ResponsesResponse{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Status:    "completed",
		Model:     model,
		Output:    []ResponsesOutputItem{},
		Usage:     usage,
	}
	if strings.TrimSpace(finalText) != "" {
		completed.Output = append(completed.Output, ResponsesOutputItem{
			ID:     itemID,
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []ResponsesOutputContent{{
				Type:        "output_text",
				Text:        finalText,
				Annotations: []interface{}{},
			}},
		})
	}
	_ = writeEvent("response.completed", map[string]interface{}{
		"type":     "response.completed",
		"response": completed,
	})
	writeDone()
}
