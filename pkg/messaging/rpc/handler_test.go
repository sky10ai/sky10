package rpc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
	skysecrets "github.com/sky10/sky10/pkg/secrets"
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

func TestHandlerListExternalAdapters(t *testing.T) {
	t.Parallel()

	registry := newExternalRegistryFixture(t)
	handler := newTestHandlerWithExternal(t, nil, registry)

	result, err, handled := handler.Dispatch(context.Background(), "messaging.adapters", nil)
	if err != nil {
		t.Fatalf("Dispatch(adapters) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(adapters) handled = false, want true")
	}
	body := result.(map[string]interface{})
	if body["count"].(int) != 2 {
		t.Fatalf("adapter count = %v, want 2", body["count"])
	}
	adapters := body["adapters"].([]adapterInfo)
	var slack adapterInfo
	for _, adapter := range adapters {
		if adapter.Name == "slack" {
			slack = adapter
			break
		}
	}
	if slack.Source != "external" {
		t.Fatalf("slack source = %q, want external", slack.Source)
	}
	if slack.Adapter == nil || slack.Adapter.DisplayName != "Slack" {
		t.Fatalf("slack adapter = %+v, want Slack metadata", slack.Adapter)
	}
	if !hasAdapterSetting(slack.Settings, "bot_token", messagingexternal.SettingTargetCredential) {
		t.Fatalf("slack settings = %#v, want credential setting", slack.Settings)
	}
	if !hasAdapterAction(slack.Actions, "connect", messagingexternal.ActionKindConnect) {
		t.Fatalf("slack actions = %#v, want connect action", slack.Actions)
	}

	result, err, handled = handler.Dispatch(context.Background(), "messaging.adapter", mustJSON(t, adapterParams{
		AdapterID: "slack",
	}))
	if err != nil {
		t.Fatalf("Dispatch(adapter) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(adapter) handled = false, want true")
	}
	if result.(adapterInfo).Source != "external" {
		t.Fatalf("adapter source = %q, want external", result.(adapterInfo).Source)
	}
}

func TestHandlerRunAdapterActionStoresSettingsAndValidates(t *testing.T) {
	t.Parallel()

	registry := newExternalRegistryFixture(t)
	writer := &recordingSecretWriter{}
	handler := newTestHandlerWithExternal(t, func(adapterID string) (messagingruntime.ProcessSpec, error) {
		if adapterID != "slack" {
			t.Fatalf("resolver adapterID = %q, want slack", adapterID)
		}
		return messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestMessagingRPCHandlerHelperProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_MESSAGING_RPC_ADAPTER=1",
				"GO_MESSAGING_RPC_ADAPTER_ID=slack",
			},
		}, nil
	}, registry)
	handler.secretWriter = writer

	result, err, handled := handler.Dispatch(context.Background(), "messaging.runAdapterAction", mustJSON(t, runAdapterActionParams{
		AdapterID:    "slack",
		ActionID:     "validate",
		ConnectionID: "slack/work",
		Label:        "Work Slack",
		Settings: map[string]json.RawMessage{
			"bot_token":                json.RawMessage(`"xoxb-test"`),
			"slack_team_id":            json.RawMessage(`"T123"`),
			"slack_history_limit":      json.RawMessage(`25`),
			"slack_api_base_url":       json.RawMessage(`"https://slack.test/api"`),
			"slack_conversation_types": json.RawMessage(`"public_channel,im"`),
		},
	}))
	if err != nil {
		t.Fatalf("Dispatch(runAdapterAction validate) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(runAdapterAction validate) handled = false, want true")
	}
	validate := result.(runAdapterActionResult)
	if validate.ActionKind != messagingexternal.ActionKindValidateConfig {
		t.Fatalf("action kind = %q, want validate_config", validate.ActionKind)
	}
	if validate.Validation == nil || len(validate.Validation.Issues) != 0 {
		t.Fatalf("validation = %+v, want no issues", validate.Validation)
	}
	if validate.Connection.Auth.Method != messaging.AuthMethodBotToken {
		t.Fatalf("auth method = %q, want bot_token", validate.Connection.Auth.Method)
	}
	if validate.Connection.Auth.CredentialRef == "" {
		t.Fatal("credential_ref is empty, want stored secret ref")
	}
	if got := validate.Connection.Metadata["slack_team_id"]; got != "T123" {
		t.Fatalf("metadata slack_team_id = %q, want T123", got)
	}
	if got := validate.Connection.Metadata["slack_history_limit"]; got != "25" {
		t.Fatalf("metadata slack_history_limit = %q, want 25", got)
	}
	if len(writer.puts) != 1 {
		t.Fatalf("secret puts = %d, want 1", len(writer.puts))
	}
	if writer.puts[0].Name != validate.Connection.Auth.CredentialRef {
		t.Fatalf("secret name = %q, want credential_ref %q", writer.puts[0].Name, validate.Connection.Auth.CredentialRef)
	}
	var credential map[string]string
	if err := json.Unmarshal(writer.puts[0].Payload, &credential); err != nil {
		t.Fatalf("unmarshal stored credential: %v", err)
	}
	if credential["bot_token"] != "xoxb-test" {
		t.Fatalf("stored credential = %#v, want bot_token", credential)
	}

	result, err, handled = handler.Dispatch(context.Background(), "messaging.runAdapterAction", mustJSON(t, runAdapterActionParams{
		AdapterID:    "slack",
		ActionID:     "connect",
		ConnectionID: "slack/work",
		Settings: map[string]json.RawMessage{
			"slack_team_id": json.RawMessage(`"T456"`),
		},
	}))
	if err != nil {
		t.Fatalf("Dispatch(runAdapterAction connect) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(runAdapterAction connect) handled = false, want true")
	}
	connect := result.(runAdapterActionResult)
	if connect.Connect == nil || connect.Connect.Connection.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connect result = %+v, want connected", connect.Connect)
	}
	if len(writer.puts) != 1 {
		t.Fatalf("secret puts after connect = %d, want unchanged 1", len(writer.puts))
	}
	if got := connect.Connect.Connection.Metadata["slack_team_id"]; got != "T456" {
		t.Fatalf("connected metadata slack_team_id = %q, want T456", got)
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
	if err := handler.store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/search",
		Name: "Search",
		Rules: messaging.PolicyRules{
			SearchMessages: true,
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}

	raw := mustJSON(t, connectBuiltinParams{
		Connection: messaging.Connection{
			ID:              "imap/work",
			AdapterID:       "imap-smtp",
			Label:           "Work Mail",
			Status:          messaging.ConnectionStatusConnecting,
			DefaultPolicyID: "policy/search",
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
	if _, ok := handler.store.GetPlacement("msg/latisha", "container/inbox"); !ok {
		t.Fatal("GetPlacement(msg/latisha, inbox) = false, want hydrated placement")
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.searchMessages", mustJSON(t, searchMessagesParams{
		ConnectionID: "imap/work",
		Query:        "hello",
	}))
	if err != nil {
		t.Fatalf("Dispatch(searchMessages) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(searchMessages) handled = false, want true")
	}
	search := result.(protocol.SearchMessagesResult)
	if search.Count != 1 || search.Hits[0].Message.Message.ID != "msg/latisha" {
		t.Fatalf("search messages = %+v, want msg/latisha", search)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.listContainers", mustJSON(t, protocol.ListContainersParams{
		ConnectionID: "imap/work",
	}))
	if err != nil {
		t.Fatalf("Dispatch(listContainers) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(listContainers) handled = false, want true")
	}
	containers := result.(protocol.ListContainersResult)
	if len(containers.Containers) != 1 || containers.Containers[0].ID != "container/inbox" {
		t.Fatalf("containers = %+v, want container/inbox", containers.Containers)
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

func TestHandlerConnectionLifecycleMethods(t *testing.T) {
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

	connection := messaging.Connection{
		ID:        "imap/work",
		AdapterID: "imap-smtp",
		Label:     "Work Mail",
		Status:    messaging.ConnectionStatusConnecting,
	}
	result, err, handled := handler.Dispatch(ctx, "messaging.createConnection", mustJSON(t, connectBuiltinParams{Connection: connection}))
	if err != nil {
		t.Fatalf("Dispatch(createConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(createConnection) handled = false, want true")
	}
	if result.(messaging.Connection).ID != connection.ID {
		t.Fatalf("created connection = %+v, want %s", result, connection.ID)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.connectConnection", mustJSON(t, connectionParams{ConnectionID: connection.ID}))
	if err != nil {
		t.Fatalf("Dispatch(connectConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(connectConnection) handled = false, want true")
	}
	if result.(messagingbroker.ConnectResult).Connection.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connect result = %+v, want connected", result)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.refreshConnection", mustJSON(t, connectionParams{ConnectionID: connection.ID}))
	if err != nil {
		t.Fatalf("Dispatch(refreshConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(refreshConnection) handled = false, want true")
	}
	if result.(messagingbroker.ConnectResult).Connection.Metadata["refreshed"] != "true" {
		t.Fatalf("refresh result = %+v, want refreshed metadata", result)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.disableConnection", mustJSON(t, connectionParams{ConnectionID: connection.ID}))
	if err != nil {
		t.Fatalf("Dispatch(disableConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(disableConnection) handled = false, want true")
	}
	if result.(messaging.Connection).Status != messaging.ConnectionStatusDisabled {
		t.Fatalf("disable result = %+v, want disabled", result)
	}

	result, err, handled = handler.Dispatch(ctx, "messaging.deleteConnection", mustJSON(t, connectionParams{ConnectionID: connection.ID}))
	if err != nil {
		t.Fatalf("Dispatch(deleteConnection) error = %v", err)
	}
	if !handled {
		t.Fatal("Dispatch(deleteConnection) handled = false, want true")
	}
	if !result.(messagingbroker.DeleteConnectionResult).Deleted {
		t.Fatalf("delete result = %+v, want deleted", result)
	}
	if got := handler.store.ListConnections(); len(got) != 0 {
		t.Fatalf("ListConnections() = %+v, want empty", got)
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

	_, err, handled = handler.Dispatch(context.Background(), "messaging.connectAdapter", mustJSON(t, connectBuiltinParams{
		Connection: messaging.Connection{
			ID:        "slack/work",
			AdapterID: "slack",
			Label:     "Work Slack",
		},
	}))
	if !handled {
		t.Fatal("Dispatch(connectAdapter) handled = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "process resolver") {
		t.Fatalf("Dispatch(connectAdapter) error = %v, want resolver failure", err)
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
	return newTestHandlerWithExternal(t, resolver, nil)
}

func newTestHandlerWithExternal(t *testing.T, resolver ProcessResolver, externalAdapters *messagingexternal.Registry) *Handler {
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
		broker:           b,
		store:            store,
		processResolver:  resolver,
		externalAdapters: externalAdapters,
	}
}

func newExternalRegistryFixture(t *testing.T) *messagingexternal.Registry {
	t.Helper()

	dir := t.TempDir()
	entryDir := filepath.Join(dir, "dist")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatalf("create fixture entry dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entryDir, "adapter.js"), []byte("console.log('fixture');\n"), 0o644); err != nil {
		t.Fatalf("write fixture entry: %v", err)
	}
	manifest := []byte(`{
  "id": "slack",
  "display_name": "Slack",
  "version": "0.1.0",
  "description": "Slack adapter fixture",
  "auth_methods": ["bot_token"],
  "capabilities": {
    "send_messages": true,
    "search_conversations": true
  },
  "settings": [
    {
      "key": "bot_token",
      "label": "Bot token",
      "kind": "secret",
      "target": "credential",
      "required": true
    },
    {
      "key": "slack_team_id",
      "label": "Team ID",
      "kind": "text",
      "target": "metadata"
    },
    {
      "key": "slack_history_limit",
      "label": "History page size",
      "kind": "number",
      "target": "metadata"
    },
    {
      "key": "slack_api_base_url",
      "label": "Slack API base URL",
      "kind": "url",
      "target": "metadata"
    },
    {
      "key": "slack_conversation_types",
      "label": "Conversation types",
      "kind": "text",
      "target": "metadata",
      "default": "public_channel,private_channel,im,mpim"
    }
  ],
  "actions": [
    {
      "id": "validate",
      "label": "Validate token",
      "kind": "validate_config"
    },
    {
      "id": "connect",
      "label": "Connect Slack",
      "kind": "connect",
      "primary": true
    }
  ],
  "runtime": {
    "type": "bun",
    "version": "^1.3"
  },
  "entry": "dist/adapter.js",
  "sandbox": {
    "mode": "none"
  }
}`)
	if err := os.WriteFile(filepath.Join(dir, "adapter.json"), manifest, 0o644); err != nil {
		t.Fatalf("write fixture manifest: %v", err)
	}
	registry, err := messagingexternal.NewRegistry(messagingexternal.ResolveOptions{BunPath: "bun"}, dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func hasAdapterAction(actions []messagingexternal.Action, id string, kind messagingexternal.ActionKind) bool {
	for _, action := range actions {
		if action.ID == id && action.Kind == kind {
			return true
		}
	}
	return false
}

func hasAdapterSetting(settings []messagingexternal.Setting, key string, target messagingexternal.SettingTarget) bool {
	for _, setting := range settings {
		if setting.Key == key && setting.Target == target {
			return true
		}
	}
	return false
}

type recordingSecretWriter struct {
	puts []skysecrets.PutParams
}

func (w *recordingSecretWriter) Put(_ context.Context, params skysecrets.PutParams) (*skysecrets.SecretSummary, error) {
	params.Payload = append([]byte(nil), params.Payload...)
	w.puts = append(w.puts, params)
	return &skysecrets.SecretSummary{
		ID:          "secret-id",
		Name:        params.Name,
		Kind:        params.Kind,
		ContentType: params.ContentType,
		Scope:       params.Scope,
		Size:        int64(len(params.Payload)),
	}, nil
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
			adapterID := os.Getenv("GO_MESSAGING_RPC_ADAPTER_ID")
			if adapterID == "" {
				adapterID = "imap-smtp"
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.DescribeResult{
					Protocol: protocol.CurrentProtocol(),
					Adapter: messaging.Adapter{
						ID:          messaging.AdapterID(adapterID),
						DisplayName: adapterID,
						Capabilities: messaging.Capabilities{
							Polling:           true,
							ListConversations: true,
							ListMessages:      true,
							ListContainers:    true,
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodValidateConfig):
			var params protocol.ValidateConfigParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			issues := make([]protocol.ValidationIssue, 0, 1)
			if params.Credential == nil || strings.TrimSpace(params.Credential.Ref) == "" {
				issues = append(issues, protocol.ValidationIssue{
					Severity: protocol.ValidationIssueError,
					Code:     "missing_credential",
					Message:  "credential is required",
				})
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  mustJSONRaw(protocol.ValidateConfigResult{Issues: issues}),
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
		case string(protocol.MethodRefresh):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.RefreshResult{
					Status: messaging.ConnectionStatusConnected,
					Metadata: map[string]string{
						"refreshed": "true",
					},
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
						Placements: []messaging.Placement{{
							MessageID:    params.MessageID,
							ConnectionID: "imap/work",
							ContainerID:  "container/inbox",
							RemoteID:     "101",
						}},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodListContainers):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSONRaw(protocol.ListContainersResult{
					Containers: []messaging.Container{{
						ID:           "container/inbox",
						ConnectionID: "imap/work",
						Kind:         messaging.ContainerKindInbox,
						Name:         "INBOX",
						RemoteID:     "INBOX",
					}},
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
