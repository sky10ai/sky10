package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/messaging"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
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

func TestBrokerConnectConnectionRequiresCredentialResolver(t *testing.T) {
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

	connection := messaging.Connection{
		ID:        "imap/work",
		AdapterID: "imap-smtp",
		Label:     "Work Mail",
		Status:    messaging.ConnectionStatusConnecting,
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBasic,
			CredentialRef: "secret://imap/work",
		},
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

	_, err = b.ConnectConnection(ctx, connection.ID)
	if err == nil || !strings.Contains(err.Error(), "credential resolver") {
		t.Fatalf("ConnectConnection() error = %v, want credential resolver failure", err)
	}
}

func TestBrokerConnectConnectionStagesCredential(t *testing.T) {
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
		CredentialResolver: CredentialResolverFunc(func(_ context.Context, ref string) (CredentialMaterial, error) {
			if ref != "secret://imap/work" {
				t.Fatalf("resolver ref = %q, want secret://imap/work", ref)
			}
			return CredentialMaterial{
				Ref:         ref,
				ContentType: "application/json",
				Payload:     []byte(`{"username":"latisha@example.com","password":"swordfish"}`),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := messaging.Connection{
		ID:        "imap/work",
		AdapterID: "imap-smtp",
		Label:     "Work Mail",
		Status:    messaging.ConnectionStatusConnecting,
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBasic,
			CredentialRef: "secret://imap/work",
		},
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
	if connectResult.Connection.Metadata["credential_ref"] != "secret://imap/work" {
		t.Fatalf("credential_ref metadata = %q, want secret://imap/work", connectResult.Connection.Metadata["credential_ref"])
	}
	if connectResult.Connection.Metadata["credential_path_present"] != "true" {
		t.Fatalf("credential_path_present = %q, want true", connectResult.Connection.Metadata["credential_path_present"])
	}
	if connectResult.Connection.Metadata["credential_content_type"] != "application/json" {
		t.Fatalf("credential_content_type = %q, want application/json", connectResult.Connection.Metadata["credential_content_type"])
	}

	stagedPath := filepath.Join(connectResult.Paths.SecretsDir, encodePathSegment("secret://imap/work")+".bin")
	raw, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", stagedPath, err)
	}
	if string(raw) != `{"username":"latisha@example.com","password":"swordfish"}` {
		t.Fatalf("staged credential body = %q", string(raw))
	}

	if _, err := b.DisableConnection(ctx, connection.ID); err != nil {
		t.Fatalf("DisableConnection() error = %v", err)
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged credential stat after disable = %v, want not exist", err)
	}
}

func TestBrokerConnectionLifecycleRefreshDisableDelete(t *testing.T) {
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

	connection := messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Work Slack",
		Status:    messaging.ConnectionStatusConnecting,
	}
	created, err := b.CreateConnection(ctx, RegisterConnectionParams{
		Connection: connection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection() error = %v", err)
	}
	if created.ID != connection.ID {
		t.Fatalf("created connection ID = %q, want %q", created.ID, connection.ID)
	}
	if _, err := b.CreateConnection(ctx, RegisterConnectionParams{
		Connection: connection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env:  []string{"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1"},
		},
	}); err == nil {
		t.Fatal("CreateConnection(duplicate) error = nil, want duplicate failure")
	}

	connect, err := b.ConnectConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}
	if got := store.ListConnectionIdentities(connection.ID); len(got) != 1 || got[0].ID != "identity/test" {
		t.Fatalf("ListConnectionIdentities() after connect = %+v, want identity/test", got)
	}

	refresh, err := b.RefreshConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("RefreshConnection() error = %v", err)
	}
	if refresh.Connection.Metadata["refreshed"] != "true" {
		t.Fatalf("RefreshConnection() metadata = %+v, want refreshed=true", refresh.Connection.Metadata)
	}
	if refresh.Connection.Auth.Method != messaging.AuthMethodNone || refresh.Connection.Auth.ExternalAccount != "refreshed@example.test" {
		t.Fatalf("RefreshConnection() auth = %+v, want refreshed auth info", refresh.Connection.Auth)
	}
	if got := store.ListConnectionIdentities(connection.ID); len(got) != 1 || got[0].ID != "identity/test" {
		t.Fatalf("ListConnectionIdentities() after refresh = %+v, want preserved identity/test", got)
	}

	disabled, err := b.DisableConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("DisableConnection() error = %v", err)
	}
	if disabled.Status != messaging.ConnectionStatusDisabled {
		t.Fatalf("disabled status = %q, want disabled", disabled.Status)
	}
	if _, err := b.PollConnection(ctx, connection.ID, 10); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("PollConnection(disabled) error = %v, want disabled failure", err)
	}

	deleted, err := b.DeleteConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("DeleteConnection() error = %v", err)
	}
	if !deleted.Deleted || deleted.ConnectionID != connection.ID {
		t.Fatalf("DeleteConnection() = %+v, want deleted connection", deleted)
	}
	if _, ok := store.GetConnection(connection.ID); ok {
		t.Fatal("GetConnection() = present after delete, want removed")
	}
	if _, err := os.Stat(connect.Paths.RootDir); !os.IsNotExist(err) {
		t.Fatalf("runtime root stat after delete = %v, want not exist", err)
	}
	if got := store.ListConnectionIdentities(connection.ID); len(got) != 0 {
		t.Fatalf("ListConnectionIdentities() after delete = %+v, want empty", got)
	}
	if _, err := b.ConnectConnection(ctx, connection.ID); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ConnectConnection(deleted) error = %v, want not found", err)
	}
}

func TestBrokerRestartReloadsPersistedStateAndCheckpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	backend := messagingstore.NewKVBackend(newMemoryKVStore(), "")
	store, err := messagingstore.NewStore(ctx, backend)
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

	connection := messaging.Connection{
		ID:              "slack/work",
		AdapterID:       "slack",
		Label:           "Work Slack",
		Status:          messaging.ConnectionStatusConnecting,
		DefaultPolicyID: "policy/drafts",
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/drafts",
		Name: "Drafts",
		Rules: messaging.PolicyRules{
			ReadInbound:     true,
			CreateDrafts:    true,
			SendMessages:    true,
			RequireApproval: true,
			ReplyOnly:       true,
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}
	if err := store.PutExposure(ctx, messaging.Exposure{
		ID:           "exposure/hermes",
		ConnectionID: connection.ID,
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		PolicyID:     "policy/drafts",
		Enabled:      true,
	}); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}
	if err := b.RegisterConnection(ctx, RegisterConnectionParams{
		Connection: connection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1",
				"SKY10_MESSAGING_BROKER_HELPER_MODE=checkpoint-aware",
			},
		},
	}); err != nil {
		t.Fatalf("RegisterConnection() error = %v", err)
	}
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}
	if _, err := b.PollConnection(ctx, connection.ID, 10); err != nil {
		t.Fatalf("PollConnection() error = %v", err)
	}
	if _, err := b.CreateDraft(ctx, "exposure/hermes", messaging.Draft{
		ID:              "draft/reply",
		ConnectionID:    connection.ID,
		ConversationID:  "conv/latisha",
		LocalIdentityID: "identity/test",
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "I can review it."}},
		Status:          messaging.DraftStatusPending,
	}); err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("broker.Close() error = %v", err)
	}

	reloadedStore, err := messagingstore.NewStore(ctx, backend)
	if err != nil {
		t.Fatalf("NewStore(reload) error = %v", err)
	}
	reloadedConnection, ok := reloadedStore.GetConnection(connection.ID)
	if !ok {
		t.Fatal("GetConnection() after reload = missing")
	}
	if got := reloadedStore.ListConnectionIdentities(connection.ID); len(got) != 1 || got[0].ID != "identity/test" {
		t.Fatalf("ListConnectionIdentities() after reload = %+v, want identity/test", got)
	}
	if _, ok := reloadedStore.GetConversation("conv/latisha"); !ok {
		t.Fatal("GetConversation(conv/latisha) after reload = missing")
	}
	if _, ok := reloadedStore.GetMessage("msg/latisha"); !ok {
		t.Fatal("GetMessage(msg/latisha) after reload = missing")
	}
	if _, ok := reloadedStore.GetPlacement("msg/latisha", "container/inbox"); !ok {
		t.Fatal("GetPlacement(msg/latisha, inbox) after reload = missing")
	}
	if _, ok := reloadedStore.GetDraft("draft/reply"); !ok {
		t.Fatal("GetDraft(draft/reply) after reload = missing")
	}
	if workflows := reloadedStore.ListWorkflows(); len(workflows) != 1 || workflows[0].DraftID != "draft/reply" {
		t.Fatalf("ListWorkflows() after reload = %+v, want draft workflow", workflows)
	}
	checkpoint, ok := reloadedStore.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-1" {
		t.Fatalf("GetCheckpoint() after reload = %+v, %v; want cursor-1", checkpoint, ok)
	}

	reloadedBroker, err := New(ctx, Config{
		Store:   reloadedStore,
		RootDir: rootDir,
	})
	if err != nil {
		t.Fatalf("New(reloaded) error = %v", err)
	}
	defer func() { _ = reloadedBroker.Close() }()
	if err := reloadedBroker.UpsertConnection(ctx, RegisterConnectionParams{
		Connection: reloadedConnection,
		Process: messagingruntime.ProcessSpec{
			Path: helperProcessExecutableForTests(),
			Args: []string{"-test.run=TestBrokerHelperMessagingAdapterProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1",
				"SKY10_MESSAGING_BROKER_HELPER_MODE=checkpoint-aware",
			},
		},
	}); err != nil {
		t.Fatalf("UpsertConnection(reloaded) error = %v", err)
	}
	pollResult, err := reloadedBroker.PollConnection(ctx, connection.ID, 10)
	if err != nil {
		t.Fatalf("PollConnection(reloaded) error = %v", err)
	}
	if len(pollResult.Events) != 0 {
		t.Fatalf("PollConnection(reloaded) events = %+v, want none after checkpoint", pollResult.Events)
	}
	checkpoint, ok = reloadedStore.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-2" {
		t.Fatalf("GetCheckpoint() after reloaded poll = %+v, %v; want cursor-2", checkpoint, ok)
	}
	receivedEvents := 0
	for _, event := range reloadedStore.ListConnectionEvents(connection.ID) {
		if event.Type == messaging.EventTypeMessageReceived {
			receivedEvents++
		}
	}
	if receivedEvents != 1 {
		t.Fatalf("message_received events after reloaded poll = %d, want 1", receivedEvents)
	}
}

func TestBrokerAdapterRestartPreservesCheckpointAndState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	crashMarker := filepath.Join(t.TempDir(), "poll-crashed")
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
			Env: []string{
				"GO_WANT_HELPER_MESSAGING_BROKER_ADAPTER=1",
				"SKY10_MESSAGING_BROKER_HELPER_MODE=crash-once-after-checkpoint",
				"SKY10_MESSAGING_BROKER_HELPER_CRASH_MARKER=" + crashMarker,
			},
		},
	}); err != nil {
		t.Fatalf("RegisterConnection() error = %v", err)
	}
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}
	if _, err := b.PollConnection(ctx, connection.ID, 10); err != nil {
		t.Fatalf("PollConnection(first) error = %v", err)
	}
	if _, ok := store.GetMessage("msg/latisha"); !ok {
		t.Fatal("GetMessage(msg/latisha) after first poll = missing")
	}
	checkpoint, ok := store.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-1" {
		t.Fatalf("GetCheckpoint() after first poll = %+v, %v; want cursor-1", checkpoint, ok)
	}

	if _, err := b.PollConnection(ctx, connection.ID, 10); err == nil {
		t.Fatal("PollConnection(crashing) error = nil, want adapter crash")
	}
	checkpoint, ok = store.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-1" {
		t.Fatalf("GetCheckpoint() after crash = %+v, %v; want cursor-1", checkpoint, ok)
	}
	receivedEvents := 0
	for _, event := range store.ListConnectionEvents(connection.ID) {
		if event.Type == messaging.EventTypeMessageReceived {
			receivedEvents++
		}
	}
	if receivedEvents != 1 {
		t.Fatalf("message_received events after crash = %d, want 1", receivedEvents)
	}

	managed, ok := b.manager.Get(string(connection.ID))
	if !ok {
		t.Fatal("managed adapter missing after crash")
	}
	waitForManagedRestart(t, managed)

	recoverCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	recovered, err := b.PollConnection(recoverCtx, connection.ID, 10)
	if err != nil {
		t.Fatalf("PollConnection(recovered) error = %v", err)
	}
	if len(recovered.Events) != 0 {
		t.Fatalf("PollConnection(recovered) events = %+v, want none after checkpoint", recovered.Events)
	}
	checkpoint, ok = store.GetCheckpoint(connection.ID)
	if !ok || checkpoint.Cursor != "cursor-2" {
		t.Fatalf("GetCheckpoint() after recovery = %+v, %v; want cursor-2", checkpoint, ok)
	}
	if _, ok := store.GetMessage("msg/latisha"); !ok {
		t.Fatal("GetMessage(msg/latisha) after recovery = missing")
	}
	if state := managed.Snapshot(); state.RestartCount < 1 || !state.Running {
		t.Fatalf("managed adapter state after recovery = %+v, want restarted and running", state)
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

func TestBrokerPollConnectionDeduplicatesStableBlankEvents(t *testing.T) {
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

	if _, err := b.PollConnection(ctx, connection.ID, 10); err != nil {
		t.Fatalf("PollConnection(first) error = %v", err)
	}
	if _, err := b.PollConnection(ctx, connection.ID, 10); err != nil {
		t.Fatalf("PollConnection(second) error = %v", err)
	}

	receivedEvents := 0
	for _, event := range store.ListConnectionEvents(connection.ID) {
		if event.Type == messaging.EventTypeMessageReceived {
			receivedEvents++
		}
	}
	if receivedEvents != 1 {
		t.Fatalf("message_received events = %d, want 1", receivedEvents)
	}
}

func TestBrokerResolvePolicyUsesExposureOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(ctx, Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := messaging.Connection{
		ID:              "slack/work",
		AdapterID:       "slack",
		Label:           "Work Slack",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: "policy/default",
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	defaultPolicy := messaging.Policy{
		ID:   "policy/default",
		Name: "Default",
		Rules: messaging.PolicyRules{
			ReadInbound: true,
		},
	}
	overridePolicy := messaging.Policy{
		ID:   "policy/exposure",
		Name: "Exposure",
		Rules: messaging.PolicyRules{
			SendMessages: true,
		},
	}
	if err := store.PutPolicy(ctx, defaultPolicy); err != nil {
		t.Fatalf("PutPolicy(default) error = %v", err)
	}
	if err := store.PutPolicy(ctx, overridePolicy); err != nil {
		t.Fatalf("PutPolicy(override) error = %v", err)
	}
	exposure := messaging.Exposure{
		ID:           "exposure/runtime",
		ConnectionID: connection.ID,
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		PolicyID:     overridePolicy.ID,
		Enabled:      true,
	}
	if err := store.PutExposure(ctx, exposure); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}

	effective, err := b.ResolvePolicy(connection.ID, exposure.ID)
	if err != nil {
		t.Fatalf("ResolvePolicy() error = %v", err)
	}
	if effective.Policy.ID != overridePolicy.ID {
		t.Fatalf("effective policy = %q, want %q", effective.Policy.ID, overridePolicy.ID)
	}
}

func TestBrokerEvaluateSendAndSearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	b, err := New(ctx, Config{
		Store:   store,
		RootDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := messaging.Connection{
		ID:              "gmail/work",
		AdapterID:       "gmail",
		Label:           "Work Gmail",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: "policy/reply-only",
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	policy := messaging.Policy{
		ID:   "policy/reply-only",
		Name: "Reply Only",
		Rules: messaging.PolicyRules{
			ReadInbound:        true,
			CreateDrafts:       true,
			SendMessages:       true,
			RequireApproval:    true,
			ReplyOnly:          true,
			AllowAttachments:   false,
			SearchMessages:     true,
			AllowedIdentityIDs: []messaging.IdentityID{"identity/work"},
		},
	}
	if err := store.PutPolicy(ctx, policy); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}

	decision, err := b.EvaluateSend(connection.ID, "", messaging.Draft{
		ID:              "draft/reply",
		ConnectionID:    connection.ID,
		ConversationID:  "conv/thread",
		LocalIdentityID: "identity/work",
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "reply"}},
		Status:          messaging.DraftStatusPending,
	}, false)
	if err != nil {
		t.Fatalf("EvaluateSend() error = %v", err)
	}
	if decision.Outcome != messagingpolicy.OutcomeRequireApproval {
		t.Fatalf("EvaluateSend() outcome = %q, want require_approval", decision.Outcome)
	}

	decision, err = b.EvaluateSend(connection.ID, "", messaging.Draft{
		ID:              "draft/new",
		ConnectionID:    connection.ID,
		ConversationID:  "conv/new",
		LocalIdentityID: "identity/work",
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "new"}},
		Status:          messaging.DraftStatusPending,
	}, true)
	if err != nil {
		t.Fatalf("EvaluateSend(new) error = %v", err)
	}
	if decision.Outcome != messagingpolicy.OutcomeDeny {
		t.Fatalf("EvaluateSend(new) outcome = %q, want deny", decision.Outcome)
	}

	decision, err = b.EvaluateSearch(connection.ID, "", messagingpolicy.SearchScopeMessages)
	if err != nil {
		t.Fatalf("EvaluateSearch(messages) error = %v", err)
	}
	if decision.Outcome != messagingpolicy.OutcomeAllow {
		t.Fatalf("EvaluateSearch(messages) outcome = %q, want allow", decision.Outcome)
	}

	decision, err = b.EvaluateSearch(connection.ID, "", messagingpolicy.SearchScopeConversations)
	if err != nil {
		t.Fatalf("EvaluateSearch(conversations) error = %v", err)
	}
	if decision.Outcome != messagingpolicy.OutcomeDeny {
		t.Fatalf("EvaluateSearch(conversations) outcome = %q, want deny", decision.Outcome)
	}
}

func TestBrokerDraftApprovalAndSendFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := filepath.Join(t.TempDir(), "messaging-runtime")
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	mailboxStore, err := agentmailbox.NewStore(ctx, agentmailbox.NewPrivateKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("mailbox.NewStore() error = %v", err)
	}
	approvalTo := agentmailbox.Principal{
		ID:    "human:alice",
		Kind:  agentmailbox.PrincipalKindHuman,
		Scope: agentmailbox.ScopePrivateNetwork,
	}
	b, err := New(ctx, Config{
		Store:           store,
		RootDir:         rootDir,
		ApprovalMailbox: mailboxStore,
		ApprovalTo:      &approvalTo,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = b.Close() }()

	connection := messaging.Connection{
		ID:              "slack/work",
		AdapterID:       "slack",
		Label:           "Work Slack",
		Status:          messaging.ConnectionStatusConnecting,
		DefaultPolicyID: "policy/reply-approval",
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/reply-approval",
		Name: "Reply With Approval",
		Rules: messaging.PolicyRules{
			ReadInbound:        true,
			CreateDrafts:       true,
			SendMessages:       true,
			RequireApproval:    true,
			ReplyOnly:          true,
			AllowAttachments:   true,
			AllowedIdentityIDs: []messaging.IdentityID{"identity/test"},
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}
	if err := store.PutExposure(ctx, messaging.Exposure{
		ID:           "exposure/hermes",
		ConnectionID: connection.ID,
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
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
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}

	conversation := messaging.Conversation{
		ID:              "conv/latisha",
		ConnectionID:    connection.ID,
		LocalIdentityID: "identity/test",
		Kind:            messaging.ConversationKindDirect,
		RemoteID:        "D123",
		Title:           "Latisha",
		Participants: []messaging.Participant{
			{Kind: messaging.ParticipantKindBot, IdentityID: "identity/test", IsLocal: true, DisplayName: "Test Bot"},
			{Kind: messaging.ParticipantKindUser, RemoteID: "U234", DisplayName: "Latisha"},
		},
	}
	if err := store.PutConversation(ctx, conversation); err != nil {
		t.Fatalf("PutConversation() error = %v", err)
	}

	draft := messaging.Draft{
		ID:              "draft/reply",
		ConnectionID:    connection.ID,
		ConversationID:  conversation.ID,
		LocalIdentityID: "identity/test",
		ReplyToRemoteID: "slack-msg-1",
		Parts: []messaging.MessagePart{
			{Kind: messaging.MessagePartKindMarkdown, Text: "I'll take care of it."},
		},
		Status: messaging.DraftStatusPending,
	}
	draftResult, err := b.CreateDraft(ctx, "exposure/hermes", draft)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if draftResult.Draft.Status != messaging.DraftStatusPending {
		t.Fatalf("draft status = %q, want pending", draftResult.Draft.Status)
	}
	if draftResult.Workflow.Status != messaging.WorkflowStatusDrafted {
		t.Fatalf("workflow status = %q, want drafted", draftResult.Workflow.Status)
	}

	sendResult, err := b.RequestSendDraft(ctx, "exposure/hermes", draft.ID, false)
	if err != nil {
		t.Fatalf("RequestSendDraft(approval) error = %v", err)
	}
	if sendResult.Approval == nil {
		t.Fatal("RequestSendDraft() approval = nil, want approval")
	}
	if sendResult.Draft.Status != messaging.DraftStatusApprovalRequired {
		t.Fatalf("draft status after request = %q, want approval_required", sendResult.Draft.Status)
	}
	if sendResult.Workflow.Status != messaging.WorkflowStatusAwaitingApproval {
		t.Fatalf("workflow status after request = %q, want awaiting_approval", sendResult.Workflow.Status)
	}

	inbox := mailboxStore.ListInbox("human:alice")
	if len(inbox) != 1 {
		t.Fatalf("mailbox inbox len = %d, want 1", len(inbox))
	}
	if inbox[0].Item.Kind != agentmailbox.ItemKindApprovalRequest {
		t.Fatalf("mailbox item kind = %q, want approval_request", inbox[0].Item.Kind)
	}

	approvalResult, err := b.ApproveDraftSend(ctx, sendResult.Approval.ID, "human:alice")
	if err != nil {
		t.Fatalf("ApproveDraftSend() error = %v", err)
	}
	if approvalResult.Approval.Status != messaging.ApprovalStatusApproved {
		t.Fatalf("approval status = %q, want approved", approvalResult.Approval.Status)
	}
	if approvalResult.Draft.Status != messaging.DraftStatusApproved {
		t.Fatalf("draft status after approval = %q, want approved", approvalResult.Draft.Status)
	}

	sendResult, err = b.RequestSendDraft(ctx, "exposure/hermes", draft.ID, false)
	if err != nil {
		t.Fatalf("RequestSendDraft(send) error = %v", err)
	}
	if sendResult.Message == nil {
		t.Fatal("RequestSendDraft() message = nil, want outbound message")
	}
	if sendResult.Message.Direction != messaging.MessageDirectionOutbound {
		t.Fatalf("outbound message direction = %q, want outbound", sendResult.Message.Direction)
	}
	if sendResult.Draft.Status != messaging.DraftStatusSent {
		t.Fatalf("draft status after send = %q, want sent", sendResult.Draft.Status)
	}
	if sendResult.Workflow.Status != messaging.WorkflowStatusSent {
		t.Fatalf("workflow status after send = %q, want sent", sendResult.Workflow.Status)
	}
	if storedMessage, ok := store.GetMessage(sendResult.Message.ID); !ok || storedMessage.Status != messaging.MessageStatusSent {
		t.Fatalf("GetMessage() = %+v, %v; want sent message", storedMessage, ok)
	}

	eventsBefore := len(store.ListConnectionEvents(connection.ID))
	repeatResult, err := b.RequestSendDraft(ctx, "exposure/hermes", draft.ID, false)
	if err != nil {
		t.Fatalf("RequestSendDraft(repeat) error = %v", err)
	}
	if repeatResult.Message == nil || repeatResult.Message.ID != sendResult.Message.ID {
		t.Fatalf("repeat send message = %+v, want %s", repeatResult.Message, sendResult.Message.ID)
	}
	if got := len(store.ListConnectionEvents(connection.ID)); got != eventsBefore {
		t.Fatalf("connection events len after repeat send = %d, want %d", got, eventsBefore)
	}
}

func TestBrokerMessageManagementPersistsContainersAndPlacements(t *testing.T) {
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

	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   "policy/manage-archive",
		Name: "Manage Archive",
		Rules: messaging.PolicyRules{
			ReadInbound:         true,
			ManageMessages:      true,
			AllowedContainerIDs: []messaging.ContainerID{"container/archive"},
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}
	if err := store.PutExposure(ctx, messaging.Exposure{
		ID:           "exposure/hermes",
		ConnectionID: "slack/work",
		SubjectID:    "runtime:hermes",
		SubjectKind:  messaging.ExposureSubjectKindRuntime,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("PutExposure() error = %v", err)
	}

	connection := messaging.Connection{
		ID:              "slack/work",
		AdapterID:       "slack",
		Label:           "Work Slack",
		Status:          messaging.ConnectionStatusConnecting,
		DefaultPolicyID: "policy/manage-archive",
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
	if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
		t.Fatalf("ConnectConnection() error = %v", err)
	}
	if _, err := b.PollConnection(ctx, connection.ID, 10); err != nil {
		t.Fatalf("PollConnection() error = %v", err)
	}
	if _, ok := store.GetPlacement("msg/latisha", "container/inbox"); !ok {
		t.Fatal("GetPlacement(msg/latisha, inbox) = false, want hydrated inbound placement")
	}

	containers, err := b.ListContainers(ctx, protocol.ListContainersParams{ConnectionID: connection.ID})
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers.Containers) != 3 {
		t.Fatalf("ListContainers() len = %d, want 3", len(containers.Containers))
	}

	result, err := b.MoveMessages(ctx, "exposure/hermes", protocol.MoveMessagesParams{
		ConnectionID:           connection.ID,
		MessageIDs:             []messaging.MessageID{"msg/latisha"},
		DestinationContainerID: "container/archive",
	})
	if err != nil {
		t.Fatalf("MoveMessages() error = %v", err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Placement == nil {
		t.Fatalf("MoveMessages() changes = %+v, want placement change", result.Changes)
	}
	placement, ok := store.GetPlacement("msg/latisha", "container/archive")
	if !ok || placement.RemoteID != "999" {
		t.Fatalf("GetPlacement(msg/latisha, archive) = %+v, %v; want remote 999", placement, ok)
	}
	if _, ok := store.GetPlacement("msg/latisha", "container/inbox"); ok {
		t.Fatal("GetPlacement(msg/latisha, inbox) = true, want removed")
	}

	_, err = b.MoveMessages(ctx, "exposure/hermes", protocol.MoveMessagesParams{
		ConnectionID:           connection.ID,
		MessageIDs:             []messaging.MessageID{"msg/latisha"},
		DestinationContainerID: "container/project",
	})
	if err == nil || !strings.Contains(err.Error(), "policy does not allow container") {
		t.Fatalf("MoveMessages(disallowed) error = %v, want container policy denial", err)
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
	if !strings.Contains(paths.SecretsDir, filepath.Join("runtime", "secrets")) {
		t.Fatalf("paths.SecretsDir = %q, want runtime secrets dir", paths.SecretsDir)
	}
}

func waitForManagedRestart(t *testing.T, managed *messagingruntime.ManagedAdapter) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		state := managed.Snapshot()
		if state.RestartCount >= 1 && state.Running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed adapter did not restart: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
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
	mode := os.Getenv("SKY10_MESSAGING_BROKER_HELPER_MODE")

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
							ListContainers:    true,
							CreateDrafts:      true,
							UpdateDrafts:      true,
							SendMessages:      true,
							MoveMessages:      true,
							ApplyLabels:       true,
							MarkRead:          true,
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
			metadata := map[string]string{}
			if params.Credential != nil {
				metadata["credential_ref"] = params.Credential.Ref
				metadata["credential_content_type"] = params.Credential.ContentType
				if params.Credential.Blob.LocalPath != "" {
					metadata["credential_path_present"] = "true"
				}
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ConnectResult{
					Status: messaging.ConnectionStatusConnected,
					Identities: []messaging.Identity{{
						ID:           "identity/test",
						ConnectionID: params.Connection.ID,
						Kind:         messaging.IdentityKindBot,
						RemoteID:     "U123",
						DisplayName:  "Test Bot",
						CanReceive:   true,
						CanSend:      true,
						IsDefault:    true,
					}},
					Metadata: metadata,
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodRefresh):
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.RefreshResult{
					Status: messaging.ConnectionStatusConnected,
					Auth: messaging.AuthInfo{
						Method:          messaging.AuthMethodNone,
						ExternalAccount: "refreshed@example.test",
					},
					Metadata: map[string]string{
						"refreshed": "true",
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodPoll):
			var params protocol.PollParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			if mode == "crash-once-after-checkpoint" && params.Checkpoint != nil && params.Checkpoint.Cursor == "cursor-1" {
				marker := os.Getenv("SKY10_MESSAGING_BROKER_HELPER_CRASH_MARKER")
				if marker == "" {
					return errors.New("crash marker is required")
				}
				if _, err := os.Stat(marker); os.IsNotExist(err) {
					if err := os.WriteFile(marker, []byte("crashed"), 0o600); err != nil {
						return err
					}
					os.Exit(31)
				}
			}
			checkpointAware := mode == "checkpoint-aware" || mode == "crash-once-after-checkpoint"
			if checkpointAware && params.Checkpoint != nil && params.Checkpoint.Cursor == "cursor-1" {
				if err := enc.Write(messagingruntime.Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: mustJSON(protocol.PollResult{
						Checkpoint: &protocol.Checkpoint{
							Cursor: "cursor-2",
						},
					}),
				}); err != nil {
					return err
				}
				continue
			}
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
					Draft: protocol.DraftRecord{
						Draft: draft,
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodUpdateDraft):
			var params protocol.UpdateDraftParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			draft := params.Draft.Draft
			if draft.Metadata == nil {
				draft.Metadata = map[string]string{}
			}
			draft.Metadata["native_draft"] = "updated"
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.UpdateDraftResult{
					Draft: protocol.DraftRecord{
						Draft: draft,
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
						Placements: []messaging.Placement{{
							MessageID:    message.ID,
							ConnectionID: message.ConnectionID,
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
				Result: mustJSON(protocol.ListContainersResult{
					Containers: []messaging.Container{
						{
							ID:           "container/archive",
							ConnectionID: "slack/work",
							Kind:         messaging.ContainerKindArchive,
							Name:         "Archive",
							RemoteID:     "archive",
						},
						{
							ID:           "container/inbox",
							ConnectionID: "slack/work",
							Kind:         messaging.ContainerKindInbox,
							Name:         "Inbox",
							RemoteID:     "inbox",
						},
						{
							ID:           "container/project",
							ConnectionID: "slack/work",
							Kind:         messaging.ContainerKindLabel,
							Name:         "Project Phoenix",
							RemoteID:     "project",
						},
					},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodMoveMessages):
			var params protocol.MoveMessagesParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.ManageMessagesResult{
					Changes: []protocol.PlacementChange{{
						MessageID: params.MessageIDs[0],
						Removed:   []messaging.ContainerID{"container/inbox"},
						Added:     []messaging.ContainerID{params.DestinationContainerID},
						Placement: &messaging.Placement{
							MessageID:    params.MessageIDs[0],
							ConnectionID: params.ConnectionID,
							ContainerID:  params.DestinationContainerID,
							RemoteID:     "999",
						},
					}},
					Events: []messaging.Event{{
						Type:         messaging.EventTypeMessageMoved,
						ConnectionID: params.ConnectionID,
						MessageID:    params.MessageIDs[0],
					}},
				}),
			}); err != nil {
				return err
			}
		case string(protocol.MethodSendMessage), string(protocol.MethodReplyMessage):
			var draft protocol.DraftRecord
			if req.Method == string(protocol.MethodSendMessage) {
				var params protocol.SendMessageParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					return err
				}
				draft = params.Draft
			} else {
				var params protocol.ReplyMessageParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					return err
				}
				draft = params.Draft
			}
			message := messaging.Message{
				ID:              messaging.MessageID("sent/" + string(draft.Draft.ID)),
				ConnectionID:    draft.Draft.ConnectionID,
				ConversationID:  draft.Draft.ConversationID,
				LocalIdentityID: draft.Draft.LocalIdentityID,
				Direction:       messaging.MessageDirectionOutbound,
				Sender: messaging.Participant{
					Kind:        messaging.ParticipantKindBot,
					IdentityID:  draft.Draft.LocalIdentityID,
					IsLocal:     true,
					DisplayName: "Test Bot",
				},
				Parts:     draft.Draft.Parts,
				CreatedAt: time.Date(2026, 4, 22, 15, 0, 0, 0, time.UTC),
				Status:    messaging.MessageStatusSent,
			}
			if err := enc.Write(messagingruntime.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mustJSON(protocol.SendResult{
					Message: protocol.MessageRecord{Message: message},
					Status:  messaging.MessageStatusSent,
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
