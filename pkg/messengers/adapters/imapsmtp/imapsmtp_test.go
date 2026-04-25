package imapsmtp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
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

func TestServerDescribeDeclaresMailboxSearchOnly(t *testing.T) {
	t.Parallel()

	server := newServer()
	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodDescribe, protocol.DescribeParams{}))
	if resp.Error != nil {
		t.Fatalf("describe error = %v", resp.Error)
	}
	var result protocol.DescribeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode describe result: %v", err)
	}
	caps := result.Adapter.Capabilities
	if !caps.SearchMessages {
		t.Fatal("SearchMessages = false, want true")
	}
	if caps.SearchIdentities || caps.SearchConversations || caps.ResolveIdentity {
		t.Fatalf("unexpected rich lookup capabilities: %+v", caps)
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
				metaIMAPHost:           "imap.example.com",
				metaSMTPHost:           "smtp.example.com",
				metaEmailAddress:       "mailer@example.com",
				metaIMAPArchiveMailbox: "Archive",
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

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodListContainers, protocol.ListContainersParams{
		ConnectionID: "imap/work",
	}))
	if resp.Error != nil {
		t.Fatalf("list containers error = %v", resp.Error)
	}
	var containers protocol.ListContainersResult
	if err := json.Unmarshal(resp.Result, &containers); err != nil {
		t.Fatalf("decode list containers result: %v", err)
	}
	if len(containers.Containers) != 2 || containers.Containers[0].Kind != messaging.ContainerKindArchive || containers.Containers[1].Kind != messaging.ContainerKindInbox {
		t.Fatalf("containers = %+v, want archive and inbox", containers.Containers)
	}
}

func TestServerHandleSearchMessagesCachesResults(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }
	server.searchFunc = func(_ context.Context, cfg adapterConfig, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
		if cfg.ConnectionID != "imap/work" {
			t.Fatalf("search cfg connection = %q, want imap/work", cfg.ConnectionID)
		}
		if params.Query != "board update" || params.Limit != 10 {
			t.Fatalf("search params = %+v, want query and limit", params)
		}
		conversation := messaging.Conversation{
			ID:              "conv/search",
			ConnectionID:    cfg.ConnectionID,
			LocalIdentityID: "identity/imap/work",
			Kind:            messaging.ConversationKindEmailThread,
			Title:           "Board Update",
		}
		message := messaging.Message{
			ID:              "msg/search",
			ConnectionID:    cfg.ConnectionID,
			ConversationID:  conversation.ID,
			LocalIdentityID: "identity/imap/work",
			RemoteID:        "mid-search",
			Direction:       messaging.MessageDirectionInbound,
			Sender: messaging.Participant{
				Kind:        messaging.ParticipantKindUser,
				Address:     "latisha@example.com",
				DisplayName: "Latisha",
			},
			Parts:     []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "board update body"}},
			CreatedAt: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
			Status:    messaging.MessageStatusReceived,
		}
		return protocol.SearchMessagesResult{
			Hits: []protocol.MessageSearchHit{{
				Message:      protocol.MessageRecord{Message: message},
				Conversation: &conversation,
			}},
		}, nil
	}
	connectServerForTest(t, server)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodSearchMessages, protocol.SearchMessagesParams{
		ConnectionID: "imap/work",
		Query:        "board update",
		Source:       protocol.SearchSourceRemote,
		PageRequest: protocol.PageRequest{
			Limit: 10,
		},
	}))
	if resp.Error != nil {
		t.Fatalf("search messages error = %v", resp.Error)
	}
	var search protocol.SearchMessagesResult
	if err := json.Unmarshal(resp.Result, &search); err != nil {
		t.Fatalf("decode search messages: %v", err)
	}
	if search.Count != 1 || search.Source != protocol.SearchSourceRemote || search.Hits[0].Message.Message.ID != "msg/search" {
		t.Fatalf("search result = %+v, want one remote msg/search", search)
	}
	if len(search.Hits[0].Message.Placements) != 1 || search.Hits[0].Message.Placements[0].ContainerID == "" {
		t.Fatalf("search placements = %+v, want default mailbox placement", search.Hits[0].Message.Placements)
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
	if len(conversations.Conversations) != 1 || conversations.Conversations[0].ID != "conv/search" {
		t.Fatalf("conversations = %+v, want cached conv/search", conversations.Conversations)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodGetMessage, protocol.GetMessageParams{
		ConnectionID: "imap/work",
		MessageID:    "msg/search",
	}))
	if resp.Error != nil {
		t.Fatalf("get message error = %v", resp.Error)
	}
	var got protocol.GetMessageResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if got.Message.Message.ID != "msg/search" {
		t.Fatalf("cached message = %+v, want msg/search", got.Message.Message)
	}
}

func TestServerHandleSearchMessagesRejectsIndexedSource(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }
	connectServerForTest(t, server)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodSearchMessages, protocol.SearchMessagesParams{
		ConnectionID: "imap/work",
		Query:        "board",
		Source:       protocol.SearchSourceIndexed,
	}))
	if resp.Error == nil {
		t.Fatal("search indexed source error = nil, want adapter rejection")
	}
}

func TestNormalizeFetchedMessageParsesRFC822Fixture(t *testing.T) {
	t.Parallel()

	cfg := adapterConfig{
		ConnectionID: "imap/work",
		Label:        "Work Mail",
		EmailAddress: "mailer@example.com",
		DisplayName:  "Mailer",
		Mailbox:      "INBOX",
	}
	internalDate := time.Date(2026, 4, 25, 14, 30, 0, 0, time.UTC)
	raw := crlf(`From: Latisha <latisha@example.com>
To: Mailer <mailer@example.com>
Cc: Board <board@example.com>
Subject: Re: Board Update
Message-ID: <reply@example.com>
In-Reply-To: <root@example.com>
References: <root@example.com> <prior@example.com>
X-Sky10-Workflow-ID: workflow/board
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="sky10"

--sky10
Content-Type: text/plain; charset=UTF-8
Content-Transfer-Encoding: quoted-printable

Hello=2C board update.
--sky10
Content-Type: text/html; charset=UTF-8

<p>Hello, board update.</p>
--sky10--
`)
	item := &imapclient.FetchMessageBuffer{
		UID:          42,
		InternalDate: internalDate,
		Envelope: &imap.Envelope{
			Date:    time.Date(2026, 4, 25, 14, 29, 0, 0, time.UTC),
			Subject: "Re: Board Update",
			From: []imap.Address{{
				Name:    "Latisha",
				Mailbox: "latisha",
				Host:    "example.com",
			}},
			To: []imap.Address{{
				Name:    "Mailer",
				Mailbox: "mailer",
				Host:    "example.com",
			}},
			Cc: []imap.Address{{
				Name:    "Board",
				Mailbox: "board",
				Host:    "example.com",
			}},
			InReplyTo: []string{"root@example.com"},
			MessageID: "reply@example.com",
		},
	}

	conversation, message, err := normalizeFetchedMessage(cfg, item, []byte(raw))
	if err != nil {
		t.Fatalf("normalize fetched message: %v", err)
	}
	if conversation.ID != conversationIDFor(cfg, "root@example.com") || conversation.RemoteID != "root@example.com" {
		t.Fatalf("conversation thread = (%s, %q), want root thread", conversation.ID, conversation.RemoteID)
	}
	if conversation.Title != "Re: Board Update" || conversation.Kind != messaging.ConversationKindEmailThread {
		t.Fatalf("conversation = %+v, want email thread title", conversation)
	}
	if len(conversation.Participants) != 3 || !conversation.Participants[0].IsLocal || conversation.Participants[1].Address != "latisha@example.com" {
		t.Fatalf("participants = %+v, want local account plus remote senders", conversation.Participants)
	}
	if message.ID != messageIDFor(cfg, 42) || message.RemoteID != "reply@example.com" {
		t.Fatalf("message ids = (%s, %q), want normalized uid and email message id", message.ID, message.RemoteID)
	}
	if message.Direction != messaging.MessageDirectionInbound || message.Status != messaging.MessageStatusReceived {
		t.Fatalf("message state = (%s, %s), want inbound received", message.Direction, message.Status)
	}
	if message.Sender.Address != "latisha@example.com" || message.Sender.DisplayName != "Latisha" {
		t.Fatalf("sender = %+v, want Latisha", message.Sender)
	}
	if !message.CreatedAt.Equal(internalDate) || message.ReplyToRemoteID != "root@example.com" {
		t.Fatalf("created/reply = (%s, %q), want internal date and root reply", message.CreatedAt, message.ReplyToRemoteID)
	}
	if len(message.Parts) != 2 || message.Parts[0].Kind != messaging.MessagePartKindText || !strings.Contains(message.Parts[0].Text, "Hello, board update.") {
		t.Fatalf("parts = %+v, want decoded text plus html", message.Parts)
	}
	if message.Parts[1].Kind != messaging.MessagePartKindHTML || !strings.Contains(message.Parts[1].Text, "<p>Hello, board update.</p>") {
		t.Fatalf("html part = %+v, want html body", message.Parts[1])
	}
	if message.Metadata["references"] != "root@example.com prior@example.com" || message.Metadata["sky10_workflow_id"] != "workflow/board" {
		t.Fatalf("metadata = %+v, want references and x-sky10 workflow", message.Metadata)
	}

	placement := placementForMessage(cfg, message)
	if placement.ContainerID != containerIDForMailbox(cfg, "INBOX") || placement.RemoteID != "42" {
		t.Fatalf("placement = %+v, want INBOX uid 42", placement)
	}
	checkpoint := nextCheckpoint(&protocol.Checkpoint{Metadata: map[string]string{"mailbox": "INBOX"}}, item.UID)
	if checkpoint.Cursor != "42" || checkpoint.Sequence != "42" || checkpoint.Metadata["mailbox"] != "INBOX" {
		t.Fatalf("checkpoint = %+v, want uid cursor with metadata preserved", checkpoint)
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
	if len(message.Message.Placements) != 1 || message.Message.Placements[0].ContainerID == "" {
		t.Fatalf("message placements = %+v, want one IMAP mailbox placement", message.Message.Placements)
	}
}

func TestServerHandleSendResolvesMetadataRecipientsAndCachesSentMessage(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }
	server.sendFunc = func(_ context.Context, cfg adapterConfig, draft messaging.Draft, recipients []string, headers outboundHeaders) (sendSnapshot, error) {
		if cfg.ConnectionID != "imap/work" {
			t.Fatalf("send cfg connection = %q, want imap/work", cfg.ConnectionID)
		}
		if got := strings.Join(recipients, ","); got != "board@example.com,latisha@example.com" {
			t.Fatalf("recipients = %q, want sorted unique metadata recipients", got)
		}
		if headers.Subject != "Board Update" || headers.InReplyTo != "" || len(headers.References) != 0 {
			t.Fatalf("headers = %+v, want new-message subject only", headers)
		}
		return sendSnapshot{Message: messaging.Message{
			ID:              "sent/draft-1",
			ConnectionID:    draft.ConnectionID,
			ConversationID:  draft.ConversationID,
			LocalIdentityID: draft.LocalIdentityID,
			RemoteID:        "sent-1@example.com",
			Direction:       messaging.MessageDirectionOutbound,
			Sender: messaging.Participant{
				Kind:        messaging.ParticipantKindAccount,
				IdentityID:  draft.LocalIdentityID,
				Address:     cfg.EmailAddress,
				DisplayName: cfg.DisplayName,
				IsLocal:     true,
			},
			Parts:     cloneParts(draft.Parts),
			CreatedAt: time.Date(2026, 4, 25, 15, 0, 0, 0, time.UTC),
			Status:    messaging.MessageStatusSent,
		}}, nil
	}
	connectServerForTest(t, server)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodSendMessage, protocol.SendMessageParams{
		Draft: protocol.DraftRecord{Draft: messaging.Draft{
			ID:              "draft-1",
			ConnectionID:    "imap/work",
			ConversationID:  "conv/new",
			LocalIdentityID: "identity/imap/work",
			Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "I can review it."}},
			Status:          messaging.DraftStatusApproved,
			Metadata: map[string]string{
				"subject":    "Board Update",
				"to":         "latisha@example.com, board@example.com",
				"recipients": "latisha@example.com",
			},
		}},
	}))
	if resp.Error != nil {
		t.Fatalf("send message error = %v", resp.Error)
	}
	var sent protocol.SendResult
	if err := json.Unmarshal(resp.Result, &sent); err != nil {
		t.Fatalf("decode send result: %v", err)
	}
	if sent.Status != messaging.MessageStatusSent || sent.Message.Message.ID != "sent/draft-1" {
		t.Fatalf("send result = %+v, want sent/draft-1", sent)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodGetMessage, protocol.GetMessageParams{
		ConnectionID: "imap/work",
		MessageID:    "sent/draft-1",
	}))
	if resp.Error != nil {
		t.Fatalf("get sent message error = %v", resp.Error)
	}
}

func TestServerHandleReplyResolvesSenderAndThreadHeaders(t *testing.T) {
	t.Parallel()

	server := newServer()
	server.verifyFunc = func(context.Context, adapterConfig) error { return nil }
	server.sendFunc = func(_ context.Context, _ adapterConfig, draft messaging.Draft, recipients []string, headers outboundHeaders) (sendSnapshot, error) {
		if got := strings.Join(recipients, ","); got != "latisha@example.com" {
			t.Fatalf("reply recipients = %q, want inbound sender", got)
		}
		if headers.Subject != "Re: Board Update" || headers.InReplyTo != "reply@example.com" {
			t.Fatalf("reply headers = %+v, want subject and in-reply-to", headers)
		}
		if got := strings.Join(headers.References, ","); got != "root@example.com,reply@example.com" {
			t.Fatalf("references = %q, want stored thread refs", got)
		}
		return sendSnapshot{Message: messaging.Message{
			ID:              "sent/reply-1",
			ConnectionID:    draft.ConnectionID,
			ConversationID:  draft.ConversationID,
			LocalIdentityID: draft.LocalIdentityID,
			RemoteID:        "sent-reply@example.com",
			Direction:       messaging.MessageDirectionOutbound,
			Sender:          messaging.Participant{Kind: messaging.ParticipantKindAccount, IdentityID: draft.LocalIdentityID, Address: "mailer@example.com", IsLocal: true},
			Parts:           cloneParts(draft.Parts),
			CreatedAt:       time.Date(2026, 4, 25, 15, 5, 0, 0, time.UTC),
			ReplyToRemoteID: headers.InReplyTo,
			Status:          messaging.MessageStatusSent,
		}}, nil
	}
	connectServerForTest(t, server)
	seedInboundThread(t, server)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodReplyMessage, protocol.ReplyMessageParams{
		ReplyToMessageID: "msg/inbound",
		Draft: protocol.DraftRecord{Draft: messaging.Draft{
			ID:              "reply-1",
			ConnectionID:    "imap/work",
			ConversationID:  "conv/board",
			LocalIdentityID: "identity/imap/work",
			Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindMarkdown, Text: "I'll take care of it."}},
			Status:          messaging.DraftStatusApproved,
		}},
	}))
	if resp.Error != nil {
		t.Fatalf("reply message error = %v", resp.Error)
	}
	var sent protocol.SendResult
	if err := json.Unmarshal(resp.Result, &sent); err != nil {
		t.Fatalf("decode reply result: %v", err)
	}
	if sent.Message.Message.ReplyToRemoteID != "reply@example.com" || sent.Message.Message.Status != messaging.MessageStatusSent {
		t.Fatalf("reply result = %+v, want sent reply linked to inbound", sent.Message.Message)
	}
}

func TestBuildRFC822MessageIncludesThreadHeaders(t *testing.T) {
	t.Parallel()

	raw := string(buildRFC822Message(
		"Mailer <mailer@example.com>",
		[]string{"latisha@example.com", "board@example.com"},
		"Re: Board Update",
		"Line one\nLine two",
		"text/plain",
		"sent@example.com",
		outboundHeaders{
			InReplyTo:  "reply@example.com",
			References: []string{"root@example.com", "reply@example.com"},
		},
	))
	for _, want := range []string{
		"From: Mailer <mailer@example.com>\r\n",
		"To: latisha@example.com, board@example.com\r\n",
		"Subject: Re: Board Update\r\n",
		"Message-ID: <sent@example.com>\r\n",
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n",
		"In-Reply-To: <reply@example.com>\r\n",
		"References: <root@example.com> <reply@example.com>\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw message missing %q:\n%s", want, raw)
		}
	}
	if !strings.HasSuffix(raw, "\r\n\r\nLine one\r\nLine two\r\n") {
		t.Fatalf("raw body = %q, want normalized CRLF body", raw)
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

func seedInboundThread(t *testing.T, server *service) {
	t.Helper()

	state, ok := server.connection("imap/work")
	if !ok {
		t.Fatal("imap/work is not connected")
	}
	conversation := messaging.Conversation{
		ID:              "conv/board",
		ConnectionID:    "imap/work",
		LocalIdentityID: "identity/imap/work",
		Kind:            messaging.ConversationKindEmailThread,
		Title:           "Board Update",
		Participants: []messaging.Participant{
			{Kind: messaging.ParticipantKindAccount, IdentityID: "identity/imap/work", Address: "mailer@example.com", IsLocal: true},
			{Kind: messaging.ParticipantKindUser, Address: "latisha@example.com", DisplayName: "Latisha"},
		},
	}
	message := messaging.Message{
		ID:              "msg/inbound",
		ConnectionID:    "imap/work",
		ConversationID:  conversation.ID,
		LocalIdentityID: "identity/imap/work",
		RemoteID:        "reply@example.com",
		Direction:       messaging.MessageDirectionInbound,
		Sender:          messaging.Participant{Kind: messaging.ParticipantKindUser, Address: "latisha@example.com", DisplayName: "Latisha"},
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "Can you review this?"}},
		CreatedAt:       time.Date(2026, 4, 25, 14, 30, 0, 0, time.UTC),
		Status:          messaging.MessageStatusReceived,
		Metadata: map[string]string{
			"email_message_id": "reply@example.com",
			"references":       "root@example.com reply@example.com",
		},
	}
	server.mu.Lock()
	state.conversations[conversation.ID] = conversation
	state.messages[message.ID] = message
	server.mu.Unlock()
}

func crlf(value string) string {
	value = strings.TrimPrefix(value, "\n")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
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
