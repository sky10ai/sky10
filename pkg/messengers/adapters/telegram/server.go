package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

type connectionState struct {
	config        adapterConfig
	client        telegramAPI
	identity      messaging.Identity
	conversations map[messaging.ConversationID]messaging.Conversation
	messages      map[messaging.MessageID]protocol.MessageRecord
}

type service struct {
	logger        *slog.Logger
	now           func() time.Time
	clientFactory func(adapterConfig) (telegramAPI, error)

	mu          sync.RWMutex
	connections map[messaging.ConnectionID]*connectionState
}

func newServer() *service {
	return &service{
		logger:        slog.New(slog.NewTextHandler(os.Stderr, nil)),
		now:           func() time.Time { return time.Now().UTC() },
		clientFactory: newTelegramAPIClient,
		connections:   make(map[messaging.ConnectionID]*connectionState),
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
		if err := enc.Write(s.handle(ctx, req)); err != nil {
			return err
		}
	}
}

func (s *service) handle(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	switch req.Method {
	case string(protocol.MethodDescribe):
		return resultResponse(req.ID, protocol.DescribeResult{
			Protocol: protocol.CurrentProtocol(),
			Adapter:  adapterMeta,
		})
	case string(protocol.MethodValidateConfig):
		return s.handleValidateConfig(ctx, req)
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
	case string(protocol.MethodCreateDraft):
		return s.handleCreateDraft(req)
	case string(protocol.MethodUpdateDraft):
		return s.handleUpdateDraft(req)
	case string(protocol.MethodDeleteDraft):
		return resultResponse(req.ID, protocol.DeleteDraftResult{Deleted: true})
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

func (s *service) handleValidateConfig(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	var params protocol.ValidateConfigParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode validate config params: %v", err))
	}
	issues := make([]protocol.ValidationIssue, 0, 1)
	cfg, err := parseConfig(params.Connection, params.Paths, params.Credential)
	if err == nil {
		var client telegramAPI
		client, err = s.clientFactory(cfg)
		if err == nil {
			_, err = client.GetMe(ctx)
		}
	}
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
	cfg, err := parseConfig(params.Connection, params.Paths, params.Credential)
	if err != nil {
		return errorResponse(req.ID, -32602, err.Error())
	}
	client, err := s.clientFactory(cfg)
	if err != nil {
		return errorResponse(req.ID, -32001, err.Error())
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return errorResponse(req.ID, -32001, fmt.Sprintf("validate telegram bot token: %v", err))
	}
	if _, err := client.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{DropPendingUpdates: cfg.DropPendingOnConnect}); err != nil {
		return errorResponse(req.ID, -32001, fmt.Sprintf("delete telegram webhook: %v", err))
	}

	identity := identityFromBot(cfg.ConnectionID, me)
	state := &connectionState{
		config:        cfg,
		client:        client,
		identity:      identity,
		conversations: make(map[messaging.ConversationID]messaging.Conversation),
		messages:      make(map[messaging.MessageID]protocol.MessageRecord),
	}
	s.mu.Lock()
	s.connections[cfg.ConnectionID] = state
	s.mu.Unlock()

	return resultResponse(req.ID, protocol.ConnectResult{
		Status:     messaging.ConnectionStatusConnected,
		Identities: []messaging.Identity{identity},
		Metadata:   connectionMetadata(cfg, me),
	})
}

func (s *service) handleRefresh(ctx context.Context, req messagingruntime.Request) messagingruntime.Response {
	var params protocol.RefreshParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode refresh params: %v", err))
	}
	raw, _ := json.Marshal(protocol.ConnectParams{
		Connection: params.Connection,
		Paths:      params.Paths,
		Credential: params.Credential,
	})
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
	return resultResponse(req.ID, protocol.ListIdentitiesResult{Identities: []messaging.Identity{state.identity}})
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
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
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
	items := make([]protocol.MessageRecord, 0)
	for _, record := range state.messages {
		if record.Message.ConversationID == params.ConversationID {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := items[i].Message
		right := items[j].Message
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	return resultResponse(req.ID, protocol.ListMessagesResult{Messages: items})
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
	messageID := params.MessageID
	if messageID == "" && params.RemoteID != "" && params.ConversationID != "" {
		messageID = messageIDFor(params.ConnectionID, remoteChatIDFromConversationID(params.ConversationID), params.RemoteID)
	}
	record, ok := state.messages[messageID]
	if !ok {
		return errorResponse(req.ID, -32005, fmt.Sprintf("message %s not found", messageID))
	}
	return resultResponse(req.ID, protocol.GetMessageResult{Message: record})
}

func (s *service) handleCreateDraft(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.CreateDraftParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode create draft params: %v", err))
	}
	return resultResponse(req.ID, protocol.CreateDraftResult{Draft: params.Draft})
}

func (s *service) handleUpdateDraft(req messagingruntime.Request) messagingruntime.Response {
	var params protocol.UpdateDraftParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, -32602, fmt.Sprintf("decode update draft params: %v", err))
	}
	return resultResponse(req.ID, protocol.UpdateDraftResult{Draft: params.Draft})
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
	updates, err := state.client.GetUpdates(ctx, getUpdatesRequest{
		Offset:         checkpointOffset(params.Checkpoint),
		Limit:          clampTelegramPollLimit(limit),
		Timeout:        state.config.PollTimeoutSeconds,
		AllowedUpdates: []string{models.AllowedUpdateMessage, models.AllowedUpdateEditedMessage, models.AllowedUpdateChannelPost, models.AllowedUpdateEditedChannelPost},
	})
	if err != nil {
		return errorResponse(req.ID, -32002, err.Error())
	}

	events := make([]messaging.Event, 0, len(updates))
	nextOffset := checkpointOffset(params.Checkpoint)
	for _, update := range updates {
		if update.ID >= nextOffset {
			nextOffset = update.ID + 1
		}
		message, eventType := updateMessage(update)
		if message == nil {
			continue
		}
		conversation, record, err := normalizeMessage(ctx, state, message, eventType, s.now)
		if err != nil {
			return errorResponse(req.ID, -32002, err.Error())
		}
		s.mu.Lock()
		state.conversations[conversation.ID] = conversation
		state.messages[record.Message.ID] = record
		s.mu.Unlock()
		events = append(events, messaging.Event{
			ID:             messaging.EventID("event/" + string(record.Message.ID) + "/" + strconv.FormatInt(update.ID, 10)),
			Type:           eventType,
			ConnectionID:   state.config.ConnectionID,
			ConversationID: conversation.ID,
			MessageID:      record.Message.ID,
			Timestamp:      eventTimestamp(record.Message, s.now),
			Metadata: map[string]string{
				"telegram_update_id": strconv.FormatInt(update.ID, 10),
			},
		})
	}

	return resultResponse(req.ID, protocol.PollResult{
		Events: events,
		Checkpoint: &protocol.Checkpoint{
			Cursor:    strconv.FormatInt(nextOffset, 10),
			UpdatedAt: s.now(),
		},
		PollAfterMillis: 1_000,
	})
}

func (s *service) handleSend(ctx context.Context, req messagingruntime.Request, reply bool) messagingruntime.Response {
	state, draft, replyTo, err := s.sendInput(req, reply)
	if err != nil {
		return errorResponse(req.ID, -32602, err.Error())
	}
	text := draftText(draft)
	if text == "" {
		return errorResponse(req.ID, -32602, "telegram send requires a text message")
	}
	conversation, ok := state.conversations[draft.ConversationID]
	if !ok {
		return errorResponse(req.ID, -32602, fmt.Sprintf("conversation %s is not known to the Telegram adapter", draft.ConversationID))
	}
	params := &tgbot.SendMessageParams{
		ChatID: chatIDForSend(conversation.RemoteID),
		Text:   text,
	}
	if replyTo > 0 {
		params.ReplyParameters = &models.ReplyParameters{MessageID: replyTo}
	}
	sent, err := state.client.SendMessage(ctx, params)
	if err != nil {
		return errorResponse(req.ID, -32002, err.Error())
	}
	_, record, err := normalizeMessage(ctx, state, sent, messaging.EventTypeMessageReceived, s.now)
	if err != nil {
		return errorResponse(req.ID, -32002, err.Error())
	}
	record.Message.Direction = messaging.MessageDirectionOutbound
	record.Message.Status = messaging.MessageStatusSent

	s.mu.Lock()
	state.messages[record.Message.ID] = record
	s.mu.Unlock()

	return resultResponse(req.ID, protocol.SendResult{
		Message: record,
		Status:  record.Message.Status,
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

func (s *service) sendInput(req messagingruntime.Request, reply bool) (*connectionState, messaging.Draft, int, error) {
	var draft protocol.DraftRecord
	var replyRemoteID string
	if reply {
		var params protocol.ReplyMessageParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, messaging.Draft{}, 0, fmt.Errorf("decode reply params: %w", err)
		}
		draft = params.Draft
		replyRemoteID = firstNonEmpty(params.ReplyToRemoteID, draft.Draft.ReplyToRemoteID)
	} else {
		var params protocol.SendMessageParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, messaging.Draft{}, 0, fmt.Errorf("decode send params: %w", err)
		}
		draft = params.Draft
	}
	if len(draft.Attachments) > 0 {
		return nil, messaging.Draft{}, 0, fmt.Errorf("telegram outbound attachments are not supported yet")
	}
	state, ok := s.connection(draft.Draft.ConnectionID)
	if !ok {
		return nil, messaging.Draft{}, 0, fmt.Errorf("connection %s is not connected", draft.Draft.ConnectionID)
	}
	replyMessageID := 0
	if replyRemoteID != "" {
		parsed, err := strconv.Atoi(replyRemoteID)
		if err != nil || parsed <= 0 {
			return nil, messaging.Draft{}, 0, fmt.Errorf("reply remote id %q is not a Telegram message id", replyRemoteID)
		}
		replyMessageID = parsed
	}
	return state, draft.Draft, replyMessageID, nil
}

func (s *service) connection(id messaging.ConnectionID) (*connectionState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.connections[id]
	return state, ok
}

func checkpointOffset(checkpoint *protocol.Checkpoint) int64 {
	if checkpoint == nil || checkpoint.Cursor == "" {
		return 0
	}
	offset, err := strconv.ParseInt(checkpoint.Cursor, 10, 64)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

func updateMessage(update models.Update) (*models.Message, messaging.EventType) {
	switch {
	case update.Message != nil:
		return update.Message, messaging.EventTypeMessageReceived
	case update.ChannelPost != nil:
		return update.ChannelPost, messaging.EventTypeMessageReceived
	case update.EditedMessage != nil:
		return update.EditedMessage, messaging.EventTypeMessageUpdated
	case update.EditedChannelPost != nil:
		return update.EditedChannelPost, messaging.EventTypeMessageUpdated
	default:
		return nil, ""
	}
}

func eventTimestamp(message messaging.Message, now func() time.Time) time.Time {
	if message.EditedAt != nil {
		return *message.EditedAt
	}
	if !message.CreatedAt.IsZero() {
		return message.CreatedAt
	}
	return now()
}

func resultResponse(id uint64, result any) messagingruntime.Response {
	body, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, -32603, fmt.Sprintf("marshal result: %v", err))
	}
	return messagingruntime.Response{JSONRPC: "2.0", ID: id, Result: body}
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
