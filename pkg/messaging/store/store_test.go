package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

func TestStoreRoundTripAndListViews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewKVBackend(newMemoryKVStore(), "")
	store, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	connection := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnected,
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodOAuth2,
			CredentialRef: "secret://slack/work",
		},
		UpdatedAt: time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC),
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}

	defaultIdentity := messaging.Identity{
		ID:           "identity/work-bot",
		ConnectionID: connection.ID,
		Kind:         messaging.IdentityKindBot,
		RemoteID:     "U123",
		DisplayName:  "Work Bot",
		CanReceive:   true,
		CanSend:      true,
		IsDefault:    true,
	}
	secondaryIdentity := messaging.Identity{
		ID:           "identity/work-shared",
		ConnectionID: connection.ID,
		Kind:         messaging.IdentityKindAccount,
		Address:      "#board",
		DisplayName:  "Board Channel",
		CanReceive:   true,
	}
	if err := store.ReplaceConnectionIdentities(ctx, connection.ID, []messaging.Identity{secondaryIdentity, defaultIdentity}); err != nil {
		t.Fatalf("ReplaceConnectionIdentities() error = %v", err)
	}

	conversation := messaging.Conversation{
		ID:              "conv/latisha",
		ConnectionID:    connection.ID,
		LocalIdentityID: defaultIdentity.ID,
		Kind:            messaging.ConversationKindDirect,
		RemoteID:        "D123",
		Title:           "Latisha",
		Participants: []messaging.Participant{
			{Kind: messaging.ParticipantKindBot, IdentityID: defaultIdentity.ID, IsLocal: true},
			{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
		},
	}
	if err := store.PutConversation(ctx, conversation); err != nil {
		t.Fatalf("PutConversation() error = %v", err)
	}

	later := messaging.Message{
		ID:              "msg/later",
		ConnectionID:    connection.ID,
		ConversationID:  conversation.ID,
		LocalIdentityID: defaultIdentity.ID,
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "follow up"}},
		CreatedAt:       time.Date(2026, 4, 22, 8, 2, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
	}
	earlier := messaging.Message{
		ID:              "msg/earlier",
		ConnectionID:    connection.ID,
		ConversationID:  conversation.ID,
		LocalIdentityID: defaultIdentity.ID,
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "hello"}},
		CreatedAt:       time.Date(2026, 4, 22, 8, 1, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
	}
	if err := store.PutMessage(ctx, later); err != nil {
		t.Fatalf("PutMessage(later) error = %v", err)
	}
	if err := store.PutMessage(ctx, earlier); err != nil {
		t.Fatalf("PutMessage(earlier) error = %v", err)
	}

	draft := messaging.Draft{
		ID:              "draft/reply",
		ConnectionID:    connection.ID,
		ConversationID:  conversation.ID,
		LocalIdentityID: defaultIdentity.ID,
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindMarkdown, Text: "I can do that."}},
		Status:          messaging.DraftStatusApprovalRequired,
	}
	if err := store.PutDraft(ctx, draft); err != nil {
		t.Fatalf("PutDraft() error = %v", err)
	}

	approval := messaging.Approval{
		ID:           "approval/reply",
		ConnectionID: connection.ID,
		DraftID:      draft.ID,
		WorkflowID:   "wf/latisha",
		PolicyID:     "policy/board",
		Action:       "send_draft",
		Summary:      "Send reply to Latisha",
		Status:       messaging.ApprovalStatusPending,
		RequestedBy:  "runtime:hermes",
		RequestedAt:  time.Date(2026, 4, 22, 8, 3, 30, 0, time.UTC),
	}
	if err := store.PutApproval(ctx, approval); err != nil {
		t.Fatalf("PutApproval() error = %v", err)
	}

	policy := messaging.Policy{
		ID:   "policy/board",
		Name: "Board Slack",
		Rules: messaging.PolicyRules{
			ReadInbound:     true,
			CreateDrafts:    true,
			SendMessages:    true,
			RequireApproval: true,
			ReplyOnly:       true,
		},
	}
	if err := store.PutPolicy(ctx, policy); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}

	exposure := messaging.Exposure{
		ID:           "exposure/hermes",
		ConnectionID: connection.ID,
		SubjectID:    "hermes:operator",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		PolicyID:     policy.ID,
		Enabled:      true,
	}
	if err := store.PutExposure(ctx, exposure); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}

	workflow := messaging.Workflow{
		ID:                   "wf/latisha",
		Kind:                 "proactive_reply",
		Status:               messaging.WorkflowStatusAwaitingApproval,
		SourceConnectionID:   connection.ID,
		SourceIdentityID:     defaultIdentity.ID,
		SourceConversationID: conversation.ID,
		SourceMessageID:      earlier.ID,
		PolicyID:             policy.ID,
		ExposureID:           exposure.ID,
		Sender:               messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
		DraftID:              draft.ID,
		ApprovalID:           approval.ID,
		BrokerReceivedAt:     time.Date(2026, 4, 22, 8, 1, 5, 0, time.UTC),
		LastActivityAt:       time.Date(2026, 4, 22, 8, 3, 0, 0, time.UTC),
	}
	if err := store.PutWorkflow(ctx, workflow); err != nil {
		t.Fatalf("PutWorkflow() error = %v", err)
	}

	if err := store.AppendActivityEvent(ctx, messaging.ActivityEvent{
		ID:         "act/2",
		WorkflowID: workflow.ID,
		Type:       messaging.EventTypeDraftUpdated,
		OccurredAt: time.Date(2026, 4, 22, 8, 3, 0, 0, time.UTC),
		DraftID:    draft.ID,
	}); err != nil {
		t.Fatalf("AppendActivityEvent(act/2) error = %v", err)
	}
	if err := store.AppendActivityEvent(ctx, messaging.ActivityEvent{
		ID:         "act/1",
		WorkflowID: workflow.ID,
		Type:       messaging.EventTypeMessageReceived,
		OccurredAt: time.Date(2026, 4, 22, 8, 1, 5, 0, time.UTC),
		MessageID:  earlier.ID,
	}); err != nil {
		t.Fatalf("AppendActivityEvent(act/1) error = %v", err)
	}

	if err := store.AppendEvent(ctx, messaging.Event{
		ID:           "evt/2",
		Type:         messaging.EventTypeDraftUpdated,
		ConnectionID: connection.ID,
		DraftID:      draft.ID,
		Timestamp:    time.Date(2026, 4, 22, 8, 3, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent(evt/2) error = %v", err)
	}
	if err := store.AppendEvent(ctx, messaging.Event{
		ID:             "evt/1",
		Type:           messaging.EventTypeMessageReceived,
		ConnectionID:   connection.ID,
		ConversationID: conversation.ID,
		MessageID:      earlier.ID,
		Timestamp:      time.Date(2026, 4, 22, 8, 1, 5, 0, time.UTC),
	}); err != nil {
		t.Fatalf("AppendEvent(evt/1) error = %v", err)
	}

	checkpoint := protocol.Checkpoint{
		Cursor:    "cursor-2",
		Sequence:  "seq-9",
		UpdatedAt: time.Date(2026, 4, 22, 8, 4, 0, 0, time.UTC),
	}
	if err := store.PutCheckpoint(ctx, connection.ID, checkpoint); err != nil {
		t.Fatalf("PutCheckpoint() error = %v", err)
	}

	reloaded, err := NewStore(ctx, backend)
	if err != nil {
		t.Fatalf("NewStore(reload) error = %v", err)
	}

	connections := reloaded.ListConnections()
	if len(connections) != 1 || connections[0].ID != connection.ID {
		t.Fatalf("ListConnections() = %+v, want %s", connections, connection.ID)
	}

	identities := reloaded.ListConnectionIdentities(connection.ID)
	if len(identities) != 2 || identities[0].ID != defaultIdentity.ID {
		t.Fatalf("ListConnectionIdentities() = %+v, want default identity first", identities)
	}

	messages := reloaded.ListConversationMessages(conversation.ID)
	if len(messages) != 2 || messages[0].ID != earlier.ID || messages[1].ID != later.ID {
		t.Fatalf("ListConversationMessages() = %+v, want earlier then later", messages)
	}

	drafts := reloaded.ListConversationDrafts(conversation.ID)
	if len(drafts) != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("ListConversationDrafts() = %+v, want %s", drafts, draft.ID)
	}

	approvals := reloaded.ListDraftApprovals(draft.ID)
	if len(approvals) != 1 || approvals[0].ID != approval.ID {
		t.Fatalf("ListDraftApprovals() = %+v, want %s", approvals, approval.ID)
	}

	workflows := reloaded.ListWorkflows()
	if len(workflows) != 1 || workflows[0].ID != workflow.ID {
		t.Fatalf("ListWorkflows() = %+v, want %s", workflows, workflow.ID)
	}

	activity := reloaded.ListWorkflowActivity(workflow.ID)
	if len(activity) != 2 || activity[0].ID != "act/1" || activity[1].ID != "act/2" {
		t.Fatalf("ListWorkflowActivity() = %+v, want act/1 then act/2", activity)
	}

	events := reloaded.ListConnectionEvents(connection.ID)
	if len(events) != 2 || events[0].ID != "evt/1" || events[1].ID != "evt/2" {
		t.Fatalf("ListConnectionEvents() = %+v, want evt/1 then evt/2", events)
	}

	gotCheckpoint, ok := reloaded.GetCheckpoint(connection.ID)
	if !ok || gotCheckpoint.Cursor != checkpoint.Cursor {
		t.Fatalf("GetCheckpoint() = %+v, %v; want cursor %q", gotCheckpoint, ok, checkpoint.Cursor)
	}
}

func TestStoreReplaceConnectionIdentitiesRemovesStaleEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewStore(ctx, NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	connection := messaging.Connection{
		ID:        "gmail/work",
		AdapterID: "gmail",
		Label:     "Work Gmail",
		Status:    messaging.ConnectionStatusConnected,
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}

	first := messaging.Identity{ID: "identity/a", ConnectionID: connection.ID, Kind: messaging.IdentityKindEmail, Address: "a@example.com", CanReceive: true}
	second := messaging.Identity{ID: "identity/b", ConnectionID: connection.ID, Kind: messaging.IdentityKindEmail, Address: "b@example.com", CanReceive: true}
	replacement := messaging.Identity{ID: "identity/c", ConnectionID: connection.ID, Kind: messaging.IdentityKindEmail, Address: "c@example.com", CanReceive: true}

	if err := store.ReplaceConnectionIdentities(ctx, connection.ID, []messaging.Identity{first, second}); err != nil {
		t.Fatalf("ReplaceConnectionIdentities(initial) error = %v", err)
	}
	if err := store.ReplaceConnectionIdentities(ctx, connection.ID, []messaging.Identity{second, replacement}); err != nil {
		t.Fatalf("ReplaceConnectionIdentities(replace) error = %v", err)
	}

	if _, ok := store.GetIdentity(first.ID); ok {
		t.Fatal("GetIdentity(first) = present, want removed")
	}
	identities := store.ListConnectionIdentities(connection.ID)
	if len(identities) != 2 {
		t.Fatalf("ListConnectionIdentities() len = %d, want 2", len(identities))
	}
}

func TestStoreRejectsMismatchedIdentityReplacement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewStore(ctx, NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	err = store.ReplaceConnectionIdentities(ctx, "slack/work", []messaging.Identity{
		{ID: "identity/1", ConnectionID: "slack/other", Kind: messaging.IdentityKindBot, RemoteID: "U1"},
	})
	if err == nil {
		t.Fatal("ReplaceConnectionIdentities() error = nil, want mismatch failure")
	}
}

func TestStoreAppendEventsAreIdempotentByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := NewStore(ctx, NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	connection := messaging.Connection{
		ID:        "conn/1",
		AdapterID: "slack",
		Label:     "Slack",
		Status:    messaging.ConnectionStatusConnected,
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	workflow := messaging.Workflow{
		ID:                 "wf/1",
		Kind:               "draft_send",
		Status:             messaging.WorkflowStatusDrafted,
		SourceConnectionID: connection.ID,
		Sender:             messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U1", DisplayName: "Latisha"},
		BrokerReceivedAt:   now,
		LastActivityAt:     now,
	}
	if err := store.PutWorkflow(ctx, workflow); err != nil {
		t.Fatalf("PutWorkflow() error = %v", err)
	}

	event := messaging.Event{
		ID:           "evt/dup",
		Type:         messaging.EventTypeMessageReceived,
		ConnectionID: connection.ID,
		Timestamp:    now,
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent(first) error = %v", err)
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent(dup) error = %v", err)
	}
	if events := store.ListConnectionEvents(connection.ID); len(events) != 1 {
		t.Fatalf("ListConnectionEvents() len = %d, want 1", len(events))
	}

	activity := messaging.ActivityEvent{
		ID:         "act/dup",
		WorkflowID: workflow.ID,
		Type:       messaging.EventTypeDraftUpdated,
		OccurredAt: now,
	}
	if err := store.AppendActivityEvent(ctx, activity); err != nil {
		t.Fatalf("AppendActivityEvent(first) error = %v", err)
	}
	if err := store.AppendActivityEvent(ctx, activity); err != nil {
		t.Fatalf("AppendActivityEvent(dup) error = %v", err)
	}
	if activities := store.ListWorkflowActivity(workflow.ID); len(activities) != 1 {
		t.Fatalf("ListWorkflowActivity() len = %d, want 1", len(activities))
	}
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
