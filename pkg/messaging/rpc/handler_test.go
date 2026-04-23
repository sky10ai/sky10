package rpc

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

func TestHandlerListAdaptersAndConnections(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, nil)

	result, err, handled := handler.Dispatch(context.Background(), "messaging.adapters", nil)
	if err != nil {
		t.Fatalf("Dispatch(adapters) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(adapters) handled = false, want true")
	}
	body := result.(map[string]interface{})
	if body["count"].(int) != 1 {
		t.Fatalf("adapter count = %v, want 1", body["count"])
	}

	result, err, handled = handler.Dispatch(context.Background(), "messaging.connections", nil)
	if err != nil {
		t.Fatalf("Dispatch(connections) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(connections) handled = false, want true")
	}
	if result.(map[string]interface{})["count"].(int) != 0 {
		t.Fatalf("connection count = %v, want 0", result.(map[string]interface{})["count"])
	}
}

func TestHandlerConnectBuiltinAndPoll(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	handler := newTestHandler(t, func(adapterID string) (messagingruntime.ProcessSpec, error) {
		if adapterID != "imap-smtp" {
			t.Fatalf("resolver adapterID = %q, want imap-smtp", adapterID)
		}
		return messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestMessagingRPCHandlerHelperProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_RPC_ADAPTER=1"},
		}, nil
	})

	raw := mustJSON(t, connectBuiltinParams{
		Connection: messaging.Connection{
			ID:        "imap/work",
			AdapterID: "imap-smtp",
			Label:     "Work Mail",
			Status:    messaging.ConnectionStatusConnecting,
			Auth: messaging.AuthInfo{
				Method:        messaging.AuthMethodBasic,
				CredentialRef: "secret://imap/work",
			},
		},
	})

	result, err, handled := handler.Dispatch(ctx, "messaging.connectBuiltin", raw)
	if err != nil {
		t.Fatalf("Dispatch(connectBuiltin) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(connectBuiltin) handled = false, want true")
	}
	connect := result.(messagingbroker.ConnectResult)
	if connect.Connection.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connect status = %q, want %q", connect.Connection.Status, messaging.ConnectionStatusConnected)
	}

	if got := handler.store.ListConnectionIdentities("imap/work"); len(got) != 1 || got[0].ID != "identity/test" {
		t.Fatalf("identities = %+v, want identity/test", got)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.pollConnection", mustJSON(t, connectionParams{
		ConnectionID: "imap/work",
		Limit:        10,
	}))
	if err != nil {
		t.Fatalf("Dispatch(pollConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(pollConnection) handled = false, want true")
	}
	poll := result.(messagingbroker.PollResult)
	if len(poll.Messages) != 1 || poll.Messages[0].ID != "msg/latisha" {
		t.Fatalf("poll messages = %+v, want msg/latisha", poll.Messages)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.connections", nil)
	if err != nil {
		t.Fatalf("Dispatch(connections) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(connections) handled = false, want true")
	}
	if result.(map[string]interface{})["count"].(int) != 1 {
		t.Fatalf("connection count = %v, want 1", result.(map[string]interface{})["count"])
	}
}

func TestHandlerConnectBuiltinRequiresResolver(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, nil)
	_, err, handled := handler.Dispatch(context.Background(), "messaging.connectBuiltin", mustJSON(t, connectBuiltinParams{
		Connection: messaging.Connection{
			ID:        "imap/work",
			AdapterID: "imap-smtp",
			Label:     "Work Mail",
		},
	}))
	if !handled {
		t.Fatal("Dispatch(connectBuiltin) handled = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "process resolver") {
		t.Fatalf("Dispatch(connectBuiltin) error = %v, want resolver failure", err)
	}
}

func TestHandlerIgnoresUnknownNamespace(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, nil)
	_, _, handled := handler.Dispatch(context.Background(), "apps.status", nil)
	if handled {
		t.Fatal("Dispatch(apps.status) handled = true, want false")
	}
}

func TestMessagingRPCHandlerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MESSAGING_RPC_ADAPTER") != "1" {
		return
	}
	if err := runMessagingRPCHandlerHelperProcess(); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func newTestHandler(t *testing.T, resolver ProcessResolver) *Handler {
	t.Helper()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:   store,
		RootDir: t.TempDir(),
		CredentialResolver: messagingbroker.CredentialResolverFunc(func(_ context.Context, ref string) (messagingbroker.CredentialMaterial, error) {
			return messagingbroker.CredentialMaterial{
				Ref:         ref,
				ContentType: "application/json",
				Payload:     []byte(`{"username":"latisha@example.com","password":"swordfish"}`),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("broker.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = b.Close()
	})
	return &Handler{
		broker:          b,
		store:           store,
		processResolver: resolver,
	}
}

func runMessagingRPCHandlerHelperProcess() error {
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
				Result: mustJSONRaw(protocol.DescribeResult{
					Protocol: protocol.CurrentProtocol(),
					Adapter: messaging.Adapter{
						ID:          "imap-smtp",
						DisplayName: "IMAP/SMTP",
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
			var params protocol.ConnectParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.ConnectResult{
					Status: messaging.ConnectionStatusConnected,
					Identities: []messaging.Identity{{
						ID:           "identity/test",
						ConnectionID: params.Connection.ID,
						Kind:         messaging.IdentityKindEmail,
						Address:      "latisha@example.com",
						DisplayName:  "Latisha",
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
				Result: mustJSONRaw(protocol.PollResult{
					Events: []messaging.Event{{
						Type:           messaging.EventTypeMessageReceived,
						ConversationID: "conv/latisha",
						MessageID:      "msg/latisha",
					}},
					Checkpoint: &protocol.Checkpoint{Cursor: "cursor-1"},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodListConversations):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.ListConversationsResult{
					Conversations: []messaging.Conversation{{
						ID:              "conv/latisha",
						ConnectionID:    "imap/work",
						LocalIdentityID: "identity/test",
						Kind:            messaging.ConversationKindEmailThread,
						RemoteID:        "thread-1",
						Title:           "Latisha",
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
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.GetMessageResult{
					Message: protocol.MessageRecord{
						Message: messaging.Message{
							ID:              params.MessageID,
							ConnectionID:    "imap/work",
							ConversationID:  "conv/latisha",
							LocalIdentityID: "identity/test",
							Direction:       messaging.MessageDirectionInbound,
							Sender: messaging.Participant{
								Kind:        messaging.ParticipantKindUser,
								Address:     "latisha@example.com",
								DisplayName: "Latisha",
							},
							Parts:     []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "hello"}},
							CreatedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
							Status:    messaging.MessageStatusReceived,
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodHealth):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.HealthResult{
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

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func mustJSONRaw(v any) json.RawMessage {
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
