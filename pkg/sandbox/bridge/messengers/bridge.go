package messengers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const (
	ErrCodeHostBridgeDisconnected = "host_bridge_disconnected"

	BridgeRoleQuery = "bridge_role"
	BridgeRoleHost  = "host"
)

// ForwardingBackend implements Backend inside a guest daemon. It forwards
// already validated messenger requests over the host-opened bridge socket.
type ForwardingBackend struct {
	mu   sync.RWMutex
	conn *bridge.Conn
}

func NewForwardingBackend() *ForwardingBackend {
	return &ForwardingBackend{}
}

// HandlerWithHostBridge wraps the normal agent-facing bridge handler with the
// host-upstream attachment path. Runtime callers use the same endpoint without
// bridge_role=host; the host daemon dials with bridge_role=host and keeps that
// socket as the forwarding upstream.
func HandlerWithHostBridge(local http.HandlerFunc, forwarder *ForwardingBackend, opts ...bridge.Option) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get(BridgeRoleQuery) != BridgeRoleHost {
			local(w, r)
			return
		}
		if forwarder == nil {
			http.Error(w, "messengers bridge is not configured", http.StatusServiceUnavailable)
			return
		}
		conn, err := bridge.Accept(w, r, nil, opts...)
		if err != nil {
			return
		}
		forwarder.Attach(conn)
		defer forwarder.Detach(conn)
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}
}

func (b *ForwardingBackend) Attach(conn *bridge.Conn) {
	b.mu.Lock()
	old := b.conn
	b.conn = conn
	b.mu.Unlock()
	if old != nil && old != conn {
		_ = old.Close(websocket.StatusNormalClosure, "replaced")
	}
}

func (b *ForwardingBackend) Detach(conn *bridge.Conn) {
	b.mu.Lock()
	if b.conn == conn {
		b.conn = nil
	}
	b.mu.Unlock()
}

func (b *ForwardingBackend) Connected() bool {
	b.mu.RLock()
	ok := b.conn != nil
	b.mu.RUnlock()
	return ok
}

func (b *ForwardingBackend) ListConnections(ctx context.Context, params ListConnectionsParams) ([]messaging.Connection, error) {
	raw, err := b.call(ctx, TypeListConnections, params)
	if err != nil {
		return nil, err
	}
	var result listConnectionsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Connections, nil
}

func (b *ForwardingBackend) ListConversations(ctx context.Context, params ListConversationsParams) ([]messaging.Conversation, error) {
	raw, err := b.call(ctx, TypeListConversations, params)
	if err != nil {
		return nil, err
	}
	var result listConversationsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Conversations, nil
}

func (b *ForwardingBackend) ListEvents(ctx context.Context, params ListEventsParams) ([]messaging.Event, error) {
	raw, err := b.call(ctx, TypeListEvents, params)
	if err != nil {
		return nil, err
	}
	var result listEventsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Events, nil
}

func (b *ForwardingBackend) GetMessages(ctx context.Context, params GetMessagesParams) ([]messaging.Message, error) {
	raw, err := b.call(ctx, TypeGetMessages, params)
	if err != nil {
		return nil, err
	}
	var result getMessagesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Messages, nil
}

func (b *ForwardingBackend) CreateDraft(ctx context.Context, params CreateDraftParams) (messagingbroker.DraftMutationResult, error) {
	raw, err := b.call(ctx, TypeCreateDraft, params)
	if err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	var result messagingbroker.DraftMutationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	return result, nil
}

func (b *ForwardingBackend) RequestSend(ctx context.Context, params RequestSendParams) (messagingbroker.RequestSendDraftResult, error) {
	raw, err := b.call(ctx, TypeRequestSend, params)
	if err != nil {
		return messagingbroker.RequestSendDraftResult{}, err
	}
	var result messagingbroker.RequestSendDraftResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return messagingbroker.RequestSendDraftResult{}, err
	}
	return result, nil
}

func (b *ForwardingBackend) call(ctx context.Context, typ string, payload any) (json.RawMessage, error) {
	conn := b.activeConn()
	if conn == nil {
		return nil, bridge.HandlerError(ErrCodeHostBridgeDisconnected, "messengers host bridge is not connected")
	}
	raw, err := conn.Call(ctx, typ, payload)
	if err != nil {
		if errors.Is(err, bridge.ErrClosed) {
			b.Detach(conn)
		}
		return nil, err
	}
	return raw, nil
}

func (b *ForwardingBackend) activeConn() *bridge.Conn {
	b.mu.RLock()
	conn := b.conn
	b.mu.RUnlock()
	return conn
}

// PreferForwardingBackend uses the guest forwarding backend when a host
// upstream is attached and otherwise falls back to a local backend.
type PreferForwardingBackend struct {
	Forwarder *ForwardingBackend
	Local     Backend
}

func (b PreferForwardingBackend) ListConnections(ctx context.Context, params ListConnectionsParams) ([]messaging.Connection, error) {
	return b.backend().ListConnections(ctx, params)
}

func (b PreferForwardingBackend) ListConversations(ctx context.Context, params ListConversationsParams) ([]messaging.Conversation, error) {
	return b.backend().ListConversations(ctx, params)
}

func (b PreferForwardingBackend) ListEvents(ctx context.Context, params ListEventsParams) ([]messaging.Event, error) {
	return b.backend().ListEvents(ctx, params)
}

func (b PreferForwardingBackend) GetMessages(ctx context.Context, params GetMessagesParams) ([]messaging.Message, error) {
	return b.backend().GetMessages(ctx, params)
}

func (b PreferForwardingBackend) CreateDraft(ctx context.Context, params CreateDraftParams) (messagingbroker.DraftMutationResult, error) {
	return b.backend().CreateDraft(ctx, params)
}

func (b PreferForwardingBackend) RequestSend(ctx context.Context, params RequestSendParams) (messagingbroker.RequestSendDraftResult, error) {
	return b.backend().RequestSend(ctx, params)
}

func (b PreferForwardingBackend) backend() Backend {
	if b.Forwarder != nil && b.Forwarder.Connected() {
		return b.Forwarder
	}
	return b.Local
}

// NewBridgeHandler returns the host-side handler for requests forwarded over a
// host-owned sandbox bridge connection. agentID is trusted host state derived
// from the sandbox record; request payload identity is ignored.
func NewBridgeHandler(backend Backend, agentID string) bridge.Handler {
	trustedAgentID := strings.TrimSpace(agentID)
	return func(ctx context.Context, req bridge.Request) (json.RawMessage, error) {
		if backend == nil {
			return nil, bridge.HandlerError("backend_unavailable", "messengers backend is not configured")
		}
		if trustedAgentID == "" {
			return nil, bridge.HandlerError("agent_unavailable", "trusted agent identity is not configured")
		}
		return dispatchBridgeRequest(ctx, backend, trustedAgentID, req)
	}
}

func dispatchBridgeRequest(ctx context.Context, backend Backend, trustedAgentID string, req bridge.Request) (json.RawMessage, error) {
	switch req.Type {
	case TypeListConnections:
		params, err := parseListConnectionsParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateListConnectionsParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		connections, err := backend.ListConnections(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(listConnectionsResult{Connections: connections})
	case TypeListConversations:
		params, err := parseListConversationsParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateListConversationsParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		conversations, err := backend.ListConversations(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(listConversationsResult{Conversations: conversations})
	case TypeListEvents:
		params, err := parseListEventsParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateListEventsParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		events, err := backend.ListEvents(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(listEventsResult{Events: events})
	case TypeGetMessages:
		params, err := parseGetMessagesParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateGetMessagesParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		messages, err := backend.GetMessages(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(getMessagesResult{Messages: messages})
	case TypeCreateDraft:
		params, err := parseCreateDraftParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateCreateDraftParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		result, err := backend.CreateDraft(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case TypeRequestSend:
		params, err := parseRequestSendParams(req.Payload)
		if err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		if err := validateRequestSendParams(params); err != nil {
			return nil, bridge.HandlerError("invalid_payload", err.Error())
		}
		params.AgentID = trustedAgentID
		result, err := backend.RequestSend(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, bridge.HandlerError("type_unregistered", "unregistered messengers bridge type")
	}
}
