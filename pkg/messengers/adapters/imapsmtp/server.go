package imapsmtp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

type connectionState struct {
	config        adapterConfig
	connectedAt   time.Time
	identities    []messaging.Identity
	conversations map[messaging.ConversationID]messaging.Conversation
	messages      map[messaging.MessageID]messaging.Message
}

type pollSnapshot struct {
	Events        []messaging.Event
	Conversations []messaging.Conversation
	Messages      []messaging.Message
	Checkpoint    *protocol.Checkpoint
}

type sendSnapshot struct {
	Message messaging.Message
}

type service struct {
	logger *slog.Logger
	now    func() time.Time

	verifyFunc func(context.Context, adapterConfig) error
	pollFunc   func(context.Context, adapterConfig, *protocol.Checkpoint, int) (pollSnapshot, error)
	sendFunc   func(context.Context, adapterConfig, messaging.Draft, []string, outboundHeaders) (sendSnapshot, error)

	mu          sync.RWMutex
	connections map[messaging.ConnectionID]*connectionState
}

type outboundHeaders struct {
	Subject    string
	InReplyTo  string
	References []string
}

func newServer() *service {
	return &service{
		logger:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
		now:         func() time.Time { return time.Now().UTC() },
		verifyFunc:  verifyMailboxAccess,
		pollFunc:    pollMailbox,
		sendFunc:    sendMailMessage,
		connections: make(map[messaging.ConnectionID]*connectionState),
	}
}

func (s *service) Serve(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	if stderr != nil {
		s.logger = slog.New(slog.NewTextHandler(stderr, nil))
	}
	dec := messagingruntime.NewDecoder(stdin)
	enc := messagingruntime.NewEncoder(stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var req messagingruntime.Request
		if err := dec.Read(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		resp := s.handle(ctx, req)
		if err := enc.Write(resp); err != nil {
			return err
		}
	}
}

func (s *service) handle(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	switch req.Method {
	case string(protocol.MethodDescribe):
		return s.handleDescribe(req)
	case string(protocol.MethodValidateConfig):
		return s.handleValidateConfig(req)
	case string(protocol.MethodConnect):
		return s.handleConnect(ctx, req)
	case string(protocol.MethodRefresh):
		return s.handleRefresh(ctx, req)
	case string(protocol.MethodListIdentities):
		return s.handleListIdentities(req)
	case string(protocol.MethodListConversations):
		return s.handleListConversations(req)
	case string(protocol.MethodListMessages):
		return s.handleListMessages(req)
	case string(protocol.MethodGetMessage):
		return s.handleGetMessage(req)
	case string(protocol.MethodListContainers):
		return s.handleListContainers(req)
	case string(protocol.MethodCreateDraft):
		return s.handleCreateDraft(req)
	case string(protocol.MethodUpdateDraft):
		return s.handleUpdateDraft(req)
	case string(protocol.MethodDeleteDraft):
		return s.handleDeleteDraft(req)
	case string(protocol.MethodSendMessage):
		return s.handleSend(ctx, req, false)
	case string(protocol.MethodReplyMessage):
		return s.handleSend(ctx, req, true)
	case string(protocol.MethodPoll):
		return s.handlePoll(ctx, req)
	case string(protocol.MethodHealth):
		return s.handleHealth(req)
	default:
		return errorResponse(req.ID, -32601, "method not found")
	}
}

func (s *service) handleDescribe(req messagingruntime.Request) messagingruntime.Response {
	return resultResponse(req.ID, protocol.DescribeResult{
		Protocol: protocol.CurrentProtocol(),
		Adapter: messaging.Adapter{
			ID:          "imap-smtp",
			DisplayName: "IMAP/SMTP",
			Description: "Built-in IMAP/SMTP messaging adapter",
			AuthMethods: []messaging.AuthMethod{
				messaging.AuthMethodBasic,
				messaging.AuthMethodAppPassword,
			},
			Capabilities: messaging.Capabilities{
				ReceiveMessages:   true,
				SendMessages:      true,
				CreateDrafts:      true,
				UpdateDrafts:      true,
				DeleteDrafts:      true,
				ListConversations: true,
				ListMessages:      true,
				ListContainers:    true,
				Threading:         true,
				Polling:           true,
			},
		},
	})
}

func (s *service) handleValidateConfig(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ValidateConfigParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode validate config params: %v", err))
	}

	_, err := parseConfig(params.Connection, params.Credential)
	issues := make([]protocol.ValidationIssue, 0, 1)
	if err != nil {
		issues = append(issues, protocol.ValidationIssue{
			Severity: protocol.ValidationIssueError,
			Code:     "invalid_config",
			Message:  err.Error(),
		})
	}
	return resultResponse(req.ID, protocol.ValidateConfigResult{Issues: issues})
}

func (s *service) handleConnect(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ConnectParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode connect params: %v", err))
	}
	cfg, err := parseConfig(params.Connection, params.Credential)
	if err != nil {
		return errorResponse(req.ID, -32602, err.Error())
	}
	if err := s.verifyFunc(ctx, cfg); err != nil {
		return errorResponse(req.ID, -32001, err.Error())
	}
	identity := defaultIdentity(cfg)

	state := &connectionState{
		config:        cfg,
		connectedAt:   s.now(),
		identities:    []messaging.Identity{identity},
		conversations: make(map[messaging.ConversationID]messaging.Conversation),
		messages:      make(map[messaging.MessageID]messaging.Message),
	}
	s.mu.Lock()
	s.connections[params.Connection.ID] = state
	s.mu.Unlock()

	return resultResponse(req.ID, protocol.ConnectResult{
		Status:     messaging.ConnectionStatusConnected,
		Identities: append([]messaging.Identity(nil), state.identities...),
		Metadata: map[string]string{
			metaEmailAddress:       cfg.EmailAddress,
			metaDisplayName:        cfg.DisplayName,
			metaIMAPMailbox:        cfg.Mailbox,
			metaIMAPArchiveMailbox: cfg.ArchiveMailbox,
		},
	})
}

func (s *service) handleRefresh(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	var params protocol.RefreshParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode refresh params: %v", err))
	}
	connectParams := protocol.ConnectParams{
		Connection: params.Connection,
		Paths:      params.Paths,
		Credential: params.Credential,
	}
	raw, _ := json.Marshal(connectParams)
	req.Params = raw
	return s.handleConnect(ctx, req)
}

func (s *service) handleListIdentities(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ListIdentitiesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode list identities params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	return resultResponse(req.ID, protocol.ListIdentitiesResult{
		Identities: append([]messaging.Identity(nil), state.identities...),
	})
}

func (s *service) handleListConversations(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ListConversationsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode list conversations params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	items := make([]messaging.Conversation, 0, len(state.conversations))
	for _, conversation := range state.conversations {
		items = append(items, conversation)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})
	return resultResponse(req.ID, protocol.ListConversationsResult{Conversations: items})
}

func (s *service) handleListMessages(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ListMessagesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode list messages params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	items := make([]messaging.Message, 0)
	for _, message := range state.messages {
		if message.ConversationID == params.ConversationID {
			items = append(items, message)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	records := make([]protocol.MessageRecord, 0, len(items))
	for _, message := range items {
		records = append(records, s.messageRecord(state.config, message))
	}
	return resultResponse(req.ID, protocol.ListMessagesResult{Messages: records})
}

func (s *service) handleGetMessage(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.GetMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode get message params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	message, ok := state.messages[params.MessageID]
	if !ok {
		return errorResponse(req.ID, -32005, fmt.Sprintf("message %s not found", params.MessageID))
	}
	return resultResponse(req.ID, protocol.GetMessageResult{Message: s.messageRecord(state.config, message)})
}

func (s *service) handleListContainers(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ListContainersParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode list containers params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	return resultResponse(req.ID, protocol.ListContainersResult{
		Containers: containersForConfig(state.config),
	})
}

func (s *service) handleCreateDraft(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.CreateDraftParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode create draft params: %v", err))
	}
	draft := params.Draft.Draft
	if draft.Metadata == nil {
		draft.Metadata = map[string]string{}
	}
	draft.Metadata["native_draft"] = "false"
	return resultResponse(req.ID, protocol.CreateDraftResult{
		Draft: protocol.DraftRecord{
			Draft:       draft,
			Attachments: slices.Clone(params.Draft.Attachments),
		},
	})
}

func (s *service) handleUpdateDraft(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.UpdateDraftParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode update draft params: %v", err))
	}
	draft := params.Draft.Draft
	if draft.Metadata == nil {
		draft.Metadata = map[string]string{}
	}
	draft.Metadata["native_draft"] = "false"
	return resultResponse(req.ID, protocol.UpdateDraftResult{
		Draft: protocol.DraftRecord{
			Draft:       draft,
			Attachments: slices.Clone(params.Draft.Attachments),
		},
	})
}

func (s *service) handleDeleteDraft(req messagingruntime.Request) messagingruntime.Response {
	return resultResponse(req.ID, protocol.DeleteDraftResult{Deleted: true})
}

func (s *service) handlePoll(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	var params protocol.PollParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode poll params: %v", err))
	}
	state, ok := s.connection(params.ConnectionID)
	if !ok {
		return errorResponse(req.ID, -32004, fmt.Sprintf("connection %s is not connected", params.ConnectionID))
	}
	limit := params.Limit
	if limit <= 0 {
		limit = state.config.PollLimit
	}
	snapshot, err := s.pollFunc(ctx, state.config, params.Checkpoint, limit)
	if err != nil {
		return errorResponse(req.ID, -32002, err.Error())
	}

	s.mu.Lock()
	for _, conversation := range snapshot.Conversations {
		state.conversations[conversation.ID] = conversation
	}
	for _, message := range snapshot.Messages {
		state.messages[message.ID] = message
	}
	s.mu.Unlock()

	return resultResponse(req.ID, protocol.PollResult{
		Events:          snapshot.Events,
		Checkpoint:      snapshot.Checkpoint,
		PollAfterMillis: 30_000,
	})
}

func (s *service) handleSend(ctx context.Context, req messagingruntime.Request, reply bool) messagingruntime.Response {
	state, draft, attachments, headers, recipients, err := s.outboundInput(req, reply)
	if err != nil {
		return errorResponse(req.ID, -32602, err.Error())
	}
	if len(attachments) > 0 {
		return errorResponse(req.ID, -32006, "imap-smtp does not support outbound attachments yet")
	}
	snapshot, err := s.sendFunc(ctx, state.config, draft, recipients, headers)
	if err != nil {
		return errorResponse(req.ID, -32002, err.Error())
	}

	s.mu.Lock()
	state.messages[snapshot.Message.ID] = snapshot.Message
	if conversation, ok := state.conversations[snapshot.Message.ConversationID]; ok {
		if _, exists := state.conversations[conversation.ID]; !exists {
			state.conversations[conversation.ID] = conversation
		}
	}
	s.mu.Unlock()

	return resultResponse(req.ID, protocol.SendResult{
		Message: protocol.MessageRecord{Message: snapshot.Message},
		Status:  snapshot.Message.Status,
	})
}

func (s *service) handleHealth(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.HealthParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, -32602, fmt.Sprintf("decode health params: %v", err))
		}
	}
	status := protocol.HealthStatus{
		OK:        true,
		Status:    messaging.ConnectionStatusConnected,
		Message:   "ok",
		CheckedAt: s.now(),
	}
	if params.ConnectionID != "" {
		if _, ok := s.connection(params.ConnectionID); !ok {
			status.OK = false
			status.Status = messaging.ConnectionStatusUnknown
			status.Message = fmt.Sprintf("connection %s is not connected", params.ConnectionID)
		}
	}
	return resultResponse(req.ID, protocol.HealthResult{Health: status})
}

func (s *service) messageRecord(cfg adapterConfig, message messaging.Message) protocol.MessageRecord {
	return protocol.MessageRecord{
		Message:    message,
		Placements: []messaging.Placement{placementForMessage(cfg, message)},
	}
}

func (s *service) outboundInput(req messagingruntime.Request, reply bool) (*connectionState, messaging.Draft, []protocol.Attachment, outboundHeaders, []string, error) {
	var draft protocol.DraftRecord
	var replyToMessageID messaging.MessageID
	var replyToRemoteID string
	if reply {
		var params protocol.ReplyMessageParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, messaging.Draft{}, nil, outboundHeaders{}, nil, fmt.Errorf("decode reply params: %w", err)
		}
		draft = params.Draft
		replyToMessageID = params.ReplyToMessageID
		replyToRemoteID = params.ReplyToRemoteID
	} else {
		var params protocol.SendMessageParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, messaging.Draft{}, nil, outboundHeaders{}, nil, fmt.Errorf("decode send params: %w", err)
		}
		draft = params.Draft
	}

	state, ok := s.connection(draft.Draft.ConnectionID)
	if !ok {
		return nil, messaging.Draft{}, nil, outboundHeaders{}, nil, fmt.Errorf("connection %s is not connected", draft.Draft.ConnectionID)
	}

	recipients := recipientsForDraft(state, draft.Draft, replyToMessageID, replyToRemoteID)
	if len(recipients) == 0 {
		return nil, messaging.Draft{}, nil, outboundHeaders{}, nil, fmt.Errorf("no recipients resolved for draft %s", draft.Draft.ID)
	}
	headers := headersForDraft(state, draft.Draft, replyToMessageID, replyToRemoteID, reply)
	return state, draft.Draft, draft.Attachments, headers, recipients, nil
}

func (s *service) connection(id messaging.ConnectionID) (*connectionState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.connections[id]
	return state, ok
}

func resultResponse(id uint64, result any) messagingruntime.Response {
	body, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, -32603, fmt.Sprintf("marshal result: %v", err))
	}
	return messagingruntime.Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  body,
	}
}

func errorResponse(id uint64, code int, message string) messagingruntime.Response {
	return messagingruntime.Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &messagingruntime.ResponseError{
			Code:    code,
			Message: message,
		},
	}
}
