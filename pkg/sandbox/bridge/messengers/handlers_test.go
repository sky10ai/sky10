package messengers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func TestHandleListConnectionsStampsAgentID(t *testing.T) {
	t.Parallel()

	backend := &fakeMessengerBackend{
		connections: []messaging.Connection{{
			ID:        "telegram/main",
			AdapterID: "telegram",
			Label:     "Telegram",
			Status:    messaging.ConnectionStatusConnected,
		}},
	}
	h := &handlers{backend: backend}

	resp, err := h.handleListConnections(context.Background(), bridge.Envelope{
		AgentID: "agent/real",
		Payload: json.RawMessage(`{"agent_id":"agent/fake","adapter_id":"telegram"}`),
	})
	if err != nil {
		t.Fatalf("handleListConnections() error = %v", err)
	}
	if backend.listConnections.AgentID != "agent/real" {
		t.Fatalf("AgentID = %q, want transport-stamped agent", backend.listConnections.AgentID)
	}
	if backend.listConnections.AdapterID != "telegram" {
		t.Fatalf("AdapterID = %q, want telegram", backend.listConnections.AdapterID)
	}
	var got listConnectionsResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response decode: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].ID != "telegram/main" {
		t.Fatalf("connections = %+v, want telegram/main", got.Connections)
	}
}

func TestHandleGetMessagesReturnsBackendMessages(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC)
	backend := &fakeMessengerBackend{
		messages: []messaging.Message{{
			ID:              "msg/1",
			ConnectionID:    "telegram/main",
			ConversationID:  "chat/1",
			LocalIdentityID: "identity/bot",
			Direction:       messaging.MessageDirectionInbound,
			Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "123"},
			Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "hello"}},
			CreatedAt:       createdAt,
			Status:          messaging.MessageStatusReceived,
		}},
	}
	h := &handlers{backend: backend}

	resp, err := h.handleGetMessages(context.Background(), bridge.Envelope{
		AgentID: "agent/real",
		Payload: json.RawMessage(`{"connection_id":"telegram/main","conversation_id":"chat/1"}`),
	})
	if err != nil {
		t.Fatalf("handleGetMessages() error = %v", err)
	}
	if backend.getMessages.AgentID != "agent/real" {
		t.Fatalf("AgentID = %q, want transport-stamped agent", backend.getMessages.AgentID)
	}
	var got getMessagesResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response decode: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("messages = %+v, want hello message", got.Messages)
	}
}

func TestHandleSearchMessagesStampsAgentID(t *testing.T) {
	t.Parallel()

	backend := &fakeMessengerBackend{
		searchMessagesResult: protocol.SearchMessagesResult{
			Hits: []protocol.MessageSearchHit{{
				Message: protocol.MessageRecord{
					Message: messaging.Message{
						ID:             "msg/1",
						ConnectionID:   "imap/work",
						ConversationID: "thread/1",
						Parts:          []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "invoice attached"}},
					},
				},
				MatchedFields: []string{"body"},
			}},
			Count:  1,
			Source: protocol.SearchSourceIndexed,
		},
	}
	h := &handlers{backend: backend}

	resp, err := h.handleSearchMessages(context.Background(), bridge.Envelope{
		AgentID: "agent/real",
		Payload: json.RawMessage(`{"agent_id":"agent/fake","connection_id":"imap/work","query":"invoice","limit":10}`),
	})
	if err != nil {
		t.Fatalf("handleSearchMessages() error = %v", err)
	}
	if backend.searchMessages.AgentID != "agent/real" {
		t.Fatalf("AgentID = %q, want transport-stamped agent", backend.searchMessages.AgentID)
	}
	if backend.searchMessages.Query != "invoice" || backend.searchMessages.Limit != 10 {
		t.Fatalf("searchMessages = %+v, want query/limit forwarded", backend.searchMessages)
	}
	var got protocol.SearchMessagesResult
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("response decode: %v", err)
	}
	if got.Count != 1 || got.Hits[0].Message.Message.ID != "msg/1" {
		t.Fatalf("search result = %+v, want msg/1", got)
	}
}

func TestHandleSearchConversationsValidatesQuery(t *testing.T) {
	t.Parallel()

	h := &handlers{backend: &fakeMessengerBackend{}}
	_, err := h.handleSearchConversations(context.Background(), bridge.Envelope{
		AgentID: "agent/real",
		Payload: json.RawMessage(`{"connection_id":"slack/work","query":"  "}`),
	})
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("handleSearchConversations() error = %v, want query validation", err)
	}
}

func TestHandleCreateDraftValidatesParts(t *testing.T) {
	t.Parallel()

	h := &handlers{backend: &fakeMessengerBackend{}}
	_, err := h.handleCreateDraft(context.Background(), bridge.Envelope{
		AgentID: "agent/real",
		Payload: json.RawMessage(`{"connection_id":"telegram/main","conversation_id":"chat/1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "parts are required") {
		t.Fatalf("handleCreateDraft() error = %v, want parts validation", err)
	}
}

func TestForwardingBackendSendsRequestsOverHostBridge(t *testing.T) {
	t.Parallel()

	forwarder := NewForwardingBackend()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EndpointPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, EndpointPath)
		}
		HandlerWithHostBridge(http.NotFound, forwarder)(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostBackend := &fakeMessengerBackend{
		connections: []messaging.Connection{{
			ID:        "telegram/main",
			AdapterID: "telegram",
			Label:     "Telegram",
			Status:    messaging.ConnectionStatusConnected,
		}},
		searchMessagesResult: protocol.SearchMessagesResult{
			Count: 1,
			Hits: []protocol.MessageSearchHit{{
				Message: protocol.MessageRecord{Message: messaging.Message{ID: "msg/forwarded"}},
			}},
		},
	}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath + "?" + BridgeRoleQuery + "=" + BridgeRoleHost
	hostConn, resp, err := bridge.Dial(ctx, wsURL, NewBridgeHandler(hostBackend, "hermes-dev"))
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("host bridge dial: %v", err)
	}
	defer hostConn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = hostConn.Run(ctx) }()

	waitForMessengerForwarderConnected(t, ctx, forwarder)
	connections, err := forwarder.ListConnections(ctx, ListConnectionsParams{AdapterID: "telegram"})
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(connections) != 1 || connections[0].ID != "telegram/main" {
		t.Fatalf("connections = %+v, want telegram/main", connections)
	}
	if hostBackend.listConnections.AgentID != "hermes-dev" {
		t.Fatalf("host AgentID = %q, want trusted sandbox slug", hostBackend.listConnections.AgentID)
	}
	searchResult, err := forwarder.SearchMessages(ctx, SearchMessagesParams{
		ConnectionID: "telegram/main",
		Query:        "invoice",
		PageRequest:  protocol.PageRequest{Limit: 5},
	})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if searchResult.Count != 1 || searchResult.Hits[0].Message.Message.ID != "msg/forwarded" {
		t.Fatalf("search result = %+v, want forwarded msg", searchResult)
	}
	if hostBackend.searchMessages.AgentID != "hermes-dev" || hostBackend.searchMessages.Query != "invoice" {
		t.Fatalf("host search params = %+v, want trusted sandbox slug and query", hostBackend.searchMessages)
	}
}

func waitForMessengerForwarderConnected(t *testing.T, ctx context.Context, forwarder *ForwardingBackend) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if forwarder.Connected() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for host bridge attachment")
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context done waiting for host bridge attachment: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type fakeMessengerBackend struct {
	connections []messaging.Connection
	messages    []messaging.Message

	listConnections      ListConnectionsParams
	listConversations    ListConversationsParams
	listEvents           ListEventsParams
	getMessages          GetMessagesParams
	searchConversations  SearchConversationsParams
	searchMessages       SearchMessagesParams
	createDraft          CreateDraftParams
	requestSend          RequestSendParams
	searchMessagesResult protocol.SearchMessagesResult
}

func (f *fakeMessengerBackend) ListConnections(_ context.Context, params ListConnectionsParams) ([]messaging.Connection, error) {
	f.listConnections = params
	return f.connections, nil
}

func (f *fakeMessengerBackend) ListConversations(_ context.Context, params ListConversationsParams) ([]messaging.Conversation, error) {
	f.listConversations = params
	return nil, nil
}

func (f *fakeMessengerBackend) ListEvents(_ context.Context, params ListEventsParams) ([]messaging.Event, error) {
	f.listEvents = params
	return nil, nil
}

func (f *fakeMessengerBackend) GetMessages(_ context.Context, params GetMessagesParams) ([]messaging.Message, error) {
	f.getMessages = params
	return f.messages, nil
}

func (f *fakeMessengerBackend) SearchConversations(_ context.Context, params SearchConversationsParams) (protocol.SearchConversationsResult, error) {
	f.searchConversations = params
	return protocol.SearchConversationsResult{}, nil
}

func (f *fakeMessengerBackend) SearchMessages(_ context.Context, params SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	f.searchMessages = params
	return f.searchMessagesResult, nil
}

func (f *fakeMessengerBackend) CreateDraft(_ context.Context, params CreateDraftParams) (messagingbroker.DraftMutationResult, error) {
	f.createDraft = params
	return messagingbroker.DraftMutationResult{}, nil
}

func (f *fakeMessengerBackend) RequestSend(_ context.Context, params RequestSendParams) (messagingbroker.RequestSendDraftResult, error) {
	f.requestSend = params
	return messagingbroker.RequestSendDraftResult{}, nil
}
