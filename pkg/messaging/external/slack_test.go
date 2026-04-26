package external

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

func TestSlackAdapterBundleManifest(t *testing.T) {
	t.Parallel()

	manifest, err := LoadManifest(slackManifestPath(t))
	if err != nil {
		t.Fatalf("LoadManifest(slack) error = %v", err)
	}
	adapter := manifest.Adapter()
	if adapter.ID != "slack" {
		t.Fatalf("adapter id = %q, want slack", adapter.ID)
	}
	if adapter.DisplayName != "Slack" {
		t.Fatalf("adapter display name = %q, want Slack", adapter.DisplayName)
	}
	if !adapter.Capabilities.SendMessages || !adapter.Capabilities.SearchConversations || !adapter.Capabilities.SearchMessages {
		t.Fatalf("adapter capabilities = %+v, want send/search support", adapter.Capabilities)
	}
}

func TestSlackAdapterBundleAgainstFakeWebAPI(t *testing.T) {
	bunPath := os.Getenv("SKY10_TEST_BUN")
	if bunPath == "" {
		var err error
		bunPath, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("bun not found on PATH; set SKY10_TEST_BUN to run the Slack adapter fixture")
		}
	}

	server := newFakeSlackServer(t)
	defer server.Close()

	spec, _, err := ResolveProcessSpec(slackManifestPath(t), ResolveOptions{BunPath: bunPath})
	if err != nil {
		t.Fatalf("ResolveProcessSpec(slack) error = %v", err)
	}
	client, err := messagingruntime.StartAdapter(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("StartAdapter(slack) error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	describe, err := client.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe() error = %v; stderr=%s", err, client.Stderr())
	}
	if describe.Adapter.ID != "slack" {
		t.Fatalf("describe adapter id = %q, want slack", describe.Adapter.ID)
	}

	connection := slackTestConnection(server.URL)
	credential := slackTestCredential(t)
	validate, err := client.ValidateConfig(ctx, protocol.ValidateConfigParams{
		Connection: connection,
		Credential: credential,
	})
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v; stderr=%s", err, client.Stderr())
	}
	if len(validate.Issues) != 0 {
		t.Fatalf("ValidateConfig() issues = %+v, want none", validate.Issues)
	}

	connect, err := client.Connect(ctx, protocol.ConnectParams{
		Connection: connection,
		Credential: credential,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v; stderr=%s", err, client.Stderr())
	}
	if connect.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("connect status = %q, want connected", connect.Status)
	}
	if len(connect.Identities) != 1 || connect.Identities[0].RemoteID != "U_BOT" {
		t.Fatalf("connect identities = %+v, want bot identity", connect.Identities)
	}

	identitySearch, err := client.SearchIdentities(ctx, protocol.SearchIdentitiesParams{
		ConnectionID: connection.ID,
		Query:        "latisha",
	})
	if err != nil {
		t.Fatalf("SearchIdentities() error = %v", err)
	}
	if len(identitySearch.Hits) != 1 || identitySearch.Hits[0].Participant.RemoteID != "U_LATISHA" {
		t.Fatalf("identity search hits = %+v, want Latisha", identitySearch.Hits)
	}

	conversationSearch, err := client.SearchConversations(ctx, protocol.SearchConversationsParams{
		ConnectionID: connection.ID,
		Query:        "board",
		Source:       protocol.SearchSourceRemote,
	})
	if err != nil {
		t.Fatalf("SearchConversations() error = %v", err)
	}
	if len(conversationSearch.Hits) != 1 || conversationSearch.Hits[0].Conversation.RemoteID != "CBOARD" {
		t.Fatalf("conversation search hits = %+v, want CBOARD", conversationSearch.Hits)
	}

	messages, err := client.ListMessages(ctx, protocol.ListMessagesParams{
		ConnectionID:   connection.ID,
		ConversationID: "slack/work/conversation/CBOARD",
		PageRequest:    protocol.PageRequest{Limit: 5},
	})
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].Message.RemoteID != "1714075200.000100" {
		t.Fatalf("messages = %+v, want Slack message", messages.Messages)
	}

	searchMessages, err := client.SearchMessages(ctx, protocol.SearchMessagesParams{
		ConnectionID: connection.ID,
		Query:        "update",
		Source:       protocol.SearchSourceRemote,
	})
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(searchMessages.Hits) != 1 || searchMessages.Hits[0].Message.Message.RemoteID != "1714075200.000100" {
		t.Fatalf("search messages = %+v, want update hit", searchMessages.Hits)
	}

	send, err := client.ReplyMessage(ctx, protocol.ReplyMessageParams{
		Draft: protocol.DraftRecord{
			Draft: messaging.Draft{
				ID:              "draft/1",
				ConnectionID:    connection.ID,
				ConversationID:  "slack/work/conversation/CBOARD",
				LocalIdentityID: connect.Identities[0].ID,
				ReplyToRemoteID: "1714075200.000100",
				Parts: []messaging.MessagePart{{
					Kind:        messaging.MessagePartKindText,
					ContentType: "text/plain",
					Text:        "Approved reply",
				}},
				Status: messaging.DraftStatusApproved,
			},
		},
		ReplyToRemoteID: "1714075200.000100",
	})
	if err != nil {
		t.Fatalf("ReplyMessage() error = %v", err)
	}
	if send.Status != messaging.MessageStatusSent || send.Message.Message.RemoteID != "1714075300.000200" {
		t.Fatalf("send result = %+v, want sent Slack reply", send)
	}
	if send.Message.Message.ReplyToRemoteID != "1714075200.000100" {
		t.Fatalf("send reply_to_remote_id = %q, want thread ts", send.Message.Message.ReplyToRemoteID)
	}
}

func slackManifestPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "external", "messengers", "slack", "adapter.json"))
}

func slackTestConnection(apiBaseURL string) messaging.Connection {
	return messaging.Connection{
		ID:        "slack/work",
		AdapterID: "slack",
		Label:     "Slack Work",
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBotToken,
			CredentialRef: "secret://slack/work",
		},
		Metadata: map[string]string{
			"slack_api_base_url":       apiBaseURL,
			"slack_conversation_types": "public_channel,private_channel,im,mpim",
			"slack_history_limit":      "15",
		},
	}
}

func slackTestCredential(t *testing.T) *protocol.ResolvedCredential {
	t.Helper()

	path := filepath.Join(t.TempDir(), "slack-credential.json")
	raw := []byte(`{"bot_token":"xoxb-test-token","team_id":"T123"}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	return &protocol.ResolvedCredential{
		Ref:        "secret://slack/work",
		AuthMethod: messaging.AuthMethodBotToken,
		Blob: protocol.BlobRef{
			LocalPath: path,
		},
	}
}

func newFakeSlackServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer xoxb-test-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		switch r.URL.Path {
		case "/auth.test":
			writeSlackJSON(t, w, map[string]interface{}{
				"ok":      true,
				"url":     "https://test.slack.com/",
				"team":    "Test Workspace",
				"team_id": "T123",
				"user":    "sky10-bot",
				"user_id": "U_BOT",
				"bot_id":  "B123",
			})
		case "/users.list":
			writeSlackJSON(t, w, map[string]interface{}{
				"ok": true,
				"members": []map[string]interface{}{
					{
						"id":        "U_LATISHA",
						"name":      "latisha",
						"real_name": "Latisha Jones",
						"profile": map[string]string{
							"display_name": "Latisha",
							"email":        "latisha@example.com",
						},
					},
				},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		case "/conversations.list":
			if !strings.Contains(r.URL.Query().Get("types"), "private_channel") {
				t.Fatalf("conversation types = %q, want private_channel", r.URL.Query().Get("types"))
			}
			writeSlackJSON(t, w, map[string]interface{}{
				"ok": true,
				"channels": []map[string]interface{}{
					{
						"id":              "CBOARD",
						"name":            "board",
						"name_normalized": "board",
						"is_channel":      true,
						"is_member":       true,
						"num_members":     3,
					},
					{
						"id":    "D1",
						"is_im": true,
						"user":  "U_LATISHA",
					},
				},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		case "/conversations.history":
			if got := r.URL.Query().Get("channel"); got != "CBOARD" {
				t.Fatalf("history channel = %q, want CBOARD", got)
			}
			writeSlackJSON(t, w, map[string]interface{}{
				"ok": true,
				"messages": []map[string]interface{}{
					{
						"type":      "message",
						"user":      "U_LATISHA",
						"text":      "Need an update for the board packet.",
						"ts":        "1714075200.000100",
						"thread_ts": "1714075200.000100",
					},
				},
				"response_metadata": map[string]string{"next_cursor": ""},
			})
		case "/search.messages":
			writeSlackJSON(t, w, map[string]interface{}{
				"ok": true,
				"messages": map[string]interface{}{
					"matches": []map[string]interface{}{
						{
							"channel": map[string]string{
								"id":   "CBOARD",
								"name": "board",
							},
							"user":      "U_LATISHA",
							"text":      "Need an update for the board packet.",
							"ts":        "1714075200.000100",
							"permalink": "https://test.slack.com/archives/CBOARD/p1714075200000100",
						},
					},
					"pagination": map[string]int{
						"page":       1,
						"page_count": 1,
					},
				},
			})
		case "/chat.postMessage":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat.postMessage body: %v", err)
			}
			if body["channel"] != "CBOARD" {
				t.Fatalf("chat.postMessage channel = %v, want CBOARD", body["channel"])
			}
			if body["thread_ts"] != "1714075200.000100" {
				t.Fatalf("chat.postMessage thread_ts = %v, want thread", body["thread_ts"])
			}
			if body["text"] != "Approved reply" {
				t.Fatalf("chat.postMessage text = %v, want Approved reply", body["text"])
			}
			writeSlackJSON(t, w, map[string]interface{}{
				"ok":      true,
				"channel": "CBOARD",
				"ts":      "1714075300.000200",
				"message": map[string]interface{}{
					"type":      "message",
					"text":      "Approved reply",
					"ts":        "1714075300.000200",
					"thread_ts": "1714075200.000100",
				},
			})
		default:
			t.Fatalf("unexpected Slack API path %s", r.URL.Path)
		}
	}))
}

func writeSlackJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write Slack JSON: %v", err)
	}
}
