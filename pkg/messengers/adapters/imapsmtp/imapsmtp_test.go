package imapsmtp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

func TestParseConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	credential := stagedCredential(t, `{"username":"mailer@example.com","password":"secret","display_name":"Mailer"}`)
	cfg, err := parseConfig(messaging.Connection{
		ID:        "imap/work",
		AdapterID: "imap-smtp",
		Label:     "Work Mail",
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBasic,
			CredentialRef: "secret://imap/work",
		},
		Metadata: map[string]string{
			metaIMAPHost: "imap.example.com",
			metaSMTPHost: "smtp.example.com",
		},
	}, credential)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.Mailbox != "INBOX" {
		t.Fatalf("Mailbox = %q, want INBOX", cfg.Mailbox)
	}
	if cfg.IMAP.Port != 993 {
		t.Fatalf("IMAP.Port = %d, want 993", cfg.IMAP.Port)
	}
	if cfg.SMTP.Port != 587 {
		t.Fatalf("SMTP.Port = %d, want 587", cfg.SMTP.Port)
	}
	if cfg.EmailAddress != "mailer@example.com" {
		t.Fatalf("EmailAddress = %q, want mailer@example.com", cfg.EmailAddress)
	}
	if cfg.DisplayName != "Mailer" {
		t.Fatalf("DisplayName = %q, want Mailer", cfg.DisplayName)
	}
}

func TestServerHandleConnectAndListIdentities(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }

	connect := protocol.ConnectParams{
		Connection: messaging.Connection{
			ID:        "imap/work",
			AdapterID: "imap-smtp",
			Label:     "Work Mail",
			Auth: messaging.AuthInfo{
				Method:        messaging.AuthMethodBasic,
				CredentialRef: "secret://imap/work",
			},
			Metadata: map[string]string{
				metaIMAPHost:     "imap.example.com",
				metaSMTPHost:     "smtp.example.com",
				metaEmailAddress: "mailer@example.com",
			},
		},
		Credential: stagedCredential(t, `{"username":"mailer@example.com","password":"secret"}`),
	}
	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodConnect, connect))
	if resp.Error != nil {
		t.Fatalf("connect error = %v", resp.Error)
	}
	var result protocol.ConnectResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode connect result: %v", err)
	}
	if len(result.Identities) != 1 || result.Identities[0].Address != "mailer@example.com" {
		t.Fatalf("connect identities = %+v", result.Identities)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodListIdentities, protocol.ListIdentitiesParams{
		ConnectionID: "imap/work",
	}))
	if resp.Error != nil {
		t.Fatalf("list identities error = %v", resp.Error)
	}
	var identities protocol.ListIdentitiesResult
	if err := json.Unmarshal(resp.Result, &identities); err != nil {
		t.Fatalf("decode list identities result: %v", err)
	}
	if len(identities.Identities) != 1 || identities.Identities[0].ConnectionID != "imap/work" {
		t.Fatalf("identities = %+v", identities.Identities)
	}
}

func TestServerHandlePollCachesResults(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }
	server.pollFunc = func(context.Context, adapterConfig, *protocol.Checkpoint, int) (pollSnapshot, error) {
		conversation := messaging.Conversation{
			ID:              "conv/1",
			ConnectionID:    "imap/work",
			LocalIdentityID: "identity/imap/work",
			Kind:            messaging.ConversationKindEmailThread,
			Title:           "Board Update",
		}
		message := messaging.Message{
			ID:              "msg/1",
			ConnectionID:    "imap/work",
			ConversationID:  conversation.ID,
			LocalIdentityID: "identity/imap/work",
			RemoteID:        "mid-1",
			Direction:       messaging.MessageDirectionInbound,
			Sender: messaging.Participant{
				Kind:        messaging.ParticipantKindUser,
				Address:     "latisha@example.com",
				DisplayName: "Latisha",
			},
			Parts:     []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "hello"}},
			CreatedAt: time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC),
			Status:    messaging.MessageStatusReceived,
		}
		return pollSnapshot{
			Events: []messaging.Event{{
				ID:             "evt/1",
				Type:           messaging.EventTypeMessageReceived,
				ConnectionID:   "imap/work",
				ConversationID: conversation.ID,
				MessageID:      message.ID,
				Timestamp:      message.CreatedAt,
			}},
			Conversations: []messaging.Conversation{conversation},
			Messages:      []messaging.Message{message},
			Checkpoint:    &protocol.Checkpoint{Cursor: "1", Sequence: "1"},
		}, nil
	}
	connectServerForTest(t, server)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodPoll, protocol.PollParams{
		ConnectionID: "imap/work",
	}))
	if resp.Error != nil {
		t.Fatalf("poll error = %v", resp.Error)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodListConversations, protocol.ListConversationsParams{
		ConnectionID: "imap/work",
	}))
	if resp.Error != nil {
		t.Fatalf("list conversations error = %v", resp.Error)
	}
	var conversations protocol.ListConversationsResult
	if err := json.Unmarshal(resp.Result, &conversations); err != nil {
		t.Fatalf("decode conversations: %v", err)
	}
	if len(conversations.Conversations) != 1 || conversations.Conversations[0].ID != "conv/1" {
		t.Fatalf("conversations = %+v", conversations.Conversations)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodGetMessage, protocol.GetMessageParams{
		ConnectionID: "imap/work",
		MessageID:    "msg/1",
	}))
	if resp.Error != nil {
		t.Fatalf("get message error = %v", resp.Error)
	}
	var message protocol.GetMessageResult
	if err := json.Unmarshal(resp.Result, &message); err != nil {
		t.Fatalf("decode get message: %v", err)
	}
	if message.Message.Message.Sender.DisplayName != "Latisha" {
		t.Fatalf("message sender = %+v", message.Message.Message.Sender)
	}
}

func stagedCredential(t *testing.T, raw string) *protocol.ResolvedCredential {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return &protocol.ResolvedCredential{
		Ref:         "secret://imap/work",
		AuthMethod:  messaging.AuthMethodBasic,
		ContentType: "application/json",
		Blob: protocol.BlobRef{
			ID:        "credential:test",
			LocalPath: path,
		},
	}
}

func rpcRequest(t *testing.T, method protocol.Method, params any) messagingruntime.Request {
	t.Helper()

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return messagingruntime.Request{
		JSONRPC: "2.0",
		Method:  string(method),
		Params:  raw,
		ID:      1,
	}
}

func connectServerForTest(t *testing.T, server *service) {
	t.Helper()

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodConnect, protocol.ConnectParams{
		Connection: messaging.Connection{
			ID:        "imap/work",
			AdapterID: "imap-smtp",
			Label:     "Work Mail",
			Auth: messaging.AuthInfo{
				Method:        messaging.AuthMethodBasic,
				CredentialRef: "secret://imap/work",
			},
			Metadata: map[string]string{
				metaIMAPHost:     "imap.example.com",
				metaSMTPHost:     "smtp.example.com",
				metaEmailAddress: "mailer@example.com",
			},
		},
		Credential: stagedCredential(t, `{"username":"mailer@example.com","password":"secret"}`),
	}))
	if resp.Error != nil {
		t.Fatalf("connect error = %v", resp.Error)
	}
}
