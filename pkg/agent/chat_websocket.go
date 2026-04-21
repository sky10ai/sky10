package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const chatWebSocketReadLimit = 64 << 20

type chatWSRequest struct {
	Type   string          `json:"type"`
	ID     interface{}     `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type chatWSResponse struct {
	Type   string       `json:"type"`
	ID     interface{}  `json:"id,omitempty"`
	Result interface{}  `json:"result,omitempty"`
	Error  *chatWSError `json:"error,omitempty"`
}

type chatWSError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type chatWSEvent struct {
	Type    string      `json:"type"`
	Event   string      `json:"event"`
	Payload interface{} `json:"payload,omitempty"`
}

type chatWSSendParams struct {
	MessageType string          `json:"message_type,omitempty"`
	Content     json.RawMessage `json:"content"`
}

// ChatStreamMessage is the normalized wire payload emitted by the guest chat
// WebSocket bridge for `delta`, `message`, `done`, and `error` events.
type ChatStreamMessage struct {
	ID          string          `json:"id,omitempty"`
	SessionID   string          `json:"session_id"`
	From        string          `json:"from,omitempty"`
	To          string          `json:"to,omitempty"`
	DeviceID    string          `json:"device_id,omitempty"`
	MessageType string          `json:"message_type,omitempty"`
	Content     json.RawMessage `json:"content,omitempty"`
	Timestamp   time.Time       `json:"timestamp,omitempty"`
}

// ChatWebSocketHandler exposes a narrow guest-side chat transport for one
// session without reopening generic RPC tunneling.
type ChatWebSocketHandler struct {
	registry   *Registry
	sender     *RPCHandler
	hub        *MessageHub
	logger     *slog.Logger
	listAgents func(context.Context) ([]AgentInfo, error)
}

// NewChatWebSocketHandler creates a chat-only guest bridge.
func NewChatWebSocketHandler(registry *Registry, sender *RPCHandler, hub *MessageHub, logger *slog.Logger) *ChatWebSocketHandler {
	handler := &ChatWebSocketHandler{
		registry: registry,
		sender:   sender,
		hub:      hub,
		logger:   componentLogger(logger),
	}
	handler.listAgents = handler.defaultListAgents
	return handler
}

// HandleChat upgrades the request and bridges session-scoped chat traffic.
func (h *ChatWebSocketHandler) HandleChat(w http.ResponseWriter, r *http.Request) {
	if h.registry == nil || h.sender == nil {
		http.Error(w, "agent chat websocket is not configured", http.StatusServiceUnavailable)
		return
	}

	agentName := strings.TrimSpace(r.PathValue("agent"))
	if agentName == "" {
		http.Error(w, "missing agent", http.StatusBadRequest)
		return
	}
	info := h.resolveAgent(r.Context(), agentName)
	if info == nil {
		http.Error(w, fmt.Sprintf("agent %q not found", agentName), http.StatusNotFound)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(chatWebSocketReadLimit)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub := h.hub.Subscribe(ctx, func(msg Message) bool {
		return msg.SessionID == sessionID
	})

	if err := wsjson.Write(ctx, conn, chatWSEvent{
		Type:  "event",
		Event: "session.ready",
		Payload: map[string]interface{}{
			"session_id": sessionID,
			"agent": map[string]string{
				"id":   info.ID,
				"name": info.Name,
			},
		},
	}); err != nil {
		return
	}

	reqCh := make(chan chatWSRequest)
	readErrCh := make(chan error, 1)
	go func() {
		defer close(reqCh)
		for {
			var req chatWSRequest
			if err := wsjson.Read(ctx, conn, &req); err != nil {
				readErrCh <- err
				return
			}
			select {
			case reqCh <- req:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-readErrCh:
			if err == nil {
				return
			}
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				return
			}
			h.logger.Debug("agent chat websocket read failed", "error", err)
			return
		case msg, ok := <-sub:
			if !ok {
				return
			}
			if err := wsjson.Write(ctx, conn, chatWSEvent{
				Type:    "event",
				Event:   chatEventName(msg),
				Payload: normalizeChatStreamMessage(msg),
			}); err != nil {
				return
			}
		case req, ok := <-reqCh:
			if !ok {
				return
			}
			resp := h.handleRequest(ctx, info.ID, sessionID, req)
			if err := wsjson.Write(ctx, conn, resp); err != nil {
				return
			}
		}
	}
}

func (h *ChatWebSocketHandler) defaultListAgents(ctx context.Context) ([]AgentInfo, error) {
	if h.sender != nil && h.sender.router != nil && h.sender.router.node != nil {
		result, err := h.sender.rpcList(ctx, nil)
		if err == nil {
			if payload, ok := result.(map[string]interface{}); ok {
				if agents, ok := payload["agents"].([]AgentInfo); ok {
					return agents, nil
				}
			}
		}
	}
	if h.registry == nil {
		return nil, nil
	}
	return h.registry.List(), nil
}

func (h *ChatWebSocketHandler) resolveAgent(ctx context.Context, nameOrID string) *AgentInfo {
	if h.listAgents != nil {
		agents, err := h.listAgents(ctx)
		if err == nil {
			for i := range agents {
				if agents[i].ID == nameOrID || agents[i].Name == nameOrID {
					info := agents[i]
					return &info
				}
			}
		} else if h.logger != nil {
			h.logger.Debug("agent chat websocket agent list failed", "error", err)
		}
	}
	if h.registry != nil {
		return h.registry.Resolve(nameOrID)
	}
	return nil
}

func (h *ChatWebSocketHandler) handleRequest(ctx context.Context, agentID, sessionID string, req chatWSRequest) chatWSResponse {
	if strings.TrimSpace(req.Type) != "req" {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "bad_request",
				Message: "type must be req",
			},
		}
	}
	if strings.TrimSpace(req.Method) != "message.send" {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "method_not_found",
				Message: "method must be message.send",
			},
		}
	}

	var params chatWSSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "invalid_params",
				Message: "invalid params: " + err.Error(),
			},
		}
	}

	messageType := strings.TrimSpace(params.MessageType)
	if messageType == "" {
		messageType = "chat"
	}
	if messageType != "chat" {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "invalid_params",
				Message: "message_type must be chat",
			},
		}
	}

	content, err := ParseChatContent(params.Content)
	if err != nil {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "invalid_content",
				Message: err.Error(),
			},
		}
	}
	if err := content.Validate(); err != nil {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "invalid_content",
				Message: err.Error(),
			},
		}
	}
	contentRaw, err := content.Marshal()
	if err != nil {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "invalid_content",
				Message: err.Error(),
			},
		}
	}

	result, err := h.sender.SendMessage(ctx, SendParams{
		To:        agentID,
		SessionID: sessionID,
		Type:      messageType,
		Content:   contentRaw,
	})
	if err != nil {
		return chatWSResponse{
			Type: "res",
			ID:   req.ID,
			Error: &chatWSError{
				Code:    "send_failed",
				Message: err.Error(),
			},
		}
	}

	return chatWSResponse{
		Type:   "res",
		ID:     req.ID,
		Result: result,
	}
}

func chatEventName(msg Message) string {
	switch strings.TrimSpace(msg.Type) {
	case "delta", "message", "done", "error":
		return msg.Type
	default:
		return "message"
	}
}

func normalizeChatStreamMessage(msg Message) ChatStreamMessage {
	return ChatStreamMessage{
		ID:          msg.ID,
		SessionID:   msg.SessionID,
		From:        msg.From,
		To:          msg.To,
		DeviceID:    msg.DeviceID,
		MessageType: msg.Type,
		Content:     msg.Content,
		Timestamp:   msg.Timestamp,
	}
}
