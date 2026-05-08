package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/messaging"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

func TestMessengerBridgeFilesMaterializesMessageRefs(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "telegram-voice.ogg")
	if err := os.WriteFile(sourcePath, []byte("voice bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	stateDir := t.TempDir()
	files := testMessengerBridgeFiles(stateDir)
	message := messaging.Message{
		ID:              "msg/1",
		ConnectionID:    "telegram/main",
		ConversationID:  "chat/1",
		LocalIdentityID: "identity/bot",
		Parts: []messaging.MessagePart{{
			Kind:        messaging.MessagePartKindFile,
			ContentType: "audio/ogg",
			FileName:    "voice note.ogg",
			Ref:         sourcePath,
			Metadata:    map[string]string{"telegram_media_type": "voice"},
		}},
	}

	got, err := files.MaterializeMessages(context.Background(), "agent/real", []messaging.Message{message})
	if err != nil {
		t.Fatalf("MaterializeMessages() error = %v", err)
	}
	wantRef := "/sandbox-state/messengers/inbox/telegram_main/chat_1/msg_1/01-voice_note.ogg"
	if got[0].Parts[0].Ref != wantRef {
		t.Fatalf("ref = %q, want %q", got[0].Parts[0].Ref, wantRef)
	}
	if message.Parts[0].Ref != sourcePath {
		t.Fatalf("original ref mutated to %q", message.Parts[0].Ref)
	}
	hostCopy := filepath.Join(stateDir, "messengers", "inbox", "telegram_main", "chat_1", "msg_1", "01-voice_note.ogg")
	raw, err := os.ReadFile(hostCopy)
	if err != nil {
		t.Fatalf("read host copy: %v", err)
	}
	if string(raw) != "voice bytes" {
		t.Fatalf("host copy = %q, want voice bytes", raw)
	}
	if got[0].Parts[0].Metadata["telegram_media_type"] != "voice" || got[0].Parts[0].Metadata["sky10_guest_path"] != wantRef {
		t.Fatalf("metadata = %+v, want preserved media type and guest path", got[0].Parts[0].Metadata)
	}
}

func TestMessengerBridgeFilesMapsDraftRefsToHostState(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	hostPath := filepath.Join(stateDir, "messengers", "outbox", "audio.ogg")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte("outbound"), 0o644); err != nil {
		t.Fatalf("write outbound: %v", err)
	}
	files := testMessengerBridgeFiles(stateDir)
	draft := messaging.Draft{
		ID:              "draft/1",
		ConnectionID:    "telegram/main",
		ConversationID:  "chat/1",
		LocalIdentityID: "identity/bot",
		Parts: []messaging.MessagePart{{
			Kind:     messaging.MessagePartKindFile,
			FileName: "audio.ogg",
			Ref:      "/sandbox-state/messengers/outbox/audio.ogg",
		}},
		Status: messaging.DraftStatusPending,
	}

	got, err := files.HostDraftRefs(context.Background(), "agent/real", draft)
	if err != nil {
		t.Fatalf("HostDraftRefs() error = %v", err)
	}
	if got.Parts[0].Ref != hostPath {
		t.Fatalf("draft ref = %q, want %q", got.Parts[0].Ref, hostPath)
	}
}

func TestHostPathForGuestRefRejectsEscapes(t *testing.T) {
	t.Parallel()

	if _, err := hostPathForGuestRef(t.TempDir(), "/sandbox-state/../shared/file.txt"); err == nil {
		t.Fatal("hostPathForGuestRef() error = nil, want escape rejection")
	}
}

func testMessengerBridgeFiles(stateDir string) *messengerBridgeFiles {
	source := &sandboxAgentSource{
		targetsBy: map[string]sandboxAgentTarget{
			"id:agent/real": {
				Agent: skyagent.AgentInfo{ID: "agent/real", Name: "hermes", KeyName: "hermes-dev"},
				Sandbox: skysandbox.Record{
					Name: "Hermes Dev",
					Slug: "hermes-dev",
				},
			},
		},
	}
	files := newMessengerBridgeFiles(source)
	files.stateDir = func(context.Context, sandboxAgentTarget) (string, error) {
		return stateDir, nil
	}
	return files
}
