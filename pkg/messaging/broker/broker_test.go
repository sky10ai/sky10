package broker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

func TestBrokerRegisterConnectAndPoll(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(ctx, Config{
		Store:   store,
		RootDir: rootDir,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Fatalf("broker.Close() error = %v", err)
		}
	}()

	connection := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnecting,
	}
	if err := b.RegisterConnection(ctx, RegisterConnectionParams{
		Connection: connection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1"},
		},
	}); err != nil {
		t.Fatalf("RegisterConnection() error = %v", err)
	}

	connectResult, err := b.ConnectConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}
	if connectResult.Connection.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connected status = %q, want %q", connectResult.Connection.Status, messaging.ConnectionStatusConnected)
	}
	identities := store.ListConnectionIdentities(connection.ID)
	if len(identities) != 1 || identities[0].ID != "identity/test" {
		t.Fatalf("ListConnectionIdentities() = %+v, want identity/test", identities)
	}
	if _, err := os.Stat(connectResult.Paths.RuntimeDir); err != nil {
		t.Fatalf("runtime dir stat error = %v", err)
	}
	if strings.Contains(connectResult.Paths.RootDir, string(connection.ID)) {
		t.Fatalf("runtime root dir = %q, should not contain raw connection id %q", connectResult.Paths.RootDir, connection.ID)
	}

	pollResult, err := b.PollConnection(ctx, connection.ID, 10)
	if err != nil {
		t.Fatalf("PollConnection() error = %v", err)
	}
	if len(pollResult.Events) != 1 {
		t.Fatalf("poll events len = %d, want 1", len(pollResult.Events))
	}
	if pollResult.Events[0].ConnectionID != connection.ID {
		t.Fatalf("poll event connection id = %q, want %q", pollResult.Events[0].ConnectionID, connection.ID)
	}
	if len(pollResult.Conversations) != 1 || pollResult.Conversations[0].ID != "conv/latisha" {
		t.Fatalf("poll conversations = %+v, want conv/latisha", pollResult.Conversations)
	}
	if len(pollResult.Messages) != 1 || pollResult.Messages[0].ID != "msg/latisha" {
		t.Fatalf("poll messages = %+v, want msg/latisha", pollResult.Messages)
	}

	events := store.ListConnectionEvents(connection.ID)
	if len(events) < 3 {
		t.Fatalf("ListConnectionEvents() len = %d, want at least 3 including connect + identity + poll", len(events))
	}
	checkpoint, ok := store.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-1" {
		t.Fatalf("GetCheckpoint() = %+v, %v; want cursor-1", checkpoint, ok)
	}
	storedConversation, ok := store.GetConversation("conv/latisha")
	if !ok || storedConversation.Title != "Latisha" {
		t.Fatalf("GetConversation() = %+v, %v; want Latisha", storedConversation, ok)
	}
	storedMessage, ok := store.GetMessage("msg/latisha")
	if !ok || storedMessage.Sender.DisplayName != "Latisha" {
		t.Fatalf("GetMessage() = %+v, %v; want sender Latisha", storedMessage, ok)
	}
}

func TestBrokerHandleWebhookConnection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(ctx, Config{
		Store:   store,
		RootDir: rootDir,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := registerHelperConnection(t, ctx, b)
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}

	result, err := b.HandleWebhookConnection(ctx, connection.ID, WebhookRequest{
		RequestID: "req-inline",
		Method:    "POST",
		URL:       "https://example.test/webhook",
		Headers: map[string][]string{
			"X-Test": {"1"},
		},
		Body:       []byte(`{"hello":"world"}`),
		RemoteAddr: "127.0.0.1:12345",
	})
	if err != nil {
		t.Fatalf("HandleWebhookConnection() error = %v", err)
	}
	if result.StatusCode != 202 {
		t.Fatalf("StatusCode = %d, want 202", result.StatusCode)
	}
	if result.Headers["x-body-source"] != "inline" {
		t.Fatalf("x-body-source = %q, want inline", result.Headers["x-body-source"])
	}
	if len(result.Events) != 1 || result.Events[0].MessageID != "msg/webhook" {
		t.Fatalf("Events = %+v, want msg/webhook event", result.Events)
	}
	if len(result.Conversations) != 1 || result.Conversations[0].ID != "conv/latisha" {
		t.Fatalf("Conversations = %+v, want conv/latisha", result.Conversations)
	}
	if len(result.Messages) != 1 || result.Messages[0].ID != "msg/webhook" {
		t.Fatalf("Messages = %+v, want msg/webhook", result.Messages)
	}
	checkpoint, ok := store.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-webhook" {
		t.Fatalf("GetCheckpoint() = %+v, %v; want cursor-webhook", checkpoint, ok)
	}
	storedMessage, ok := store.GetMessage("msg/webhook")
	if !ok || storedMessage.Sender.DisplayName != "Webhook Sender" {
		t.Fatalf("GetMessage() = %+v, %v; want Webhook Sender", storedMessage, ok)
	}
}

func TestBrokerHandleWebhookConnectionStagesBinaryBody(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(ctx, Config{
		Store:   store,
		RootDir: rootDir,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := registerHelperConnection(t, ctx, b)
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}

	result, err := b.HandleWebhookConnection(ctx, connection.ID, WebhookRequest{
		RequestID: "req-binary",
		Method:    "POST",
		URL:       "https://example.test/webhook",
		Body:      []byte{0xff, 0x00, 0x01, 0x02},
	})
	if err != nil {
		t.Fatalf("HandleWebhookConnection() error = %v", err)
	}
	if result.Headers["x-body-source"] != "blob" {
		t.Fatalf("x-body-source = %q, want blob", result.Headers["x-body-source"])
	}
	if result.Body != "blob:4" {
		t.Fatalf("Body = %q, want blob:4", result.Body)
	}
	found := false
	err = filepath.WalkDir(filepath.Join(rootDir, "staging"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(path, ".bin") {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	if !found {
		t.Fatal("expected staged webhook body file")
	}
}

func TestBrokerRuntimePathsForConnection(t *testing.T) {
	t.Parallel()

	store, err := messagingstore.NewStore(context.Background(), messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(context.Background(), Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	paths := b.runtimePathsForConnection(messaging.Connection{
		ID:        "imap/work/inbox",
		AdapterID: "imap-smtp",
		Label:     "Work Mail",
	})
	if strings.Contains(paths.RootDir, "imap/work/inbox") {
		t.Fatalf("paths.RootDir = %q, want encoded connection segment", paths.RootDir)
	}
	if !strings.Contains(paths.RootDir, filepath.Join("adapters", "imap-smtp")) {
		t.Fatalf("paths.RootDir = %q, want adapter segment", paths.RootDir)
	}
}

func registerHelperConnection(t *testing.T, ctx context.Context, b *Broker) messaging.Connection {
	t.Helper()

	connection := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnecting,
	}
	if err := b.RegisterConnection(ctx, RegisterConnectionParams{
		Connection: connection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1"},
		},
	}); err != nil {
		t.Fatalf("RegisterConnection() error = %v", err)
	}
	return connection
}

func TestBrokerHelperMessagingAdapterProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER") != "1" {
		return
	}
	if err := runBrokerHelperMessagingAdapter(); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func runBrokerHelperMessagingAdapter() error {
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
						ID:          "test-adapter",
						DisplayName: "Test Adapter",
						Capabilities: messaging.Capabilities{
							Polling:           true,
							ListConversations: true,
							ListMessages:      true,
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodConnect):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ConnectResult{
					Status: messaging.ConnectionStatusConnected,
					Identities: []messaging.Identity{{
						ID:           "identity/test",
						ConnectionID: "slack/work",
						Kind:         messaging.IdentityKindBot,
						RemoteID:     "U123",
						DisplayName:  "Test Bot",
						CanReceive:   true,
						CanSend:      true,
						IsDefault:    true,
					}},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodPoll):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.PollResult{
					Events: []messaging.Event{{
						Type:           messaging.EventTypeMessageReceived,
						ConversationID: "conv/latisha",
						MessageID:      "msg/latisha",
					}},
					Checkpoint: &protocol.Checkpoint{
						Cursor: "cursor-1",
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodHandleWebhook):
			var params protocol.HandleWebhookParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			bodySource := "inline"
			bodyReply := params.Request.Body
			if params.Request.BodyBlob.LocalPath != "" {
				bodySource = "blob"
				raw, err := os.ReadFile(params.Request.BodyBlob.LocalPath)
				if err != nil {
					return err
				}
				bodyReply = "blob:" + strconv.Itoa(len(raw))
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.HandleWebhookResult{
					Events: []messaging.Event{{
						Type:           messaging.EventTypeMessageReceived,
						ConversationID: "conv/latisha",
						MessageID:      "msg/webhook",
					}},
					Checkpoint: &protocol.Checkpoint{
						Cursor: "cursor-webhook",
					},
					StatusCode: 202,
					Headers: map[string]string{
						"x-body-source": bodySource,
						"x-request-id":  params.Request.RequestID,
					},
					Body: bodyReply,
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodListConversations):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ListConversationsResult{
					Conversations: []messaging.Conversation{{
						ID:              "conv/latisha",
						ConnectionID:    "slack/work",
						LocalIdentityID: "identity/test",
						Kind:            messaging.ConversationKindDirect,
						RemoteID:        "D123",
						Title:           "Latisha",
						Participants: []messaging.Participant{
							{Kind: messaging.ParticipantKindBot, IdentityID: "identity/test", IsLocal: true},
							{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
						},
					}},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodGetMessage):
			var params protocol.GetMessageParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			message := messaging.Message{
				ID:              params.MessageID,
				ConnectionID:    "slack/work",
				ConversationID:  "conv/latisha",
				LocalIdentityID: "identity/test",
				Direction:       messaging.MessageDirectionInbound,
				Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
				Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "Can you review this?"}},
				CreatedAt:       time.Date(2026, 4, 22, 13, 0, 0, 0, time.UTC),
				Status:          messaging.MessageStatusReceived,
			}
			if params.MessageID == "msg/webhook" {
				message.Sender.DisplayName = "Webhook Sender"
				message.Parts[0].Text = "Webhook payload"
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.GetMessageResult{
					Message: protocol.MessageRecord{
						Message: message,
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodHealth):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.HealthResult{
					Health: protocol.HealthStatus{OK: true, Status: messaging.ConnectionStatusConnected},
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
