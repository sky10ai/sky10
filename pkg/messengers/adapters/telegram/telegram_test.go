package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

func TestParseConfigAppliesTelegramDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig(messaging.Connection{
		ID:        "telegram/default",
		AdapterID: "telegram",
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBotToken,
			CredentialRef: "secret://telegram/default",
		},
	}, protocol.RuntimePaths{}, stagedCredential(t, `{"bot_token":"123:abc"}`))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.APIBaseURL != defaultAPIBaseURL {
		t.Fatalf("APIBaseURL = %q, want %q", cfg.APIBaseURL, defaultAPIBaseURL)
	}
	if cfg.PollLimit != 100 {
		t.Fatalf("PollLimit = %d, want 100", cfg.PollLimit)
	}
	if !cfg.DownloadMedia {
		t.Fatal("DownloadMedia = false, want true")
	}
	if cfg.MaxDownloadBytes != defaultMaxDownloadBytes {
		t.Fatalf("MaxDownloadBytes = %d, want %d", cfg.MaxDownloadBytes, defaultMaxDownloadBytes)
	}
}

func TestServerConnectDeletesWebhookAndListsIdentity(t *testing.T) {
	t.Parallel()

	fake := newFakeTelegramAPI()
	server := newServer()
	server.clientFactory = func(adapterConfig) (telegramAPI, error) { return fake, nil }

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodConnect, protocol.ConnectParams{
		Connection: telegramConnection(map[string]string{
			metaAPIBaseURL:           "https://telegram.example.test/",
			metaDropPendingOnConnect: "true",
		}),
		Paths:      protocol.RuntimePaths{BlobDir: t.TempDir()},
		Credential: stagedCredential(t, `{"bot_token":"123:abc"}`),
	}))
	if resp.Error != nil {
		t.Fatalf("connect error = %v", resp.Error)
	}
	if !fake.deletedWebhook || !fake.dropPending {
		t.Fatalf("DeleteWebhook called = %v drop = %v, want true true", fake.deletedWebhook, fake.dropPending)
	}
	var connected protocol.ConnectResult
	if err := json.Unmarshal(resp.Result, &connected); err != nil {
		t.Fatalf("decode connect result: %v", err)
	}
	if connected.Status != messaging.ConnectionStatusConnected {
		t.Fatalf("Status = %q, want connected", connected.Status)
	}
	if len(connected.Identities) != 1 || connected.Identities[0].Address != "@sky10_bot" {
		t.Fatalf("Identities = %+v, want @sky10_bot", connected.Identities)
	}
	if connected.Metadata[metaAPIBaseURL] != "https://telegram.example.test" {
		t.Fatalf("metadata api url = %q", connected.Metadata[metaAPIBaseURL])
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodListIdentities, protocol.ListIdentitiesParams{
		ConnectionID: "telegram/default",
	}))
	if resp.Error != nil {
		t.Fatalf("list identities error = %v", resp.Error)
	}
	var listed protocol.ListIdentitiesResult
	if err := json.Unmarshal(resp.Result, &listed); err != nil {
		t.Fatalf("decode identities: %v", err)
	}
	if len(listed.Identities) != 1 || listed.Identities[0].ID != connected.Identities[0].ID {
		t.Fatalf("listed identities = %+v, want connected identity", listed.Identities)
	}
}

func TestServerPollDownloadsVoiceNote(t *testing.T) {
	t.Parallel()

	fake := newFakeTelegramAPI()
	fake.updates = []models.Update{{
		ID: 42,
		Message: &models.Message{
			ID:   7,
			Date: int(time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC).Unix()),
			Chat: models.Chat{
				ID:        9001,
				Type:      models.ChatTypePrivate,
				FirstName: "Ada",
			},
			From: &models.User{
				ID:        111,
				FirstName: "Ada",
				Username:  "ada",
			},
			Voice: &models.Voice{
				FileID:       "voice-file",
				FileUniqueID: "voice-unique",
				Duration:     3,
				MimeType:     "audio/ogg",
				FileSize:     int64(len(fake.fileBytes)),
			},
			Caption: "please transcribe",
		},
	}}
	fake.files["voice-file"] = &models.File{
		FileID:       "voice-file",
		FileUniqueID: "voice-unique",
		FileSize:     int64(len(fake.fileBytes)),
		FilePath:     "voice/file.ogg",
	}

	server := newServer()
	server.clientFactory = func(adapterConfig) (telegramAPI, error) { return fake, nil }
	blobDir := t.TempDir()
	connectTelegramServer(t, server, fake, blobDir)

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodPoll, protocol.PollParams{
		ConnectionID: "telegram/default",
		Limit:        10,
	}))
	if resp.Error != nil {
		t.Fatalf("poll error = %v", resp.Error)
	}
	var poll protocol.PollResult
	if err := json.Unmarshal(resp.Result, &poll); err != nil {
		t.Fatalf("decode poll: %v", err)
	}
	if poll.Checkpoint == nil || poll.Checkpoint.Cursor != "43" {
		t.Fatalf("checkpoint = %+v, want cursor 43", poll.Checkpoint)
	}
	if len(poll.Events) != 1 || poll.Events[0].Type != messaging.EventTypeMessageReceived {
		t.Fatalf("events = %+v, want one received event", poll.Events)
	}

	resp = server.handle(context.Background(), rpcRequest(t, protocol.MethodGetMessage, protocol.GetMessageParams{
		ConnectionID:   "telegram/default",
		ConversationID: conversationIDFor("telegram/default", "9001"),
		MessageID:      messageIDFor("telegram/default", "9001", "7"),
	}))
	if resp.Error != nil {
		t.Fatalf("get message error = %v", resp.Error)
	}
	var got protocol.GetMessageResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if got.Message.Message.Sender.Address != "@ada" {
		t.Fatalf("sender = %+v, want @ada", got.Message.Message.Sender)
	}
	if len(got.Message.Message.Parts) != 2 {
		t.Fatalf("parts = %+v, want caption and voice", got.Message.Message.Parts)
	}
	voice := got.Message.Message.Parts[1]
	if voice.Kind != messaging.MessagePartKindFile || voice.ContentType != "audio/ogg" || voice.Ref == "" {
		t.Fatalf("voice part = %+v, want downloaded audio file", voice)
	}
	if voice.Metadata["telegram_media_type"] != "voice" || voice.Metadata["telegram_duration"] != "3" {
		t.Fatalf("voice metadata = %+v", voice.Metadata)
	}
	if !filepath.IsAbs(voice.Ref) {
		t.Fatalf("voice ref = %q, want absolute blob path", voice.Ref)
	}
	raw, err := os.ReadFile(voice.Ref)
	if err != nil {
		t.Fatalf("read downloaded voice: %v", err)
	}
	if string(raw) != string(fake.fileBytes) {
		t.Fatalf("downloaded voice = %q, want %q", raw, fake.fileBytes)
	}
	if len(got.Message.Attachments) != 1 || got.Message.Attachments[0].Blob.LocalPath != voice.Ref {
		t.Fatalf("attachments = %+v, want downloaded voice attachment", got.Message.Attachments)
	}
	if len(fake.downloads) != 1 || fake.downloads[0].maxBytes != defaultMaxDownloadBytes {
		t.Fatalf("downloads = %+v, want one default-limited download", fake.downloads)
	}
}

func TestServerSendTextMessage(t *testing.T) {
	t.Parallel()

	fake := newFakeTelegramAPI()
	server := newServer()
	server.clientFactory = func(adapterConfig) (telegramAPI, error) { return fake, nil }
	connectTelegramServer(t, server, fake, t.TempDir())

	state, ok := server.connection("telegram/default")
	if !ok {
		t.Fatal("connection missing after connect")
	}
	conversation := messaging.Conversation{
		ID:              conversationIDFor("telegram/default", "9001"),
		ConnectionID:    "telegram/default",
		LocalIdentityID: state.identity.ID,
		Kind:            messaging.ConversationKindDirect,
		RemoteID:        "9001",
		Title:           "Ada",
	}
	state.conversations[conversation.ID] = conversation
	fake.sentMessage = &models.Message{
		ID:   8,
		Date: int(time.Date(2026, 5, 7, 12, 1, 0, 0, time.UTC).Unix()),
		Chat: models.Chat{ID: 9001, Type: models.ChatTypePrivate, FirstName: "Ada"},
		From: fake.me,
		Text: "hello from sky10",
	}

	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodSendMessage, protocol.SendMessageParams{
		Draft: protocol.DraftRecord{Draft: messaging.Draft{
			ID:              "draft/1",
			ConnectionID:    "telegram/default",
			ConversationID:  conversation.ID,
			LocalIdentityID: state.identity.ID,
			Parts: []messaging.MessagePart{{
				Kind: messaging.MessagePartKindText,
				Text: "hello from sky10",
			}},
		}},
	}))
	if resp.Error != nil {
		t.Fatalf("send error = %v", resp.Error)
	}
	if len(fake.sent) != 1 || fake.sent[0].Text != "hello from sky10" || fake.sent[0].ChatID.(int64) != 9001 {
		t.Fatalf("sent params = %+v, want chat 9001 text", fake.sent)
	}
	var result protocol.SendResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode send result: %v", err)
	}
	if result.Status != messaging.MessageStatusSent || result.Message.Message.Direction != messaging.MessageDirectionOutbound {
		t.Fatalf("send result = %+v, want outbound sent", result)
	}
}

type fakeTelegramAPI struct {
	me             *models.User
	files          map[string]*models.File
	updates        []models.Update
	fileBytes      []byte
	deletedWebhook bool
	dropPending    bool
	sent           []tgbot.SendMessageParams
	sentMessage    *models.Message
	downloads      []fakeDownload
}

type fakeDownload struct {
	url      string
	destPath string
	maxBytes int64
}

func newFakeTelegramAPI() *fakeTelegramAPI {
	return &fakeTelegramAPI{
		me: &models.User{
			ID:        1000,
			IsBot:     true,
			FirstName: "Sky10",
			Username:  "sky10_bot",
		},
		files:     make(map[string]*models.File),
		fileBytes: []byte("voice bytes"),
	}
}

func (f *fakeTelegramAPI) GetMe(context.Context) (*models.User, error) {
	return f.me, nil
}

func (f *fakeTelegramAPI) DeleteWebhook(_ context.Context, params *tgbot.DeleteWebhookParams) (bool, error) {
	f.deletedWebhook = true
	if params != nil {
		f.dropPending = params.DropPendingUpdates
	}
	return true, nil
}

func (f *fakeTelegramAPI) GetUpdates(context.Context, getUpdatesRequest) ([]models.Update, error) {
	return append([]models.Update(nil), f.updates...), nil
}

func (f *fakeTelegramAPI) GetFile(_ context.Context, params *tgbot.GetFileParams) (*models.File, error) {
	file, ok := f.files[params.FileID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return file, nil
}

func (f *fakeTelegramAPI) FileDownloadLink(file *models.File) string {
	return "https://telegram.example.test/file/" + file.FilePath
}

func (f *fakeTelegramAPI) SendMessage(_ context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	f.sent = append(f.sent, *params)
	if f.sentMessage != nil {
		return f.sentMessage, nil
	}
	return &models.Message{
		ID:   99,
		Date: int(time.Now().Unix()),
		Chat: models.Chat{ID: params.ChatID.(int64), Type: models.ChatTypePrivate},
		From: f.me,
		Text: params.Text,
	}, nil
}

func (f *fakeTelegramAPI) DownloadFile(_ context.Context, url, destPath string, maxBytes int64) (protocol.BlobRef, error) {
	f.downloads = append(f.downloads, fakeDownload{url: url, destPath: destPath, maxBytes: maxBytes})
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return protocol.BlobRef{}, err
	}
	if err := os.WriteFile(destPath, f.fileBytes, 0o644); err != nil {
		return protocol.BlobRef{}, err
	}
	sum := sha256.Sum256(f.fileBytes)
	return protocol.BlobRef{
		ID:        "telegram:" + filepath.Base(destPath),
		LocalPath: destPath,
		SizeBytes: int64(len(f.fileBytes)),
		SHA256:    hex.EncodeToString(sum[:]),
	}, nil
}

func telegramConnection(metadata map[string]string) messaging.Connection {
	return messaging.Connection{
		ID:        "telegram/default",
		AdapterID: "telegram",
		Label:     "Telegram",
		Auth: messaging.AuthInfo{
			Method:        messaging.AuthMethodBotToken,
			CredentialRef: "secret://telegram/default",
		},
		Metadata: metadata,
	}
}

func connectTelegramServer(t *testing.T, server *service, fake *fakeTelegramAPI, blobDir string) {
	t.Helper()
	server.clientFactory = func(adapterConfig) (telegramAPI, error) { return fake, nil }
	resp := server.handle(context.Background(), rpcRequest(t, protocol.MethodConnect, protocol.ConnectParams{
		Connection: telegramConnection(nil),
		Paths:      protocol.RuntimePaths{BlobDir: blobDir},
		Credential: stagedCredential(t, `{"bot_token":"123:abc"}`),
	}))
	if resp.Error != nil {
		t.Fatalf("connect error = %v", resp.Error)
	}
}

func stagedCredential(t *testing.T, raw string) *protocol.ResolvedCredential {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	return &protocol.ResolvedCredential{
		Ref:        "secret://telegram/default",
		AuthMethod: messaging.AuthMethodBotToken,
		Blob: protocol.BlobRef{
			LocalPath: path,
		},
	}
}

func rpcRequest(t *testing.T, method protocol.Method, params any) messagingruntime.Request {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return messagingruntime.Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  string(method),
		Params:  raw,
	}
}
