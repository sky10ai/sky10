package imapsmtp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

const (
	defaultIMAPSearchLimit = 25
	maxIMAPSearchLimit     = 100
)

func verifyMailboxAccess(ctx context.Context, cfg adapterConfig) error {
	client, err := dialAndLoginIMAP(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeIMAP(client)
	if _, err := client.Select(cfg.Mailbox, nil).Wait(); err != nil {
		return fmt.Errorf("select mailbox %q: %w", cfg.Mailbox, err)
	}
	return nil
}

func pollMailbox(ctx context.Context, cfg adapterConfig, checkpoint *protocol.Checkpoint, limit int) (pollSnapshot, error) {
	client, err := dialAndLoginIMAP(ctx, cfg)
	if err != nil {
		return pollSnapshot{}, err
	}
	defer closeIMAP(client)

	if _, err := client.Select(cfg.Mailbox, nil).Wait(); err != nil {
		return pollSnapshot{}, fmt.Errorf("select mailbox %q: %w", cfg.Mailbox, err)
	}

	lastUID := checkpointUID(checkpoint)
	search := imap.SearchCriteria{}
	if lastUID > 0 {
		var uidSet imap.UIDSet
		uidSet.AddRange(lastUID+1, 0)
		search.UID = []imap.UIDSet{uidSet}
	}
	searchResult, err := client.UIDSearch(&search, nil).Wait()
	if err != nil {
		return pollSnapshot{}, fmt.Errorf("uid search: %w", err)
	}
	uids := searchResult.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) == 0 {
		return pollSnapshot{
			Checkpoint: nextCheckpoint(checkpoint, lastUID),
		}, nil
	}

	switch {
	case limit > 0 && lastUID == 0 && len(uids) > limit:
		uids = uids[len(uids)-limit:]
	case limit > 0 && len(uids) > limit:
		uids = uids[:limit]
	}

	var uidSet imap.UIDSet
	uidSet.AddNum(uids...)
	bodySection := &imap.FetchItemBodySection{}
	fetchResult, err := client.Fetch(uidSet, &imap.FetchOptions{
		UID:          true,
		Envelope:     true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{bodySection},
	}).Collect()
	if err != nil {
		return pollSnapshot{}, fmt.Errorf("fetch messages: %w", err)
	}

	conversations := make([]messaging.Conversation, 0, len(fetchResult))
	messages := make([]messaging.Message, 0, len(fetchResult))
	events := make([]messaging.Event, 0, len(fetchResult))
	seenConversations := make(map[messaging.ConversationID]struct{}, len(fetchResult))
	highest := lastUID
	for _, item := range fetchResult {
		if item == nil {
			continue
		}
		raw := item.FindBodySection(bodySection)
		conversation, message, err := normalizeFetchedMessage(cfg, item, raw)
		if err != nil {
			return pollSnapshot{}, err
		}
		if item.UID > highest {
			highest = item.UID
		}
		if _, ok := seenConversations[conversation.ID]; !ok {
			seenConversations[conversation.ID] = struct{}{}
			conversations = append(conversations, conversation)
		}
		messages = append(messages, message)
		events = append(events, messaging.Event{
			ID:             messaging.EventID("evt/" + string(message.ID)),
			Type:           messaging.EventTypeMessageReceived,
			ConnectionID:   cfg.ConnectionID,
			ConversationID: conversation.ID,
			MessageID:      message.ID,
			Timestamp:      message.CreatedAt,
		})
	}

	return pollSnapshot{
		Events:        events,
		Conversations: conversations,
		Messages:      messages,
		Checkpoint:    nextCheckpoint(checkpoint, highest),
	}, nil
}

func searchMailboxMessages(ctx context.Context, cfg adapterConfig, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	if params.ConnectionID != "" && params.ConnectionID != cfg.ConnectionID {
		return protocol.SearchMessagesResult{}, fmt.Errorf("connection_id %q does not match connected account %q", params.ConnectionID, cfg.ConnectionID)
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return protocol.SearchMessagesResult{}, fmt.Errorf("query is required")
	}
	mailbox, err := mailboxForSearchContainer(cfg, params.ContainerID)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	searchCfg := cfg
	searchCfg.Mailbox = mailbox

	client, err := dialAndLoginIMAP(ctx, searchCfg)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	defer closeIMAP(client)

	if _, err := client.Select(searchCfg.Mailbox, nil).Wait(); err != nil {
		return protocol.SearchMessagesResult{}, fmt.Errorf("select mailbox %q: %w", searchCfg.Mailbox, err)
	}

	searchResult, err := client.UIDSearch(&imap.SearchCriteria{
		Text: searchTerms(query),
	}, nil).Wait()
	if err != nil {
		return protocol.SearchMessagesResult{}, fmt.Errorf("uid search: %w", err)
	}
	uids := searchResult.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) == 0 {
		return protocol.SearchMessagesResult{Source: protocol.SearchSourceRemote}, nil
	}

	if params.ConversationID == "" {
		var nextCursor string
		uids, nextCursor, err = paginateUIDs(uids, params.PageRequest)
		if err != nil {
			return protocol.SearchMessagesResult{}, err
		}
		hits, err := fetchMessageSearchHits(client, searchCfg, query, params.ConversationID, uids)
		if err != nil {
			return protocol.SearchMessagesResult{}, err
		}
		return protocol.SearchMessagesResult{
			Hits:       hits,
			Count:      len(hits),
			Source:     protocol.SearchSourceRemote,
			NextCursor: nextCursor,
		}, nil
	}

	hits, err := fetchMessageSearchHits(client, searchCfg, query, params.ConversationID, uids)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	hits, nextCursor, err := paginateMessageSearchHits(hits, params.PageRequest)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	return protocol.SearchMessagesResult{
		Hits:       hits,
		Count:      len(hits),
		Source:     protocol.SearchSourceRemote,
		NextCursor: nextCursor,
	}, nil
}

func fetchMessageSearchHits(client *imapclient.Client, cfg adapterConfig, query string, conversationID messaging.ConversationID, uids []imap.UID) ([]protocol.MessageSearchHit, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	var uidSet imap.UIDSet
	uidSet.AddNum(uids...)
	bodySection := &imap.FetchItemBodySection{}
	fetchResult, err := client.Fetch(uidSet, &imap.FetchOptions{
		UID:          true,
		Envelope:     true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{bodySection},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch search results: %w", err)
	}

	hits := make([]protocol.MessageSearchHit, 0, len(fetchResult))
	for _, item := range fetchResult {
		if item == nil {
			continue
		}
		raw := item.FindBodySection(bodySection)
		conversation, message, err := normalizeFetchedMessage(cfg, item, raw)
		if err != nil {
			return nil, err
		}
		if conversationID != "" && conversation.ID != conversationID {
			continue
		}
		matched := matchedMessageFields(query, conversation, message)
		if len(matched) == 0 {
			matched = []string{"imap_text"}
		}
		conversationCopy := conversation
		hits = append(hits, protocol.MessageSearchHit{
			Message: protocol.MessageRecord{
				Message:    message,
				Placements: []messaging.Placement{placementForMessage(cfg, message)},
			},
			Conversation:  &conversationCopy,
			MatchedFields: matched,
			Source:        protocol.SearchSourceRemote,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		left := hits[i].Message.Message
		right := hits[j].Message.Message
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	return hits, nil
}

func dialAndLoginIMAP(ctx context.Context, cfg adapterConfig) (*imapclient.Client, error) {
	address := fmt.Sprintf("%s:%d", cfg.IMAP.Host, cfg.IMAP.Port)
	options := &imapclient.Options{
		TLSConfig: &tls.Config{
			ServerName: cfg.IMAP.Host,
			MinVersion: tls.VersionTLS12,
		},
	}
	var client *imapclient.Client
	var err error
	switch cfg.IMAP.TLSMode {
	case tlsModeImplicit:
		client, err = imapclient.DialTLS(address, options)
	case tlsModeStartTLS:
		client, err = imapclient.DialStartTLS(address, options)
	case tlsModeInsecure:
		client, err = imapclient.DialInsecure(address, options)
	default:
		err = fmt.Errorf("unsupported imap tls mode %q", cfg.IMAP.TLSMode)
	}
	if err != nil {
		return nil, fmt.Errorf("dial imap %s: %w", address, err)
	}
	if err := client.Login(cfg.IMAP.Username, cfg.IMAP.Password).Wait(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("login imap %s: %w", address, err)
	}
	select {
	case <-ctx.Done():
		_ = client.Logout().Wait()
		_ = client.Close()
		return nil, ctx.Err()
	default:
	}
	return client, nil
}

func closeIMAP(client *imapclient.Client) {
	if client == nil {
		return
	}
	_ = client.Logout().Wait()
	_ = client.Close()
}

func checkpointUID(checkpoint *protocol.Checkpoint) imap.UID {
	if checkpoint == nil {
		return 0
	}
	raw := strings.TrimSpace(firstNonEmpty(checkpoint.Sequence, checkpoint.Cursor))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0
	}
	return imap.UID(value)
}

func nextCheckpoint(previous *protocol.Checkpoint, uid imap.UID) *protocol.Checkpoint {
	next := protocol.Checkpoint{
		Cursor:    strconv.FormatUint(uint64(uid), 10),
		Sequence:  strconv.FormatUint(uint64(uid), 10),
		UpdatedAt: time.Now().UTC(),
	}
	if previous != nil && previous.Metadata != nil {
		next.Metadata = cloneMap(previous.Metadata)
	}
	return &next
}

func normalizeFetchedMessage(cfg adapterConfig, item *imapclient.FetchMessageBuffer, raw []byte) (messaging.Conversation, messaging.Message, error) {
	localIdentity := defaultIdentity(cfg)
	envelope := item.Envelope
	if envelope == nil {
		return messaging.Conversation{}, messaging.Message{}, fmt.Errorf("fetched message %d is missing envelope", item.UID)
	}

	threadKey := threadKeyForEnvelope(envelope, item.UID)
	conversationID := conversationIDFor(cfg, threadKey)
	sender := participantFromAddresses(envelope.From)
	if sender.Address == "" {
		sender = participantFromAddresses(envelope.Sender)
	}
	if sender.Address == "" {
		sender = messaging.Participant{
			Kind:        messaging.ParticipantKindUser,
			DisplayName: "Unknown Sender",
		}
	}

	conversation := messaging.Conversation{
		ID:              conversationID,
		ConnectionID:    cfg.ConnectionID,
		LocalIdentityID: localIdentity.ID,
		Kind:            messaging.ConversationKindEmailThread,
		RemoteID:        threadKey,
		Title:           normalizedSubject(envelope.Subject),
		Participants:    conversationParticipants(localIdentity, envelope),
		Metadata: map[string]string{
			"mailbox": cfg.Mailbox,
			"thread":  threadKey,
		},
	}

	parts := []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: ""}}
	if len(raw) > 0 {
		if parsedParts, err := extractMessageParts(raw); err == nil && len(parsedParts) > 0 {
			parts = parsedParts
		}
	}

	remoteID := envelope.MessageID
	if remoteID == "" {
		remoteID = strconv.FormatUint(uint64(item.UID), 10)
	}
	createdAt := item.InternalDate
	if createdAt.IsZero() {
		createdAt = envelope.Date
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	message := messaging.Message{
		ID:              messageIDFor(cfg, item.UID),
		ConnectionID:    cfg.ConnectionID,
		ConversationID:  conversation.ID,
		LocalIdentityID: localIdentity.ID,
		RemoteID:        remoteID,
		Direction:       messaging.MessageDirectionInbound,
		Sender:          sender,
		Parts:           parts,
		CreatedAt:       createdAt.UTC(),
		Status:          messaging.MessageStatusReceived,
		Metadata: map[string]string{
			"imap_uid":         strconv.FormatUint(uint64(item.UID), 10),
			"mailbox":          cfg.Mailbox,
			"email_message_id": envelope.MessageID,
			"subject":          envelope.Subject,
		},
	}
	if len(envelope.InReplyTo) > 0 {
		message.ReplyToRemoteID = envelope.InReplyTo[len(envelope.InReplyTo)-1]
	}
	return conversation, message, nil
}

func extractMessageParts(raw []byte) ([]messaging.MessagePart, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse raw email: %w", err)
	}
	return extractEntityParts(msg.Header, msg.Body)
}

func extractEntityParts(header mail.Header, body io.Reader) ([]messaging.MessagePart, error) {
	rawBody, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	decodedBody, err := decodeTransferEncoding(header.Get("Content-Transfer-Encoding"), rawBody)
	if err != nil {
		return nil, err
	}

	contentType := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
		params = map[string]string{}
	}

	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: string(decodedBody)}}, nil
		}
		reader := multipart.NewReader(bytes.NewReader(decodedBody), boundary)
		parts := make([]messaging.MessagePart, 0)
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			nestedHeader := mail.Header(part.Header)
			nestedParts, err := extractEntityParts(nestedHeader, part)
			_ = part.Close()
			if err != nil {
				return nil, err
			}
			parts = append(parts, nestedParts...)
		}
		if len(parts) == 0 {
			parts = append(parts, messaging.MessagePart{Kind: messaging.MessagePartKindText, Text: string(decodedBody)})
		}
		return parts, nil
	case mediaType == "text/html":
		return []messaging.MessagePart{{
			Kind:        messaging.MessagePartKindHTML,
			ContentType: mediaType,
			Text:        string(decodedBody),
		}}, nil
	default:
		return []messaging.MessagePart{{
			Kind:        messaging.MessagePartKindText,
			ContentType: mediaType,
			Text:        string(decodedBody),
		}}, nil
	}
}

func decodeTransferEncoding(encoding string, body []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return body, nil
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body)))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
	default:
		return body, nil
	}
}

func threadKeyForEnvelope(envelope *imap.Envelope, uid imap.UID) string {
	switch {
	case len(envelope.InReplyTo) > 0:
		return envelope.InReplyTo[0]
	case envelope.MessageID != "":
		return envelope.MessageID
	default:
		return fmt.Sprintf("uid:%d", uid)
	}
}

func conversationParticipants(local messaging.Identity, envelope *imap.Envelope) []messaging.Participant {
	participants := []messaging.Participant{{
		Kind:        messaging.ParticipantKindAccount,
		IdentityID:  local.ID,
		Address:     local.Address,
		DisplayName: local.DisplayName,
		IsLocal:     true,
	}}
	appendAddresses := func(addrs []imap.Address) {
		for _, addr := range addrs {
			participant := messaging.Participant{
				Kind:        messaging.ParticipantKindUser,
				Address:     addr.Addr(),
				DisplayName: addr.Name,
			}
			if participant.Address == "" {
				continue
			}
			duplicate := false
			for _, existing := range participants {
				if strings.EqualFold(existing.Address, participant.Address) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				participants = append(participants, participant)
			}
		}
	}
	appendAddresses(envelope.From)
	appendAddresses(envelope.To)
	appendAddresses(envelope.Cc)
	return participants
}

func participantFromAddresses(addrs []imap.Address) messaging.Participant {
	if len(addrs) == 0 {
		return messaging.Participant{}
	}
	addr := addrs[0]
	return messaging.Participant{
		Kind:        messaging.ParticipantKindUser,
		Address:     addr.Addr(),
		DisplayName: addr.Name,
	}
}

func defaultIdentity(cfg adapterConfig) messaging.Identity {
	return messaging.Identity{
		ID:           messaging.IdentityID("identity/" + string(cfg.ConnectionID)),
		ConnectionID: cfg.ConnectionID,
		Kind:         messaging.IdentityKindEmail,
		Address:      cfg.EmailAddress,
		DisplayName:  cfg.DisplayName,
		CanReceive:   true,
		CanSend:      true,
		IsDefault:    true,
	}
}

func containersForConfig(cfg adapterConfig) []messaging.Container {
	mailboxes := make([]messaging.Container, 0, 2)
	if mailbox := strings.TrimSpace(cfg.Mailbox); mailbox != "" {
		mailboxes = append(mailboxes, containerForMailbox(cfg, mailbox, containerKindForMailbox(mailbox)))
	}
	if mailbox := strings.TrimSpace(cfg.ArchiveMailbox); mailbox != "" && !sameMailbox(mailbox, cfg.Mailbox) {
		mailboxes = append(mailboxes, containerForMailbox(cfg, mailbox, messaging.ContainerKindArchive))
	}
	sort.Slice(mailboxes, func(i, j int) bool {
		if mailboxes[i].Kind != mailboxes[j].Kind {
			return mailboxes[i].Kind < mailboxes[j].Kind
		}
		return mailboxes[i].Name < mailboxes[j].Name
	})
	return mailboxes
}

func containerForMailbox(cfg adapterConfig, mailbox string, kind messaging.ContainerKind) messaging.Container {
	return messaging.Container{
		ID:           containerIDForMailbox(cfg, mailbox),
		ConnectionID: cfg.ConnectionID,
		Kind:         kind,
		Name:         mailbox,
		RemoteID:     mailbox,
		Metadata: map[string]string{
			"imap_mailbox": mailbox,
		},
	}
}

func placementForMessage(cfg adapterConfig, message messaging.Message) messaging.Placement {
	mailbox := cfg.Mailbox
	if message.Metadata != nil {
		mailbox = firstNonEmpty(message.Metadata["mailbox"], mailbox)
	}
	remoteID := message.RemoteID
	if message.Metadata != nil {
		remoteID = firstNonEmpty(message.Metadata["imap_uid"], remoteID)
	}
	return messaging.Placement{
		MessageID:    message.ID,
		ConnectionID: cfg.ConnectionID,
		ContainerID:  containerIDForMailbox(cfg, mailbox),
		RemoteID:     remoteID,
		Metadata: map[string]string{
			"imap_mailbox": mailbox,
		},
	}
}

func containerIDForMailbox(cfg adapterConfig, mailbox string) messaging.ContainerID {
	return messaging.ContainerID(fmt.Sprintf("container/%s/%s", encodeIDPart(string(cfg.ConnectionID)), encodeIDPart(mailbox)))
}

func containerKindForMailbox(mailbox string) messaging.ContainerKind {
	switch strings.ToLower(strings.TrimSpace(mailbox)) {
	case "inbox":
		return messaging.ContainerKindInbox
	case "archive", "all mail", "[gmail]/all mail":
		return messaging.ContainerKindArchive
	case "trash", "deleted", "deleted items", "[gmail]/trash":
		return messaging.ContainerKindTrash
	case "spam", "junk", "junk email", "[gmail]/spam":
		return messaging.ContainerKindSpam
	case "sent", "sent mail", "sent items", "[gmail]/sent mail":
		return messaging.ContainerKindSent
	case "drafts", "[gmail]/drafts":
		return messaging.ContainerKindDrafts
	default:
		return messaging.ContainerKindFolder
	}
}

func sameMailbox(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func mailboxForSearchContainer(cfg adapterConfig, containerID messaging.ContainerID) (string, error) {
	if containerID == "" {
		return cfg.Mailbox, nil
	}
	for _, container := range containersForConfig(cfg) {
		if container.ID == containerID {
			return container.RemoteID, nil
		}
	}
	return "", fmt.Errorf("container_id %s is not available on imap-smtp connection %s", containerID, cfg.ConnectionID)
}

func searchTerms(query string) []string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return nil
	}
	return terms
}

func paginateUIDs(uids []imap.UID, page protocol.PageRequest) ([]imap.UID, string, error) {
	offset, limit, err := searchPage(page)
	if err != nil {
		return nil, "", err
	}
	if offset >= len(uids) {
		return nil, "", nil
	}
	end := offset + limit
	if end > len(uids) {
		end = len(uids)
	}
	nextCursor := ""
	if end < len(uids) {
		nextCursor = strconv.Itoa(end)
	}
	return uids[offset:end], nextCursor, nil
}

func paginateMessageSearchHits(hits []protocol.MessageSearchHit, page protocol.PageRequest) ([]protocol.MessageSearchHit, string, error) {
	offset, limit, err := searchPage(page)
	if err != nil {
		return nil, "", err
	}
	if offset >= len(hits) {
		return nil, "", nil
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	nextCursor := ""
	if end < len(hits) {
		nextCursor = strconv.Itoa(end)
	}
	return hits[offset:end], nextCursor, nil
}

func searchPage(page protocol.PageRequest) (int, int, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = defaultIMAPSearchLimit
	}
	if limit > maxIMAPSearchLimit {
		limit = maxIMAPSearchLimit
	}
	offset := 0
	if strings.TrimSpace(page.Cursor) != "" {
		parsed, err := strconv.Atoi(page.Cursor)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("invalid cursor")
		}
		offset = parsed
	}
	return offset, limit, nil
}

func matchedMessageFields(query string, conversation messaging.Conversation, message messaging.Message) []string {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return nil
	}
	fields := []struct {
		name  string
		value string
	}{
		{name: "conversation_title", value: conversation.Title},
		{name: "conversation_remote_id", value: conversation.RemoteID},
		{name: "message_id", value: string(message.ID)},
		{name: "remote_id", value: message.RemoteID},
		{name: "sender_address", value: message.Sender.Address},
		{name: "sender_display_name", value: message.Sender.DisplayName},
		{name: "subject", value: message.Metadata["subject"]},
		{name: "email_message_id", value: message.Metadata["email_message_id"]},
	}
	for _, part := range message.Parts {
		fields = append(fields,
			struct {
				name  string
				value string
			}{name: "part_text", value: part.Text},
			struct {
				name  string
				value string
			}{name: "part_file_name", value: part.FileName},
		)
	}

	matched := make(map[string]struct{})
	for _, token := range tokens {
		tokenMatched := false
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field.value), token) {
				matched[field.name] = struct{}{}
				tokenMatched = true
			}
		}
		if !tokenMatched {
			return nil
		}
	}
	names := make([]string, 0, len(matched))
	for name := range matched {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func messageIDFor(cfg adapterConfig, uid imap.UID) messaging.MessageID {
	return messaging.MessageID(fmt.Sprintf("msg/%s/%s/%d", encodeIDPart(string(cfg.ConnectionID)), encodeIDPart(cfg.Mailbox), uid))
}

func conversationIDFor(cfg adapterConfig, threadKey string) messaging.ConversationID {
	sum := sha256.Sum256([]byte(string(cfg.ConnectionID) + "\x00" + cfg.Mailbox + "\x00" + threadKey))
	return messaging.ConversationID("conv/" + hex.EncodeToString(sum[:])[:24])
}

func normalizedSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "(no subject)"
	}
	return subject
}

func encodeIDPart(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func cloneMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
