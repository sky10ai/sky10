package shim

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

func TestServiceScopesReadsDraftsAndSendRequestsToExposure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("broker.New() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	allowed := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnecting,
		Auth: messaging.AuthInfo{
			Method:          messaging.AuthMethodNone,
			ExternalAccount: "work@example.test",
		},
		DefaultPolicyID: "policy/allowed",
		Metadata: map[string]string{
			"credential_ref": "secret://slack/work",
			"display":        "safe display metadata",
			"token_hint":     "should-not-leak",
		},
	}
	allowedIdentityID := shimHelperIdentityID(allowed.ID)
	allowedInboxID := shimHelperInboxID(allowed.ID)
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/allowed",
		Name: "Allowed Runtime Policy",
		Rules: messaging.PolicyRules{
			ReadInbound:         true,
			CreateDrafts:        true,
			SendMessages:        true,
			RequireApproval:     true,
			ReplyOnly:           true,
			AllowedIdentityIDs:  []messaging.IdentityID{allowedIdentityID},
			ManageMessages:      true,
			AllowedContainerIDs: []messaging.ContainerID{allowedInboxID},
		},
	}); err != nil {
		t.Fatalf("PutPolicy(allowed) error = %v", err)
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
	if err := b.RegisterConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: allowed,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestShimHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_SHIM_ADAPTER=1"},
		},
	}); err != nil {
		t.Fatalf("RegisterConnection(allowed) error = %v", err)
	}
	if _, err := b.ConnectConnection(ctx, allowed.ID); err != nil {
		t.Fatalf("ConnectConnection(allowed) error = %v", err)
	}

	blocked := messaging.Connection{
		ID:              "slack/personal",
		AdapterID:       "slack",
		Label:           "Personal Slack",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: "policy/blocked",
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/blocked",
		Name: "Blocked Policy",
		Rules: messaging.PolicyRules{
			ReadInbound:  true,
			CreateDrafts: true,
			SendMessages: true,
		},
	}); err != nil {
		t.Fatalf("PutPolicy(blocked) error = %v", err)
	}
	if err := store.PutConnection(ctx, blocked); err != nil {
		t.Fatalf("PutConnection(blocked) error = %v", err)
	}
	if err := store.PutIdentity(ctx, messaging.Identity{
		ID:           shimHelperIdentityID(blocked.ID),
		ConnectionID: blocked.ID,
		Kind:         messaging.IdentityKindBot,
		RemoteID:     "blocked-bot",
		DisplayName:  "Blocked Bot",
		CanReceive:   true,
		CanSend:      true,
		IsDefault:    true,
	}); err != nil {
		t.Fatalf("PutIdentity(blocked) error = %v", err)
	}

	allowedConversationID := messaging.ConversationID("conv/work/latisha")
	blockedConversationID := messaging.ConversationID("conv/personal/latisha")
	if err := store.PutConversation(ctx, messaging.Conversation{
		ID:              allowedConversationID,
		ConnectionID:    allowed.ID,
		LocalIdentityID: allowedIdentityID,
		Kind:            messaging.ConversationKindDirect,
		RemoteID:        "D-work",
		Title:           "Latisha",
		Participants: []messaging.Participant{
			{Kind: messaging.ParticipantKindBot, IdentityID: allowedIdentityID, IsLocal: true},
			{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
		},
	}); err != nil {
		t.Fatalf("PutConversation(allowed) error = %v", err)
	}
	if err := store.PutConversation(ctx, messaging.Conversation{
		ID:              blockedConversationID,
		ConnectionID:    blocked.ID,
		LocalIdentityID: shimHelperIdentityID(blocked.ID),
		Kind:            messaging.ConversationKindDirect,
		RemoteID:        "D-personal",
		Title:           "Personal Latisha",
		Participants: []messaging.Participant{
			{Kind: messaging.ParticipantKindBot, IdentityID: shimHelperIdentityID(blocked.ID), IsLocal: true},
			{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
		},
	}); err != nil {
		t.Fatalf("PutConversation(blocked) error = %v", err)
	}
	if err := store.PutMessage(ctx, messaging.Message{
		ID:              "msg/work/1",
		ConnectionID:    allowed.ID,
		ConversationID:  allowedConversationID,
		LocalIdentityID: allowedIdentityID,
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "work message"}},
		CreatedAt:       time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
	}); err != nil {
		t.Fatalf("PutMessage(allowed) error = %v", err)
	}
	if err := store.PutMessage(ctx, messaging.Message{
		ID:              "msg/personal/1",
		ConnectionID:    blocked.ID,
		ConversationID:  blockedConversationID,
		LocalIdentityID: shimHelperIdentityID(blocked.ID),
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U-latisha", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "personal message"}},
		CreatedAt:       time.Date(2026, 4, 25, 12, 1, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
	}); err != nil {
		t.Fatalf("PutMessage(blocked) error = %v", err)
	}

	service, err := New(Config{
		Broker:     b,
		Store:      store,
		ExposureID: "exposure/hermes",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	connections, err := service.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections() error = %v", err)
	}
	if len(connections) != 1 || connections[0].ID != allowed.ID {
		t.Fatalf("ListConnections() = %+v, want only %s", connections, allowed.ID)
	}
	if connections[0].Auth.Configured() {
		t.Fatalf("ListConnections()[0].Auth = %+v, want sanitized", connections[0].Auth)
	}
	if _, ok := connections[0].Metadata["credential_ref"]; ok {
		t.Fatalf("credential_ref metadata leaked: %+v", connections[0].Metadata)
	}
	if _, ok := connections[0].Metadata["token_hint"]; ok {
		t.Fatalf("token_hint metadata leaked: %+v", connections[0].Metadata)
	}
	if connections[0].Metadata["display"] != "safe display metadata" {
		t.Fatalf("safe metadata missing after sanitize: %+v", connections[0].Metadata)
	}

	identities, err := service.ListIdentities(ctx, allowed.ID)
	if err != nil {
		t.Fatalf("ListIdentities(allowed) error = %v", err)
	}
	if len(identities) != 1 || identities[0].ID != allowedIdentityID {
		t.Fatalf("ListIdentities(allowed) = %+v, want %s", identities, allowedIdentityID)
	}
	if _, err := service.ListIdentities(ctx, blocked.ID); err == nil {
		t.Fatal("ListIdentities(blocked) error = nil, want exposure denial")
	}

	conversations, err := service.ListConversations(ctx, allowed.ID)
	if err != nil {
		t.Fatalf("ListConversations(allowed) error = %v", err)
	}
	if len(conversations) != 1 || conversations[0].ID != allowedConversationID {
		t.Fatalf("ListConversations(allowed) = %+v, want %s", conversations, allowedConversationID)
	}
	if _, err := service.ListConversations(ctx, blocked.ID); err == nil {
		t.Fatal("ListConversations(blocked) error = nil, want exposure denial")
	}

	messages, err := service.GetMessages(ctx, allowed.ID, allowedConversationID)
	if err != nil {
		t.Fatalf("GetMessages(allowed) error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "msg/work/1" {
		t.Fatalf("GetMessages(allowed) = %+v, want msg/work/1", messages)
	}
	if conversation, ok, err := service.GetConversation(ctx, allowed.ID, blockedConversationID); err != nil || ok {
		t.Fatalf("GetConversation(allowed, blocked conversation) = %+v, %v, %v; want not found without error", conversation, ok, err)
	}
	crossConnectionMessages, err := service.GetMessages(ctx, allowed.ID, blockedConversationID)
	if err != nil {
		t.Fatalf("GetMessages(allowed, blocked conversation) error = %v", err)
	}
	if len(crossConnectionMessages) != 0 {
		t.Fatalf("GetMessages(allowed, blocked conversation) = %+v, want empty", crossConnectionMessages)
	}
	if _, err := service.GetMessages(ctx, blocked.ID, blockedConversationID); err == nil {
		t.Fatal("GetMessages(blocked) error = nil, want exposure denial")
	}

	containers, err := service.ListContainers(ctx, protocol.ListContainersParams{ConnectionID: allowed.ID})
	if err != nil {
		t.Fatalf("ListContainers(allowed) error = %v", err)
	}
	if len(containers.Containers) != 1 || containers.Containers[0].ID != allowedInboxID {
		t.Fatalf("ListContainers(allowed) = %+v, want only %s", containers.Containers, allowedInboxID)
	}

	draft := messaging.Draft{
		ID:              "draft/work/reply",
		ConnectionID:    allowed.ID,
		ConversationID:  allowedConversationID,
		LocalIdentityID: allowedIdentityID,
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "I can do that."}},
		Status:          messaging.DraftStatusPending,
	}
	draftResult, err := service.CreateDraft(ctx, draft)
	if err != nil {
		t.Fatalf("CreateDraft(allowed) error = %v", err)
	}
	if draftResult.Draft.ID != draft.ID || draftResult.Draft.Metadata["native_draft"] != "true" {
		t.Fatalf("CreateDraft(allowed) = %+v, want native draft", draftResult.Draft)
	}
	if _, err := service.CreateDraft(ctx, draft); err == nil {
		t.Fatal("CreateDraft(duplicate) error = nil, want duplicate draft denial")
	}
	updatedDraft := draft
	updatedDraft.Parts = []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "Updated draft."}}
	updateResult, err := service.UpdateDraft(ctx, updatedDraft)
	if err != nil {
		t.Fatalf("UpdateDraft(allowed) error = %v", err)
	}
	if updateResult.Draft.ID != draft.ID || updateResult.Draft.Metadata["native_draft"] != "true" {
		t.Fatalf("UpdateDraft(allowed) = %+v, want native draft", updateResult.Draft)
	}
	if _, err := service.UpdateDraft(ctx, messaging.Draft{
		ID:              "draft/work/missing",
		ConnectionID:    allowed.ID,
		ConversationID:  allowedConversationID,
		LocalIdentityID: allowedIdentityID,
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "missing"}},
		Status:          messaging.DraftStatusPending,
	}); err == nil {
		t.Fatal("UpdateDraft(missing) error = nil, want missing draft denial")
	}
	if _, err := service.UpdateDraft(ctx, messaging.Draft{
		ID:              draft.ID,
		ConnectionID:    blocked.ID,
		ConversationID:  blockedConversationID,
		LocalIdentityID: shimHelperIdentityID(blocked.ID),
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "nope"}},
		Status:          messaging.DraftStatusPending,
	}); err == nil {
		t.Fatal("UpdateDraft(blocked) error = nil, want exposure denial")
	}
	if _, err := service.CreateDraft(ctx, messaging.Draft{
		ID:              "draft/blocked/reply",
		ConnectionID:    blocked.ID,
		ConversationID:  blockedConversationID,
		LocalIdentityID: shimHelperIdentityID(blocked.ID),
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "nope"}},
		Status:          messaging.DraftStatusPending,
	}); err == nil {
		t.Fatal("CreateDraft(blocked) error = nil, want exposure denial")
	}

	sendResult, err := service.RequestSend(ctx, draft.ID, false)
	if err != nil {
		t.Fatalf("RequestSend() error = %v", err)
	}
	if sendResult.Approval == nil || sendResult.Approval.Status != messaging.ApprovalStatusPending {
		t.Fatalf("RequestSend() approval = %+v, want pending approval", sendResult.Approval)
	}
	if sendResult.Message != nil {
		t.Fatalf("RequestSend() message = %+v, want nil because policy requires approval", sendResult.Message)
	}
}

func TestServiceRejectsReadsDeniedByPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("broker.New() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	connection := messaging.Connection{
		ID:              "email/work",
		AdapterID:       "imap-smtp",
		Label:           "Work Email",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: "policy/no-read",
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/no-read",
		Name: "No Read Policy",
		Rules: messaging.PolicyRules{
			ReadInbound:  false,
			CreateDrafts: true,
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}
	if err := store.PutExposure(ctx, messaging.Exposure{
		ID:           "exposure/no-read",
		ConnectionID: connection.ID,
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		PolicyID:     "policy/no-read",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}

	service, err := New(Config{
		Broker:     b,
		Store:      store,
		ExposureID: "exposure/no-read",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := service.ListConversations(ctx, connection.ID); err == nil || !strings.Contains(err.Error(), "read denied") {
		t.Fatalf("ListConversations() error = %v, want read denied", err)
	}
}

func TestShimHelperMessagingAdapterProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MESSAGING_SHIM_ADAPTER") != "1" {
		return
	}
	if err := runShimHelperMessagingAdapter(); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func runShimHelperMessagingAdapter() error {
	dec := messagingruntime.NewDecoder(os.Stdin)
	enc := messagingruntime.NewEncoder(os.Stdout)
	for {
		var req messagingruntime.Request
		if err := dec.Read(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch req.Method {
		case string(protocol.MethodDescribe):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.DescribeResult{
					Protocol: protocol.CurrentProtocol(),
					Adapter: messaging.Adapter{
						ID:          "shim-test",
						DisplayName: "Shim Test",
						Capabilities: messaging.Capabilities{
							CreateDrafts:   true,
							SendMessages:   true,
							ListContainers: true,
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodConnect):
			var params protocol.ConnectParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ConnectResult{
					Status: messaging.ConnectionStatusConnected,
					Identities: []messaging.Identity{{
						ID:           shimHelperIdentityID(params.Connection.ID),
						ConnectionID: params.Connection.ID,
						Kind:         messaging.IdentityKindBot,
						RemoteID:     "B-" + shimHelperSlug(params.Connection.ID),
						DisplayName:  "Shim Bot",
						CanReceive:   true,
						CanSend:      true,
						IsDefault:    true,
					}},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodListContainers):
			var params protocol.ListContainersParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ListContainersResult{
					Containers: []messaging.Container{
						{
							ID:           shimHelperInboxID(params.ConnectionID),
							ConnectionID: params.ConnectionID,
							Kind:         messaging.ContainerKindInbox,
							Name:         "Inbox",
							RemoteID:     "inbox",
						},
						{
							ID:           messaging.ContainerID("container/" + shimHelperSlug(params.ConnectionID) + "/archive"),
							ConnectionID: params.ConnectionID,
							Kind:         messaging.ContainerKindArchive,
							Name:         "Archive",
							RemoteID:     "archive",
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodCreateDraft):
			var params protocol.CreateDraftParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			draft := params.Draft.Draft
			if draft.Metadata == nil {
				draft.Metadata = map[string]string{}
			}
			draft.Metadata["native_draft"] = "true"
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.CreateDraftResult{
					Draft: protocol.DraftRecord{Draft: draft},
				}),
			}); err != nil {
				return err
			}
		default:
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &messagingruntime.ResponseError{
					Code:    -32601,
					Message: "method not found",
				},
			}); err != nil {
				return err
			}
		}
	}
}

func helperProcessExecutableForTests() string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return exe
}

func shimHelperIdentityID(connectionID messaging.ConnectionID) messaging.IdentityID {
	return messaging.IdentityID("identity/" + shimHelperSlug(connectionID))
}

func shimHelperInboxID(connectionID messaging.ConnectionID) messaging.ContainerID {
	return messaging.ContainerID("container/" + shimHelperSlug(connectionID) + "/inbox")
}

func shimHelperSlug(connectionID messaging.ConnectionID) string {
	slug := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-").Replace(string(connectionID))
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "unknown"
	}
	return slug
}

func mustJSON(v any) json.RawMessage {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
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
