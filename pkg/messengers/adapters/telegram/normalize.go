package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

type telegramMedia struct {
	Type         string
	FileID       string
	FileUniqueID string
	FileName     string
	ContentType  string
	SizeBytes    int64
	Duration     int
	Width        int
	Height       int
}

func identityFromBot(connectionID messaging.ConnectionID, user *models.User) messaging.Identity {
	remoteID := strconv.FormatInt(user.ID, 10)
	displayName := displayNameForUser(user)
	if displayName == "" {
		displayName = "Telegram Bot"
	}
	address := user.Username
	if address != "" {
		address = "@" + strings.TrimPrefix(address, "@")
	}
	return messaging.Identity{
		ID:           messaging.IdentityID("identity/" + string(connectionID) + "/bot/" + remoteID),
		ConnectionID: connectionID,
		Kind:         messaging.IdentityKindBot,
		RemoteID:     remoteID,
		Address:      firstNonEmpty(address, remoteID),
		DisplayName:  displayName,
		CanReceive:   true,
		CanSend:      true,
		IsDefault:    true,
		Metadata: cleanStringMap(map[string]string{
			"telegram_user_id":  remoteID,
			"telegram_username": user.Username,
		}),
	}
}

func connectionMetadata(cfg adapterConfig, me *models.User) map[string]string {
	return cleanStringMap(map[string]string{
		metaAPIBaseURL:           cfg.APIBaseURL,
		metaPollLimit:            strconv.Itoa(cfg.PollLimit),
		metaPollTimeoutSeconds:   strconv.Itoa(cfg.PollTimeoutSeconds),
		metaDownloadMedia:        strconv.FormatBool(cfg.DownloadMedia),
		metaMaxDownloadBytes:     strconv.FormatInt(cfg.MaxDownloadBytes, 10),
		metaDropPendingOnConnect: strconv.FormatBool(cfg.DropPendingOnConnect),
		"telegram_bot_id":        strconv.FormatInt(me.ID, 10),
		"telegram_bot_username":  me.Username,
		"telegram_bot_name":      displayNameForUser(me),
	})
}

func normalizeMessage(ctx context.Context, state *connectionState, message *models.Message, eventType messaging.EventType, now func() time.Time) (messaging.Conversation, protocol.MessageRecord, error) {
	conversation := conversationFromMessage(state, message)
	record := protocol.MessageRecord{
		Message: messaging.Message{
			ID:              messageIDFor(state.config.ConnectionID, strconv.FormatInt(message.Chat.ID, 10), strconv.Itoa(message.ID)),
			ConnectionID:    state.config.ConnectionID,
			ConversationID:  conversation.ID,
			LocalIdentityID: state.identity.ID,
			RemoteID:        strconv.Itoa(message.ID),
			Direction:       directionForMessage(state, message),
			Sender:          senderForMessage(state, message),
			CreatedAt:       telegramTime(message.Date, now),
			ReplyToRemoteID: replyRemoteID(message),
			Status:          statusForMessage(state, message),
			Metadata: cleanStringMap(map[string]string{
				"telegram_chat_id":           strconv.FormatInt(message.Chat.ID, 10),
				"telegram_chat_type":         string(message.Chat.Type),
				"telegram_message_id":        strconv.Itoa(message.ID),
				"telegram_message_thread_id": zeroOmitInt(message.MessageThreadID),
				"telegram_media_group_id":    message.MediaGroupID,
				"telegram_event_type":        string(eventType),
			}),
		},
	}
	if message.EditDate > 0 {
		edited := telegramTime(message.EditDate, now)
		record.Message.EditedAt = &edited
	}

	parts, attachments, err := messageParts(ctx, state, record.Message.ID, message)
	if err != nil {
		return messaging.Conversation{}, protocol.MessageRecord{}, err
	}
	record.Message.Parts = parts
	record.Attachments = attachments
	return conversation, record, nil
}

func conversationFromMessage(state *connectionState, message *models.Message) messaging.Conversation {
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	participants := make([]messaging.Participant, 0, 2)
	if message.From != nil {
		participants = append(participants, participantFromUser(message.From, false))
	}
	if message.Chat.Type == models.ChatTypePrivate {
		participants = append(participants, messaging.Participant{
			Kind:        messaging.ParticipantKindBot,
			RemoteID:    state.identity.RemoteID,
			Address:     state.identity.Address,
			DisplayName: state.identity.DisplayName,
			IdentityID:  state.identity.ID,
			IsLocal:     true,
		})
	}
	return messaging.Conversation{
		ID:              conversationIDFor(state.config.ConnectionID, chatID),
		ConnectionID:    state.config.ConnectionID,
		LocalIdentityID: state.identity.ID,
		Kind:            conversationKind(message.Chat.Type),
		RemoteID:        chatID,
		Title:           chatTitle(message.Chat),
		Participants:    participants,
		Metadata: cleanStringMap(map[string]string{
			"telegram_chat_id":       chatID,
			"telegram_chat_type":     string(message.Chat.Type),
			"telegram_chat_username": message.Chat.Username,
			"telegram_is_forum":      strconv.FormatBool(message.Chat.IsForum),
		}),
	}
}

func messageParts(ctx context.Context, state *connectionState, messageID messaging.MessageID, message *models.Message) ([]messaging.MessagePart, []protocol.Attachment, error) {
	parts := make([]messaging.MessagePart, 0, 2)
	attachments := make([]protocol.Attachment, 0)
	if strings.TrimSpace(message.Text) != "" {
		parts = append(parts, messaging.MessagePart{
			Kind: messaging.MessagePartKindText,
			Text: strings.TrimSpace(message.Text),
		})
	}
	if strings.TrimSpace(message.Caption) != "" {
		parts = append(parts, messaging.MessagePart{
			Kind: messaging.MessagePartKindText,
			Text: strings.TrimSpace(message.Caption),
			Metadata: map[string]string{
				"telegram_part": "caption",
			},
		})
	}
	for _, media := range mediaFromMessage(message) {
		part, attachment, err := mediaPart(ctx, state, messageID, media)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, part)
		if attachment != nil {
			attachments = append(attachments, *attachment)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, messaging.MessagePart{
			Kind: messaging.MessagePartKindText,
			Text: "Unsupported Telegram message",
			Metadata: map[string]string{
				"telegram_unsupported": "true",
			},
		})
	}
	return parts, attachments, nil
}

func mediaFromMessage(message *models.Message) []telegramMedia {
	items := make([]telegramMedia, 0, 2)
	if message.Voice != nil {
		voice := message.Voice
		items = append(items, telegramMedia{
			Type:         "voice",
			FileID:       voice.FileID,
			FileUniqueID: voice.FileUniqueID,
			FileName:     "telegram-voice-" + strconv.Itoa(message.ID) + ".ogg",
			ContentType:  firstNonEmpty(voice.MimeType, "audio/ogg"),
			SizeBytes:    voice.FileSize,
			Duration:     voice.Duration,
		})
	}
	if message.Audio != nil {
		audio := message.Audio
		items = append(items, telegramMedia{
			Type:         "audio",
			FileID:       audio.FileID,
			FileUniqueID: audio.FileUniqueID,
			FileName:     firstNonEmpty(audio.FileName, audio.Title, "telegram-audio-"+strconv.Itoa(message.ID)),
			ContentType:  audio.MimeType,
			SizeBytes:    audio.FileSize,
			Duration:     audio.Duration,
		})
	}
	if message.Document != nil {
		document := message.Document
		items = append(items, telegramMedia{
			Type:         "document",
			FileID:       document.FileID,
			FileUniqueID: document.FileUniqueID,
			FileName:     firstNonEmpty(document.FileName, "telegram-document-"+strconv.Itoa(message.ID)),
			ContentType:  document.MimeType,
			SizeBytes:    document.FileSize,
		})
	}
	if len(message.Photo) > 0 {
		photo := largestPhoto(message.Photo)
		items = append(items, telegramMedia{
			Type:         "photo",
			FileID:       photo.FileID,
			FileUniqueID: photo.FileUniqueID,
			FileName:     "telegram-photo-" + strconv.Itoa(message.ID) + ".jpg",
			ContentType:  "image/jpeg",
			SizeBytes:    int64(photo.FileSize),
			Width:        photo.Width,
			Height:       photo.Height,
		})
	}
	if message.Video != nil {
		video := message.Video
		items = append(items, telegramMedia{
			Type:         "video",
			FileID:       video.FileID,
			FileUniqueID: video.FileUniqueID,
			FileName:     firstNonEmpty(video.FileName, "telegram-video-"+strconv.Itoa(message.ID)+".mp4"),
			ContentType:  video.MimeType,
			SizeBytes:    video.FileSize,
			Duration:     video.Duration,
			Width:        video.Width,
			Height:       video.Height,
		})
	}
	if message.VideoNote != nil {
		note := message.VideoNote
		items = append(items, telegramMedia{
			Type:         "video_note",
			FileID:       note.FileID,
			FileUniqueID: note.FileUniqueID,
			FileName:     "telegram-video-note-" + strconv.Itoa(message.ID) + ".mp4",
			ContentType:  "video/mp4",
			SizeBytes:    int64(note.FileSize),
			Duration:     note.Duration,
			Width:        note.Length,
			Height:       note.Length,
		})
	}
	return items
}

func mediaPart(ctx context.Context, state *connectionState, messageID messaging.MessageID, media telegramMedia) (messaging.MessagePart, *protocol.Attachment, error) {
	part := messaging.MessagePart{
		Kind:        partKindForMedia(media),
		ContentType: media.ContentType,
		FileName:    media.FileName,
		SizeBytes:   media.SizeBytes,
		Metadata: cleanStringMap(map[string]string{
			"telegram_media_type":     media.Type,
			"telegram_file_id":        media.FileID,
			"telegram_file_unique_id": media.FileUniqueID,
			"telegram_duration":       zeroOmitInt(media.Duration),
			"telegram_width":          zeroOmitInt(media.Width),
			"telegram_height":         zeroOmitInt(media.Height),
		}),
	}
	if !state.config.DownloadMedia || media.FileID == "" {
		return part, nil, nil
	}
	if state.config.MaxDownloadBytes > 0 && media.SizeBytes > state.config.MaxDownloadBytes {
		part.Metadata["download_skipped"] = "max_download_bytes"
		return part, nil, nil
	}
	file, err := state.client.GetFile(ctx, &tgbot.GetFileParams{FileID: media.FileID})
	if err != nil {
		return messaging.MessagePart{}, nil, fmt.Errorf("get telegram file %s: %w", media.FileUniqueID, err)
	}
	if file.FilePath == "" {
		part.Metadata["download_skipped"] = "missing_file_path"
		return part, nil, nil
	}
	if state.config.MaxDownloadBytes > 0 && file.FileSize > state.config.MaxDownloadBytes {
		part.Metadata["download_skipped"] = "max_download_bytes"
		part.Metadata["telegram_get_file_size"] = strconv.FormatInt(file.FileSize, 10)
		return part, nil, nil
	}
	fileName := safeFileName(string(messageID) + "-" + firstNonEmpty(media.FileUniqueID, media.FileID, media.FileName))
	if ext := filepath.Ext(media.FileName); ext != "" && filepath.Ext(fileName) == "" {
		fileName += ext
	}
	blob, err := state.client.DownloadFile(ctx, state.client.FileDownloadLink(file), filepath.Join(state.config.Paths.BlobDir, fileName), state.config.MaxDownloadBytes)
	if err != nil {
		return messaging.MessagePart{}, nil, err
	}
	if media.ContentType != "" {
		blob.ContentType = media.ContentType
	}
	if media.SizeBytes == 0 {
		part.SizeBytes = blob.SizeBytes
	}
	part.Ref = blob.LocalPath
	part.Metadata["sha256"] = blob.SHA256
	part.Metadata["telegram_file_path"] = file.FilePath
	attachment := &protocol.Attachment{
		Name:        media.FileName,
		ContentType: media.ContentType,
		SizeBytes:   blob.SizeBytes,
		SHA256:      blob.SHA256,
		Blob:        blob,
		Metadata:    part.Metadata,
	}
	return part, attachment, nil
}

func partKindForMedia(media telegramMedia) messaging.MessagePartKind {
	if strings.HasPrefix(media.ContentType, "image/") || media.Type == "photo" {
		return messaging.MessagePartKindImage
	}
	return messaging.MessagePartKindFile
}

func largestPhoto(items []models.PhotoSize) models.PhotoSize {
	best := items[0]
	for _, item := range items[1:] {
		if item.FileSize > best.FileSize || item.Width*item.Height > best.Width*best.Height {
			best = item
		}
	}
	return best
}

func directionForMessage(state *connectionState, message *models.Message) messaging.MessageDirection {
	if message.From != nil && strconv.FormatInt(message.From.ID, 10) == state.identity.RemoteID {
		return messaging.MessageDirectionOutbound
	}
	return messaging.MessageDirectionInbound
}

func statusForMessage(state *connectionState, message *models.Message) messaging.MessageStatus {
	if directionForMessage(state, message) == messaging.MessageDirectionOutbound {
		return messaging.MessageStatusSent
	}
	return messaging.MessageStatusReceived
}

func senderForMessage(state *connectionState, message *models.Message) messaging.Participant {
	if message.From != nil {
		isLocal := strconv.FormatInt(message.From.ID, 10) == state.identity.RemoteID
		participant := participantFromUser(message.From, isLocal)
		if isLocal {
			participant.IdentityID = state.identity.ID
			participant.Address = state.identity.Address
		}
		return participant
	}
	if message.SenderChat != nil {
		return messaging.Participant{
			Kind:        messaging.ParticipantKindAccount,
			RemoteID:    strconv.FormatInt(message.SenderChat.ID, 10),
			Address:     usernameAddress(message.SenderChat.Username),
			DisplayName: chatTitle(*message.SenderChat),
		}
	}
	return messaging.Participant{
		Kind:        messaging.ParticipantKindAccount,
		RemoteID:    strconv.FormatInt(message.Chat.ID, 10),
		Address:     usernameAddress(message.Chat.Username),
		DisplayName: chatTitle(message.Chat),
	}
}

func participantFromUser(user *models.User, isLocal bool) messaging.Participant {
	return messaging.Participant{
		Kind:        participantKindForUser(user),
		RemoteID:    strconv.FormatInt(user.ID, 10),
		Address:     usernameAddress(user.Username),
		DisplayName: displayNameForUser(user),
		IsLocal:     isLocal,
		Metadata: cleanStringMap(map[string]string{
			"telegram_user_id":  strconv.FormatInt(user.ID, 10),
			"telegram_username": user.Username,
			"telegram_is_bot":   strconv.FormatBool(user.IsBot),
		}),
	}
}

func participantKindForUser(user *models.User) messaging.ParticipantKind {
	if user.IsBot {
		return messaging.ParticipantKindBot
	}
	return messaging.ParticipantKindUser
}

func conversationKind(chatType models.ChatType) messaging.ConversationKind {
	switch chatType {
	case models.ChatTypePrivate:
		return messaging.ConversationKindDirect
	case models.ChatTypeChannel:
		return messaging.ConversationKindChannel
	default:
		return messaging.ConversationKindGroup
	}
}

func displayNameForUser(user *models.User) string {
	if user == nil {
		return ""
	}
	return strings.TrimSpace(strings.Join(nonEmptyStrings(user.FirstName, user.LastName), " "))
}

func chatTitle(chat models.Chat) string {
	return firstNonEmpty(chat.Title, strings.TrimSpace(strings.Join(nonEmptyStrings(chat.FirstName, chat.LastName), " ")), usernameAddress(chat.Username), strconv.FormatInt(chat.ID, 10))
}

func usernameAddress(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return "@" + strings.TrimPrefix(username, "@")
}

func telegramTime(seconds int, now func() time.Time) time.Time {
	if seconds <= 0 {
		return now()
	}
	return time.Unix(int64(seconds), 0).UTC()
}

func replyRemoteID(message *models.Message) string {
	if message.ReplyToMessage == nil {
		return ""
	}
	return strconv.Itoa(message.ReplyToMessage.ID)
}

func conversationIDFor(connectionID messaging.ConnectionID, chatID string) messaging.ConversationID {
	return messaging.ConversationID("conversation/" + string(connectionID) + "/" + chatID)
}

func messageIDFor(connectionID messaging.ConnectionID, chatID, messageID string) messaging.MessageID {
	return messaging.MessageID("message/" + string(connectionID) + "/" + chatID + "/" + messageID)
}

func remoteChatIDFromConversationID(conversationID messaging.ConversationID) string {
	value := string(conversationID)
	idx := strings.LastIndex(value, "/")
	if idx < 0 {
		return value
	}
	return value[idx+1:]
}

func chatIDForSend(remoteID string) any {
	if id, err := strconv.ParseInt(remoteID, 10, 64); err == nil {
		return id
	}
	return remoteID
}

func draftText(draft messaging.Draft) string {
	parts := make([]string, 0, len(draft.Parts))
	for _, part := range draft.Parts {
		switch part.Kind {
		case messaging.MessagePartKindText, messaging.MessagePartKindMarkdown, messaging.MessagePartKindHTML:
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, strings.TrimSpace(part.Text))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "telegram-file"
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	if len(encoded) > 180 {
		encoded = encoded[:180]
	}
	return encoded
}

func cleanStringMap(values map[string]string) map[string]string {
	cleaned := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) != "" {
			cleaned[key] = value
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func zeroOmitInt(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}
