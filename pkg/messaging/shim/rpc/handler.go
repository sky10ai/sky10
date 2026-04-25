package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingshim "github.com/sky10/sky10/pkg/messaging/shim"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

var _ skyrpc.Handler = (*Handler)(nil)

// Service is the exposure-bound messaging surface exposed over JSON-RPC.
type Service interface {
	ListConnections(ctx context.Context) ([]messaging.Connection, error)
	ListIdentities(ctx context.Context, connectionID messaging.ConnectionID) ([]messaging.Identity, error)
	ListConversations(ctx context.Context, connectionID messaging.ConnectionID) ([]messaging.Conversation, error)
	GetConversation(ctx context.Context, connectionID messaging.ConnectionID, conversationID messaging.ConversationID) (messaging.Conversation, bool, error)
	GetMessages(ctx context.Context, connectionID messaging.ConnectionID, conversationID messaging.ConversationID) ([]messaging.Message, error)
	ListContainers(ctx context.Context, params protocol.ListContainersParams) (protocol.ListContainersResult, error)
	SearchIdentities(ctx context.Context, params protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error)
	SearchConversations(ctx context.Context, params protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error)
	SearchMessages(ctx context.Context, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error)
	CreateDraft(ctx context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error)
	UpdateDraft(ctx context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error)
	RequestSend(ctx context.Context, draftID messaging.DraftID, newConversation bool) (messagingbroker.RequestSendDraftResult, error)
	MoveMessages(ctx context.Context, params protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error)
	MoveConversation(ctx context.Context, params protocol.MoveConversationParams) (protocol.ManageMessagesResult, error)
	ArchiveMessages(ctx context.Context, params protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error)
	ArchiveConversation(ctx context.Context, params protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error)
	ApplyLabels(ctx context.Context, params protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error)
	MarkRead(ctx context.Context, params protocol.MarkReadParams) (protocol.ManageMessagesResult, error)
}

// Config configures one JSON-RPC handler for a single exposure-bound shim.
type Config struct {
	Service Service
}

// Handler dispatches messaging.shim.* JSON-RPC methods.
type Handler struct {
	service Service
}

// NewHandler creates a local JSON-RPC handler over one exposure-bound service.
func NewHandler(cfg Config) *Handler {
	return &Handler{service: cfg.Service}
}

// Dispatch implements the repo RPC handler contract.
func (h *Handler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "messaging.shim.") {
		return nil, nil, false
	}
	if h == nil || h.service == nil {
		return nil, fmt.Errorf("messaging shim rpc handler is not configured"), true
	}

	var result interface{}
	var err error
	switch messagingshim.Method(method) {
	case messagingshim.MethodListConnections:
		result, err = h.rpcListConnections(ctx)
	case messagingshim.MethodListIdentities:
		result, err = h.rpcListIdentities(ctx, params)
	case messagingshim.MethodListConversations:
		result, err = h.rpcListConversations(ctx, params)
	case messagingshim.MethodGetConversation:
		result, err = h.rpcGetConversation(ctx, params)
	case messagingshim.MethodGetMessages:
		result, err = h.rpcGetMessages(ctx, params)
	case messagingshim.MethodListContainers:
		result, err = h.rpcListContainers(ctx, params)
	case messagingshim.MethodSearchIdentities:
		result, err = h.rpcSearchIdentities(ctx, params)
	case messagingshim.MethodSearchConversations:
		result, err = h.rpcSearchConversations(ctx, params)
	case messagingshim.MethodSearchMessages:
		result, err = h.rpcSearchMessages(ctx, params)
	case messagingshim.MethodCreateDraft:
		result, err = h.rpcCreateDraft(ctx, params)
	case messagingshim.MethodUpdateDraft:
		result, err = h.rpcUpdateDraft(ctx, params)
	case messagingshim.MethodRequestSend:
		result, err = h.rpcRequestSend(ctx, params)
	case messagingshim.MethodMoveMessages:
		result, err = h.rpcMoveMessages(ctx, params)
	case messagingshim.MethodMoveConversation:
		result, err = h.rpcMoveConversation(ctx, params)
	case messagingshim.MethodArchiveMessages:
		result, err = h.rpcArchiveMessages(ctx, params)
	case messagingshim.MethodArchiveConversation:
		result, err = h.rpcArchiveConversation(ctx, params)
	case messagingshim.MethodApplyLabels:
		result, err = h.rpcApplyLabels(ctx, params)
	case messagingshim.MethodMarkRead:
		result, err = h.rpcMarkRead(ctx, params)
	case messagingshim.MethodSubscribeEvents:
		return nil, fmt.Errorf("%s is reserved until shim event fanout is implemented", method), true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
	return result, err, true
}

type listConnectionsResult struct {
	Connections []messaging.Connection `json:"connections"`
	Count       int                    `json:"count"`
}

type listIdentitiesResult struct {
	Identities []messaging.Identity `json:"identities"`
	Count      int                  `json:"count"`
}

type listConversationsResult struct {
	Conversations []messaging.Conversation `json:"conversations"`
	Count         int                      `json:"count"`
}

type getConversationResult struct {
	Conversation messaging.Conversation `json:"conversation,omitempty"`
	Found        bool                   `json:"found"`
}

type getMessagesResult struct {
	Messages []messaging.Message `json:"messages"`
	Count    int                 `json:"count"`
}

type connectionParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
}

type conversationParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id"`
}

type draftParams struct {
	Draft messaging.Draft `json:"draft"`
}

type requestSendParams struct {
	DraftID         messaging.DraftID `json:"draft_id"`
	NewConversation bool              `json:"new_conversation,omitempty"`
}

func (h *Handler) rpcListConnections(ctx context.Context) (listConnectionsResult, error) {
	connections, err := h.service.ListConnections(ctx)
	if err != nil {
		return listConnectionsResult{}, err
	}
	return listConnectionsResult{Connections: connections, Count: len(connections)}, nil
}

func (h *Handler) rpcListIdentities(ctx context.Context, params json.RawMessage) (listIdentitiesResult, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return listIdentitiesResult{}, err
	}
	identities, err := h.service.ListIdentities(ctx, p.ConnectionID)
	if err != nil {
		return listIdentitiesResult{}, err
	}
	return listIdentitiesResult{Identities: identities, Count: len(identities)}, nil
}

func (h *Handler) rpcListConversations(ctx context.Context, params json.RawMessage) (listConversationsResult, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return listConversationsResult{}, err
	}
	conversations, err := h.service.ListConversations(ctx, p.ConnectionID)
	if err != nil {
		return listConversationsResult{}, err
	}
	return listConversationsResult{Conversations: conversations, Count: len(conversations)}, nil
}

func (h *Handler) rpcGetConversation(ctx context.Context, params json.RawMessage) (getConversationResult, error) {
	p, err := parseConversationParams(params)
	if err != nil {
		return getConversationResult{}, err
	}
	conversation, found, err := h.service.GetConversation(ctx, p.ConnectionID, p.ConversationID)
	if err != nil {
		return getConversationResult{}, err
	}
	return getConversationResult{Conversation: conversation, Found: found}, nil
}

func (h *Handler) rpcGetMessages(ctx context.Context, params json.RawMessage) (getMessagesResult, error) {
	p, err := parseConversationParams(params)
	if err != nil {
		return getMessagesResult{}, err
	}
	messages, err := h.service.GetMessages(ctx, p.ConnectionID, p.ConversationID)
	if err != nil {
		return getMessagesResult{}, err
	}
	return getMessagesResult{Messages: messages, Count: len(messages)}, nil
}

func (h *Handler) rpcListContainers(ctx context.Context, params json.RawMessage) (protocol.ListContainersResult, error) {
	p, err := parseListContainersParams(params)
	if err != nil {
		return protocol.ListContainersResult{}, err
	}
	return h.service.ListContainers(ctx, p)
}

func (h *Handler) rpcSearchIdentities(ctx context.Context, params json.RawMessage) (protocol.SearchIdentitiesResult, error) {
	var p protocol.SearchIdentitiesParams
	if err := parseParams(params, &p); err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	return h.service.SearchIdentities(ctx, p)
}

func (h *Handler) rpcSearchConversations(ctx context.Context, params json.RawMessage) (protocol.SearchConversationsResult, error) {
	var p protocol.SearchConversationsParams
	if err := parseParams(params, &p); err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	return h.service.SearchConversations(ctx, p)
}

func (h *Handler) rpcSearchMessages(ctx context.Context, params json.RawMessage) (protocol.SearchMessagesResult, error) {
	var p protocol.SearchMessagesParams
	if err := parseParams(params, &p); err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	return h.service.SearchMessages(ctx, p)
}

func (h *Handler) rpcCreateDraft(ctx context.Context, params json.RawMessage) (messagingbroker.DraftMutationResult, error) {
	draft, err := parseDraftParams(params)
	if err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	return h.service.CreateDraft(ctx, draft)
}

func (h *Handler) rpcUpdateDraft(ctx context.Context, params json.RawMessage) (messagingbroker.DraftMutationResult, error) {
	draft, err := parseDraftParams(params)
	if err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	return h.service.UpdateDraft(ctx, draft)
}

func (h *Handler) rpcRequestSend(ctx context.Context, params json.RawMessage) (messagingbroker.RequestSendDraftResult, error) {
	var p requestSendParams
	if err := parseParams(params, &p); err != nil {
		return messagingbroker.RequestSendDraftResult{}, err
	}
	if strings.TrimSpace(string(p.DraftID)) == "" {
		return messagingbroker.RequestSendDraftResult{}, fmt.Errorf("draft_id is required")
	}
	return h.service.RequestSend(ctx, p.DraftID, p.NewConversation)
}

func (h *Handler) rpcMoveMessages(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.MoveMessagesParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.MoveMessages(ctx, p)
}

func (h *Handler) rpcMoveConversation(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.MoveConversationParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.MoveConversation(ctx, p)
}

func (h *Handler) rpcArchiveMessages(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.ArchiveMessagesParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.ArchiveMessages(ctx, p)
}

func (h *Handler) rpcArchiveConversation(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.ArchiveConversationParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.ArchiveConversation(ctx, p)
}

func (h *Handler) rpcApplyLabels(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.ApplyLabelsParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.ApplyLabels(ctx, p)
}

func (h *Handler) rpcMarkRead(ctx context.Context, params json.RawMessage) (protocol.ManageMessagesResult, error) {
	var p protocol.MarkReadParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return h.service.MarkRead(ctx, p)
}

func parseConnectionParams(params json.RawMessage) (connectionParams, error) {
	var p connectionParams
	if err := parseParams(params, &p); err != nil {
		return connectionParams{}, err
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return connectionParams{}, fmt.Errorf("connection_id is required")
	}
	return p, nil
}

func parseConversationParams(params json.RawMessage) (conversationParams, error) {
	var p conversationParams
	if err := parseParams(params, &p); err != nil {
		return conversationParams{}, err
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return conversationParams{}, fmt.Errorf("connection_id is required")
	}
	if strings.TrimSpace(string(p.ConversationID)) == "" {
		return conversationParams{}, fmt.Errorf("conversation_id is required")
	}
	return p, nil
}

func parseListContainersParams(params json.RawMessage) (protocol.ListContainersParams, error) {
	var p protocol.ListContainersParams
	if err := parseParams(params, &p); err != nil {
		return protocol.ListContainersParams{}, err
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return protocol.ListContainersParams{}, fmt.Errorf("connection_id is required")
	}
	return p, nil
}

func validateSearchParams(connectionID messaging.ConnectionID, query string) error {
	if strings.TrimSpace(string(connectionID)) == "" {
		return fmt.Errorf("connection_id is required")
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("query is required")
	}
	return nil
}

func parseDraftParams(params json.RawMessage) (messaging.Draft, error) {
	var p draftParams
	if err := parseParams(params, &p); err != nil {
		return messaging.Draft{}, err
	}
	if err := p.Draft.Validate(); err != nil {
		return messaging.Draft{}, err
	}
	return p.Draft, nil
}

func parseParams(params json.RawMessage, out interface{}) error {
	if len(params) == 0 {
		params = []byte("{}")
	}
	if err := json.Unmarshal(params, out); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}
