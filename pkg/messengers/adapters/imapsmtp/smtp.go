package imapsmtp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
)

func sendMailMessage(ctx context.Context, cfg adapterConfig, draft messaging.Draft, recipients []string, headers outboundHeaders) (sendSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return sendSnapshot{}, err
	}
	from := formatAddress(cfg.DisplayName, cfg.EmailAddress)
	subject := headers.Subject
	if strings.TrimSpace(subject) == "" {
		subject = "(no subject)"
	}
	body, contentType := draftBody(draft)
	messageID := generatedMessageID(cfg.EmailAddress)
	raw := buildRFC822Message(from, recipients, subject, body, contentType, messageID, headers)
	if err := smtpSend(ctx, cfg, cfg.EmailAddress, recipients, raw); err != nil {
		return sendSnapshot{}, err
	}

	sender := messaging.Participant{
		Kind:        messaging.ParticipantKindAccount,
		IdentityID:  messaging.IdentityID("identity/" + string(cfg.ConnectionID)),
		Address:     cfg.EmailAddress,
		DisplayName: cfg.DisplayName,
		IsLocal:     true,
	}
	message := messaging.Message{
		ID:              messaging.MessageID("sent/" + string(draft.ID)),
		ConnectionID:    draft.ConnectionID,
		ConversationID:  draft.ConversationID,
		LocalIdentityID: draft.LocalIdentityID,
		RemoteID:        messageID,
		Direction:       messaging.MessageDirectionOutbound,
		Sender:          sender,
		Parts:           cloneParts(draft.Parts),
		CreatedAt:       time.Now().UTC(),
		Status:          messaging.MessageStatusSent,
	}
	if headers.InReplyTo != "" {
		message.ReplyToRemoteID = strings.Trim(headers.InReplyTo, "<>")
	}
	return sendSnapshot{Message: message}, nil
}

func smtpSend(ctx context.Context, cfg adapterConfig, from string, recipients []string, raw []byte) error {
	address := fmt.Sprintf("%s:%d", cfg.SMTP.Host, cfg.SMTP.Port)
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	var conn net.Conn
	var err error
	switch cfg.SMTP.TLSMode {
	case tlsModeImplicit:
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: cfg.SMTP.Host,
			MinVersion: tls.VersionTLS12,
		})
	default:
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", address, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.SMTP.Host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()

	if cfg.SMTP.TLSMode == tlsModeStartTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{
				ServerName: cfg.SMTP.Host,
				MinVersion: tls.VersionTLS12,
			}); err != nil {
				return fmt.Errorf("starttls smtp %s: %w", address, err)
			}
		}
	}

	if cfg.SMTP.Username != "" || cfg.SMTP.Password != "" {
		auth := smtp.PlainAuth("", cfg.SMTP.Username, cfg.SMTP.Password, cfg.SMTP.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth %s: %w", address, err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from %q: %w", from, err)
	}
	uniqueRecipients := append([]string(nil), recipients...)
	sort.Strings(uniqueRecipients)
	uniqueRecipients = slicesCompact(uniqueRecipients)
	for _, recipient := range uniqueRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt to %q: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func buildRFC822Message(from string, recipients []string, subject, body, contentType, messageID string, headers outboundHeaders) []byte {
	var buf bytes.Buffer
	writeHeader := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		buf.WriteString(key)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}
	writeHeader("From", from)
	writeHeader("To", strings.Join(recipients, ", "))
	writeHeader("Subject", subject)
	writeHeader("Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader("Message-ID", "<"+messageID+">")
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", contentType+`; charset="UTF-8"`)
	if headers.InReplyTo != "" {
		writeHeader("In-Reply-To", wrapMessageID(headers.InReplyTo))
	}
	if len(headers.References) > 0 {
		refs := make([]string, 0, len(headers.References))
		for _, ref := range headers.References {
			if strings.TrimSpace(ref) != "" {
				refs = append(refs, wrapMessageID(ref))
			}
		}
		writeHeader("References", strings.Join(refs, " "))
	}
	buf.WriteString("\r\n")
	buf.WriteString(normalizeCRLF(body))
	if !strings.HasSuffix(buf.String(), "\r\n") {
		buf.WriteString("\r\n")
	}
	return buf.Bytes()
}

func draftBody(draft messaging.Draft) (string, string) {
	for _, part := range draft.Parts {
		if part.Kind == messaging.MessagePartKindHTML && strings.TrimSpace(part.Text) != "" {
			return part.Text, "text/html"
		}
	}
	for _, part := range draft.Parts {
		switch part.Kind {
		case messaging.MessagePartKindMarkdown, messaging.MessagePartKindText:
			if strings.TrimSpace(part.Text) != "" {
				return part.Text, "text/plain"
			}
		}
	}
	return "", "text/plain"
}

func headersForDraft(state *connectionState, draft messaging.Draft, replyToMessageID messaging.MessageID, replyToRemoteID string, reply bool) outboundHeaders {
	headers := outboundHeaders{
		Subject: strings.TrimSpace(draft.Metadata["subject"]),
	}
	if headers.Subject == "" {
		if conversation, ok := state.conversations[draft.ConversationID]; ok {
			headers.Subject = conversation.Title
		}
	}
	if headers.Subject == "" {
		headers.Subject = state.config.Label
	}
	if reply && headers.Subject != "" && !strings.HasPrefix(strings.ToLower(headers.Subject), "re:") {
		headers.Subject = "Re: " + headers.Subject
	}
	if replyToMessageID != "" {
		if message, ok := state.messages[replyToMessageID]; ok {
			headers.InReplyTo = firstNonEmpty(message.RemoteID, message.Metadata["email_message_id"])
			if ref := firstNonEmpty(message.Metadata["references"], headers.InReplyTo); ref != "" {
				headers.References = strings.Fields(ref)
			}
		}
	}
	if headers.InReplyTo == "" {
		headers.InReplyTo = replyToRemoteID
	}
	if headers.InReplyTo != "" && len(headers.References) == 0 {
		headers.References = []string{headers.InReplyTo}
	}
	return headers
}

func recipientsForDraft(state *connectionState, draft messaging.Draft, replyToMessageID messaging.MessageID, replyToRemoteID string) []string {
	recipients := make([]string, 0)
	if replyToMessageID != "" {
		if message, ok := state.messages[replyToMessageID]; ok && message.Sender.Address != "" {
			recipients = append(recipients, message.Sender.Address)
		}
	}
	if len(recipients) == 0 && replyToRemoteID != "" {
		for _, message := range state.messages {
			if message.RemoteID == replyToRemoteID && message.Sender.Address != "" {
				recipients = append(recipients, message.Sender.Address)
				break
			}
		}
	}
	if len(recipients) == 0 {
		if conversation, ok := state.conversations[draft.ConversationID]; ok {
			for _, participant := range conversation.Participants {
				if participant.IsLocal || strings.EqualFold(participant.Address, state.config.EmailAddress) {
					continue
				}
				if participant.Address != "" {
					recipients = append(recipients, participant.Address)
				}
			}
		}
	}
	if len(recipients) == 0 {
		for _, field := range []string{"to", "recipients"} {
			for _, recipient := range strings.Split(draft.Metadata[field], ",") {
				recipient = strings.TrimSpace(recipient)
				if recipient != "" {
					recipients = append(recipients, recipient)
				}
			}
		}
	}
	sort.Strings(recipients)
	return slicesCompact(recipients)
}

func formatAddress(name, address string) string {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name == "" {
		return address
	}
	return fmt.Sprintf("%s <%s>", name, address)
}

func generatedMessageID(address string) string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return fmt.Sprintf("%d@sky10.local", time.Now().UnixNano())
	}
	domain := "sky10.local"
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		domain = address[idx+1:]
	}
	return hex.EncodeToString(random) + "@" + domain
}

func wrapMessageID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		return value
	}
	return "<" + value + ">"
}

func normalizeCRLF(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}

func cloneParts(parts []messaging.MessagePart) []messaging.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]messaging.MessagePart, 0, len(parts))
	for _, part := range parts {
		copy := part
		if len(part.Metadata) > 0 {
			copy.Metadata = make(map[string]string, len(part.Metadata))
			for key, value := range part.Metadata {
				copy.Metadata[key] = value
			}
		}
		out = append(out, copy)
	}
	return out
}

func slicesCompact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:0]
	var last string
	for idx, value := range values {
		if idx == 0 || value != last {
			out = append(out, value)
			last = value
		}
	}
	return out
}
