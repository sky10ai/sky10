package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingshim "github.com/sky10/sky10/pkg/messaging/shim"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

func TestHandlerDispatchesShimMethods(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := &recordingService{
		connections: []messaging.Connection{{ID: "slack/work", AdapterID: "slack", Label: "Work Slack"}},
		identities:  []messaging.Identity{{ID: "identity/work", ConnectionID: "slack/work", Kind: messaging.IdentityKindBot}},
		conversations: []messaging.Conversation{{
			ID:              "conv/work",
			ConnectionID:    "slack/work",
			LocalIdentityID: "identity/work",
			Kind:            messaging.ConversationKindDirect,
		}},
		messages: []messaging.Message{{ID: "msg/work", ConnectionID: "slack/work", ConversationID: "conv/work", LocalIdentityID: "identity/work"}},
		identityHits: []protocol.IdentitySearchHit{{
			Participant: messaging.Participant{Kind: messaging.ParticipantKindUser, DisplayName: "Latisha"},
		}},
	}
	handler := NewHandler(Config{Service: service})

	if _, _, handled := handler.Dispatch(ctx, "messaging.adapters", nil); handled {
		t.Fatal("Dispatch(non-shim namespace) handled = true, want false")
	}

	listRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodListConnections), nil)
	if err != nil || !handled {
		t.Fatalf("Dispatch(list connections) = %v, %v; want handled without error", err, handled)
	}
	list := listRaw.(listConnectionsResult)
	if list.Count != 1 || list.Connections[0].ID != "slack/work" {
		t.Fatalf("list connections = %+v, want slack/work", list)
	}

	identitiesRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodListIdentities), mustJSON(t, connectionParams{
		ConnectionID: "slack/work",
	}))
	if err != nil || !handled {
		t.Fatalf("Dispatch(list identities) = %v, %v; want handled without error", err, handled)
	}
	if got := identitiesRaw.(listIdentitiesResult); got.Count != 1 || got.Identities[0].ConnectionID != "slack/work" {
		t.Fatalf("list identities = %+v, want one work identity", got)
	}

	messagesRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodGetMessages), mustJSON(t, conversationParams{
		ConnectionID:   "slack/work",
		ConversationID: "conv/work",
	}))
	if err != nil || !handled {
		t.Fatalf("Dispatch(get messages) = %v, %v; want handled without error", err, handled)
	}
	if got := messagesRaw.(getMessagesResult); got.Count != 1 || got.Messages[0].ID != "msg/work" {
		t.Fatalf("get messages = %+v, want msg/work", got)
	}

	searchRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodSearchIdentities), mustJSON(t, protocol.SearchIdentitiesParams{
		ConnectionID: "slack/work",
		Query:        "Latisha",
	}))
	if err != nil || !handled {
		t.Fatalf("Dispatch(search identities) = %v, %v; want handled without error", err, handled)
	}
	if got := searchRaw.(protocol.SearchIdentitiesResult); got.Count != 1 || got.Hits[0].Participant.DisplayName != "Latisha" {
		t.Fatalf("search identities = %+v, want Latisha", got)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodSearchMessages), mustJSON(t, protocol.SearchMessagesParams{
		ConnectionID: "slack/work",
		Query:        "hello",
	})); err != nil || !handled {
		t.Fatalf("Dispatch(search messages) = %v, %v; want handled without error", err, handled)
	}

	draft := messaging.Draft{
		ID:              "draft/work",
		ConnectionID:    "slack/work",
		ConversationID:  "conv/work",
		LocalIdentityID: "identity/work",
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "yes"}},
		Status:          messaging.DraftStatusPending,
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodCreateDraft), mustJSON(t, draftParams{Draft: draft})); err != nil || !handled {
		t.Fatalf("Dispatch(create draft) = %v, %v; want handled without error", err, handled)
	}
	if service.lastMethod != messagingshim.MethodCreateDraft {
		t.Fatalf("last method = %s, want %s", service.lastMethod, messagingshim.MethodCreateDraft)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodUpdateDraft), mustJSON(t, draftParams{Draft: draft})); err != nil || !handled {
		t.Fatalf("Dispatch(update draft) = %v, %v; want handled without error", err, handled)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodRequestSend), mustJSON(t, requestSendParams{
		DraftID: "draft/work",
	})); err != nil || !handled {
		t.Fatalf("Dispatch(request send) = %v, %v; want handled without error", err, handled)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodApplyLabels), mustJSON(t, protocol.ApplyLabelsParams{
		ConnectionID:   "slack/work",
		ConversationID: "conv/work",
		Add:            []messaging.ContainerID{"container/work/inbox"},
	})); err != nil || !handled {
		t.Fatalf("Dispatch(apply labels) = %v, %v; want handled without error", err, handled)
	}

	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodSubscribeEvents), nil); err == nil || !handled {
		t.Fatalf("Dispatch(subscribe events) = %v, %v; want reserved method error", err, handled)
	}
	if _, err, handled := handler.Dispatch(ctx, "messaging.shim.nope", nil); err == nil || !handled {
		t.Fatalf("Dispatch(unknown shim method) = %v, %v; want handled error", err, handled)
	}
}

func TestHandlerPreservesExposureBoundaryAndApproval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	broker, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("broker.New() error = %v", err)
	}
	t.Cleanup(func() { _ = broker.Close() })

	allowed := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnected,
		Auth: messaging.AuthInfo{
			Method:          messaging.AuthMethodNone,
			ExternalAccount: "work@example.test",
		},
		DefaultPolicyID: "policy/allowed",
		Metadata: map[string]string{
			"display":        "safe",
			"credential_ref": "secret://slack/work",
			"token_hint":     "redacted",
		},
	}
	blocked := messaging.Connection{
		ID:              "slack/personal",
		AdapterID:       "slack",
		Label:           "Personal Slack",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: "policy/blocked",
	}
	if err := store.PutConnection(ctx, allowed); err != nil {
		t.Fatalf("PutConnection(allowed) error = %v", err)
	}
	if err := store.PutConnection(ctx, blocked); err != nil {
		t.Fatalf("PutConnection(blocked) error = %v", err)
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/allowed",
		Name: "Allowed Runtime Policy",
		Rules: messaging.PolicyRules{
			ReadInbound:        true,
			CreateDrafts:       true,
			SendMessages:       true,
			RequireApproval:    true,
			ReplyOnly:          true,
			AllowedIdentityIDs: []messaging.IdentityID{"identity/work"},
		},
	}); err != nil {
		t.Fatalf("PutPolicy(allowed) error = %v", err)
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/blocked",
		Name: "Blocked Runtime Policy",
		Rules: messaging.PolicyRules{
			ReadInbound:  true,
			CreateDrafts: true,
			SendMessages: true,
		},
	}); err != nil {
		t.Fatalf("PutPolicy(blocked) error = %v", err)
	}
	if err := store.PutExposure(ctx, messaging.Exposure{
		ID:           "exposure/hermes",
		ConnectionID: allowed.ID,
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		PolicyID:     "policy/allowed",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}

	for _, identity := range []messaging.Identity{
		{ID: "identity/work", ConnectionID: allowed.ID, Kind: messaging.IdentityKindBot, RemoteID: "B-work", DisplayName: "Work Bot", CanReceive: true, CanSend: true, IsDefault: true},
		{ID: "identity/personal", ConnectionID: blocked.ID, Kind: messaging.IdentityKindBot, RemoteID: "B-personal", DisplayName: "Personal Bot", CanReceive: true, CanSend: true, IsDefault: true},
	} {
		if err := store.PutIdentity(ctx, identity); err != nil {
			t.Fatalf("PutIdentity(%s) error = %v", identity.ID, err)
		}
	}
	for _, conversation := range []messaging.Conversation{
		{
			ID:              "conv/work",
			ConnectionID:    allowed.ID,
			LocalIdentityID: "identity/work",
			Kind:            messaging.ConversationKindDirect,
			Participants: []messaging.Participant{
				{Kind: messaging.ParticipantKindBot, IdentityID: "identity/work", IsLocal: true},
				{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
			},
		},
		{
			ID:              "conv/personal",
			ConnectionID:    blocked.ID,
			LocalIdentityID: "identity/personal",
			Kind:            messaging.ConversationKindDirect,
			Participants: []messaging.Participant{
				{Kind: messaging.ParticipantKindBot, IdentityID: "identity/personal", IsLocal: true},
				{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
			},
		},
	} {
		if err := store.PutConversation(ctx, conversation); err != nil {
			t.Fatalf("PutConversation(%s) error = %v", conversation.ID, err)
		}
	}
	if err := store.PutMessage(ctx, messaging.Message{
		ID:              "msg/work",
		ConnectionID:    allowed.ID,
		ConversationID:  "conv/work",
		LocalIdentityID: "identity/work",
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "hello"}},
		CreatedAt:       time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
	}); err != nil {
		t.Fatalf("PutMessage(allowed) error = %v", err)
	}
	for _, draft := range []messaging.Draft{
		{
			ID:              "draft/work",
			ConnectionID:    allowed.ID,
			ConversationID:  "conv/work",
			LocalIdentityID: "identity/work",
			Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "I can do that."}},
			Status:          messaging.DraftStatusPending,
		},
		{
			ID:              "draft/personal",
			ConnectionID:    blocked.ID,
			ConversationID:  "conv/personal",
			LocalIdentityID: "identity/personal",
			Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "nope"}},
			Status:          messaging.DraftStatusPending,
		},
	} {
		if err := store.PutDraft(ctx, draft); err != nil {
			t.Fatalf("PutDraft(%s) error = %v", draft.ID, err)
		}
	}

	service, err := messagingshim.New(messagingshim.Config{
		Broker:     broker,
		Store:      store,
		ExposureID: "exposure/hermes",
	})
	if err != nil {
		t.Fatalf("shim.New() error = %v", err)
	}
	handler := NewHandler(Config{Service: service})

	connectionsRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodListConnections), nil)
	if err != nil || !handled {
		t.Fatalf("Dispatch(list connections) = %v, %v; want handled without error", err, handled)
	}
	connections := connectionsRaw.(listConnectionsResult)
	if connections.Count != 1 || connections.Connections[0].ID != allowed.ID {
		t.Fatalf("connections = %+v, want only %s", connections, allowed.ID)
	}
	if connections.Connections[0].Auth.Configured() {
		t.Fatalf("connection auth leaked: %+v", connections.Connections[0].Auth)
	}
	if _, ok := connections.Connections[0].Metadata["credential_ref"]; ok {
		t.Fatalf("credential metadata leaked: %+v", connections.Connections[0].Metadata)
	}
	if _, ok := connections.Connections[0].Metadata["token_hint"]; ok {
		t.Fatalf("token metadata leaked: %+v", connections.Connections[0].Metadata)
	}

	messagesRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodGetMessages), mustJSON(t, conversationParams{
		ConnectionID:   allowed.ID,
		ConversationID: "conv/work",
	}))
	if err != nil || !handled {
		t.Fatalf("Dispatch(get allowed messages) = %v, %v; want handled without error", err, handled)
	}
	if messages := messagesRaw.(getMessagesResult); messages.Count != 1 || messages.Messages[0].ID != "msg/work" {
		t.Fatalf("messages = %+v, want msg/work", messages)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodGetMessages), mustJSON(t, conversationParams{
		ConnectionID:   blocked.ID,
		ConversationID: "conv/personal",
	})); err == nil || !handled {
		t.Fatalf("Dispatch(get blocked messages) = %v, %v; want exposure denial", err, handled)
	}

	sendRaw, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodRequestSend), mustJSON(t, requestSendParams{
		DraftID: "draft/work",
	}))
	if err != nil || !handled {
		t.Fatalf("Dispatch(request send) = %v, %v; want handled without error", err, handled)
	}
	send := sendRaw.(messagingbroker.RequestSendDraftResult)
	if send.Approval == nil || send.Approval.Status != messaging.ApprovalStatusPending {
		t.Fatalf("request send approval = %+v, want pending approval", send.Approval)
	}
	if send.Message != nil {
		t.Fatalf("request send message = %+v, want nil because policy requires approval", send.Message)
	}
	if _, err, handled := handler.Dispatch(ctx, string(messagingshim.MethodRequestSend), mustJSON(t, requestSendParams{
		DraftID: "draft/personal",
	})); err == nil || !handled {
		t.Fatalf("Dispatch(request blocked send) = %v, %v; want exposure denial", err, handled)
	}
}

func TestHandlerRejectsInvalidParams(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{Service: &recordingService{}})
	if _, err, handled := handler.Dispatch(context.Background(), string(messagingshim.MethodListIdentities), mustJSON(t, map[string]string{})); err == nil || !handled || !strings.Contains(err.Error(), "connection_id") {
		t.Fatalf("Dispatch(missing connection_id) = %v, %v; want connection_id error", err, handled)
	}
	if _, err, handled := handler.Dispatch(context.Background(), string(messagingshim.MethodSearchMessages), mustJSON(t, protocol.SearchMessagesParams{
		ConnectionID: "slack/work",
	})); err == nil || !handled || !strings.Contains(err.Error(), "query") {
		t.Fatalf("Dispatch(missing search query) = %v, %v; want query error", err, handled)
	}
	if _, err, handled := handler.Dispatch(context.Background(), string(messagingshim.MethodCreateDraft), mustJSON(t, draftParams{})); err == nil || !handled || !strings.Contains(err.Error(), "draft id") {
		t.Fatalf("Dispatch(invalid draft) = %v, %v; want draft validation error", err, handled)
	}
	if _, err, handled := (*Handler)(nil).Dispatch(context.Background(), string(messagingshim.MethodListConnections), nil); err == nil || !handled || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Dispatch(nil handler) = %v, %v; want configuration error", err, handled)
	}
}

type recordingService struct {
	lastMethod    messagingshim.Method
	connections   []messaging.Connection
	identities    []messaging.Identity
	conversations []messaging.Conversation
	messages      []messaging.Message
	identityHits  []protocol.IdentitySearchHit
}

func (s *recordingService) ListConnections(context.Context) ([]messaging.Connection, error) {
	s.lastMethod = messagingshim.MethodListConnections
	return s.connections, nil
}

func (s *recordingService) ListIdentities(_ context.Context, _ messaging.ConnectionID) ([]messaging.Identity, error) {
	s.lastMethod = messagingshim.MethodListIdentities
	return s.identities, nil
}

func (s *recordingService) ListConversations(_ context.Context, _ messaging.ConnectionID) ([]messaging.Conversation, error) {
	s.lastMethod = messagingshim.MethodListConversations
	return s.conversations, nil
}

func (s *recordingService) GetConversation(_ context.Context, _ messaging.ConnectionID, conversationID messaging.ConversationID) (messaging.Conversation, bool, error) {
	s.lastMethod = messagingshim.MethodGetConversation
	for _, conversation := range s.conversations {
		if conversation.ID == conversationID {
			return conversation, true, nil
		}
	}
	return messaging.Conversation{}, false, nil
}

func (s *recordingService) GetMessages(_ context.Context, _ messaging.ConnectionID, _ messaging.ConversationID) ([]messaging.Message, error) {
	s.lastMethod = messagingshim.MethodGetMessages
	return s.messages, nil
}

func (s *recordingService) ListContainers(context.Context, protocol.ListContainersParams) (protocol.ListContainersResult, error) {
	s.lastMethod = messagingshim.MethodListContainers
	return protocol.ListContainersResult{}, nil
}

func (s *recordingService) SearchIdentities(context.Context, protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error) {
	s.lastMethod = messagingshim.MethodSearchIdentities
	return protocol.SearchIdentitiesResult{Hits: s.identityHits, Count: len(s.identityHits)}, nil
}

func (s *recordingService) SearchConversations(context.Context, protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error) {
	s.lastMethod = messagingshim.MethodSearchConversations
	hits := make([]protocol.ConversationSearchHit, 0, len(s.conversations))
	for _, conversation := range s.conversations {
		hits = append(hits, protocol.ConversationSearchHit{Conversation: conversation})
	}
	return protocol.SearchConversationsResult{Hits: hits, Count: len(hits)}, nil
}

func (s *recordingService) SearchMessages(context.Context, protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	s.lastMethod = messagingshim.MethodSearchMessages
	hits := make([]protocol.MessageSearchHit, 0, len(s.messages))
	for _, message := range s.messages {
		hits = append(hits, protocol.MessageSearchHit{Message: protocol.MessageRecord{Message: message}})
	}
	return protocol.SearchMessagesResult{Hits: hits, Count: len(hits)}, nil
}

func (s *recordingService) CreateDraft(_ context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	s.lastMethod = messagingshim.MethodCreateDraft
	return messagingbroker.DraftMutationResult{Draft: draft}, nil
}

func (s *recordingService) UpdateDraft(_ context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	s.lastMethod = messagingshim.MethodUpdateDraft
	return messagingbroker.DraftMutationResult{Draft: draft}, nil
}

func (s *recordingService) RequestSend(_ context.Context, draftID messaging.DraftID, _ bool) (messagingbroker.RequestSendDraftResult, error) {
	s.lastMethod = messagingshim.MethodRequestSend
	return messagingbroker.RequestSendDraftResult{Draft: messaging.Draft{ID: draftID}}, nil
}

func (s *recordingService) MoveMessages(context.Context, protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodMoveMessages
	return protocol.ManageMessagesResult{}, nil
}

func (s *recordingService) MoveConversation(context.Context, protocol.MoveConversationParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodMoveConversation
	return protocol.ManageMessagesResult{}, nil
}

func (s *recordingService) ArchiveMessages(context.Context, protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodArchiveMessages
	return protocol.ManageMessagesResult{}, nil
}

func (s *recordingService) ArchiveConversation(context.Context, protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodArchiveConversation
	return protocol.ManageMessagesResult{}, nil
}

func (s *recordingService) ApplyLabels(context.Context, protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodApplyLabels
	return protocol.ManageMessagesResult{}, nil
}

func (s *recordingService) MarkRead(context.Context, protocol.MarkReadParams) (protocol.ManageMessagesResult, error) {
	s.lastMethod = messagingshim.MethodMarkRead
	return protocol.ManageMessagesResult{}, nil
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

type memoryKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryKVStore() *memoryKVStore {
	return &memoryKVStore{data: make(map[string][]byte)}
}

func (s *memoryKVStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return append([]byte(nil), value...), ok
}

func (s *memoryKVStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *memoryKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}
