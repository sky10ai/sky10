package imapsmtp

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

const (
	metaIMAPHost     = "imap_host"
	metaIMAPPort     = "imap_port"
	metaIMAPTLSMode  = "imap_tls_mode"
	metaIMAPMailbox  = "imap_mailbox"
	metaSMTPHost     = "smtp_host"
	metaSMTPPort     = "smtp_port"
	metaSMTPTLSMode  = "smtp_tls_mode"
	metaEmailAddress = "email_address"
	metaDisplayName  = "display_name"
	metaPollLimit    = "poll_limit"
)

type tlsMode string

const (
	tlsModeImplicit tlsMode = "tls"
	tlsModeStartTLS tlsMode = "starttls"
	tlsModeInsecure tlsMode = "insecure"
)

type endpointConfig struct {
	Host     string
	Port     int
	TLSMode  tlsMode
	Username string
	Password string
}

type adapterConfig struct {
	ConnectionID messaging.ConnectionID
	Label        string
	EmailAddress string
	DisplayName  string
	Mailbox      string
	PollLimit    int
	IMAP         endpointConfig
	SMTP         endpointConfig
}

type credentialPayload struct {
	EmailAddress string `json:"email_address,omitempty"`
	Email        string `json:"email,omitempty"`
	Address      string `json:"address,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	Name         string `json:"name,omitempty"`

	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	IMAPUsername string `json:"imap_username,omitempty"`
	IMAPPassword string `json:"imap_password,omitempty"`

	SMTPUsername string `json:"smtp_username,omitempty"`
	SMTPPassword string `json:"smtp_password,omitempty"`
}

func parseConfig(connection messaging.Connection, credential *protocol.ResolvedCredential) (adapterConfig, error) {
	cfg := adapterConfig{
		ConnectionID: connection.ID,
		Label:        connection.Label,
		Mailbox:      "INBOX",
		PollLimit:    50,
		IMAP: endpointConfig{
			TLSMode: tlsModeImplicit,
		},
		SMTP: endpointConfig{
			TLSMode: tlsModeStartTLS,
		},
	}
	if connection.Metadata != nil {
		cfg.IMAP.Host = strings.TrimSpace(connection.Metadata[metaIMAPHost])
		cfg.SMTP.Host = strings.TrimSpace(connection.Metadata[metaSMTPHost])
		cfg.Mailbox = firstNonEmpty(connection.Metadata[metaIMAPMailbox], cfg.Mailbox)
		cfg.EmailAddress = strings.TrimSpace(connection.Metadata[metaEmailAddress])
		cfg.DisplayName = strings.TrimSpace(connection.Metadata[metaDisplayName])
		if value := strings.TrimSpace(connection.Metadata[metaIMAPTLSMode]); value != "" {
			mode, err := parseTLSMode(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s: %w", metaIMAPTLSMode, err)
			}
			cfg.IMAP.TLSMode = mode
		}
		if value := strings.TrimSpace(connection.Metadata[metaSMTPTLSMode]); value != "" {
			mode, err := parseTLSMode(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s: %w", metaSMTPTLSMode, err)
			}
			cfg.SMTP.TLSMode = mode
		}
		if value := strings.TrimSpace(connection.Metadata[metaIMAPPort]); value != "" {
			port, err := parsePort(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s: %w", metaIMAPPort, err)
			}
			cfg.IMAP.Port = port
		}
		if value := strings.TrimSpace(connection.Metadata[metaSMTPPort]); value != "" {
			port, err := parsePort(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s: %w", metaSMTPPort, err)
			}
			cfg.SMTP.Port = port
		}
		if value := strings.TrimSpace(connection.Metadata[metaPollLimit]); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				return adapterConfig{}, fmt.Errorf("%s must be a positive integer", metaPollLimit)
			}
			cfg.PollLimit = limit
		}
	}

	if cfg.IMAP.Port == 0 {
		cfg.IMAP.Port = defaultIMAPPort(cfg.IMAP.TLSMode)
	}
	if cfg.SMTP.Port == 0 {
		cfg.SMTP.Port = defaultSMTPPort(cfg.SMTP.TLSMode)
	}

	payload, err := parseCredentialPayload(credential)
	if err != nil {
		return adapterConfig{}, err
	}
	cfg.EmailAddress = firstNonEmpty(cfg.EmailAddress, payload.EmailAddress, payload.Email, payload.Address, connection.Auth.ExternalAccount, emailLike(payload.Username))
	cfg.DisplayName = firstNonEmpty(cfg.DisplayName, payload.DisplayName, payload.Name)
	cfg.IMAP.Username = firstNonEmpty(payload.IMAPUsername, payload.Username, cfg.EmailAddress)
	cfg.IMAP.Password = firstNonEmpty(payload.IMAPPassword, payload.Password)
	cfg.SMTP.Username = firstNonEmpty(payload.SMTPUsername, payload.Username, cfg.EmailAddress)
	cfg.SMTP.Password = firstNonEmpty(payload.SMTPPassword, payload.Password)

	if connection.AdapterID != "imap-smtp" {
		return adapterConfig{}, fmt.Errorf("connection adapter_id %q does not match imap-smtp", connection.AdapterID)
	}
	if connection.Auth.Method != messaging.AuthMethodBasic && connection.Auth.Method != messaging.AuthMethodAppPassword {
		return adapterConfig{}, fmt.Errorf("auth method %q is not supported by imap-smtp", connection.Auth.Method)
	}
	if strings.TrimSpace(cfg.IMAP.Host) == "" {
		return adapterConfig{}, fmt.Errorf("%s is required", metaIMAPHost)
	}
	if strings.TrimSpace(cfg.SMTP.Host) == "" {
		return adapterConfig{}, fmt.Errorf("%s is required", metaSMTPHost)
	}
	if strings.TrimSpace(cfg.IMAP.Username) == "" || strings.TrimSpace(cfg.IMAP.Password) == "" {
		return adapterConfig{}, fmt.Errorf("imap credentials are required")
	}
	if strings.TrimSpace(cfg.SMTP.Username) == "" || strings.TrimSpace(cfg.SMTP.Password) == "" {
		return adapterConfig{}, fmt.Errorf("smtp credentials are required")
	}
	if strings.TrimSpace(cfg.EmailAddress) == "" {
		return adapterConfig{}, fmt.Errorf("%s or auth.external_account is required", metaEmailAddress)
	}
	cfg.Mailbox = strings.TrimSpace(cfg.Mailbox)
	if cfg.Mailbox == "" {
		cfg.Mailbox = "INBOX"
	}
	return cfg, nil
}

func parseCredentialPayload(credential *protocol.ResolvedCredential) (credentialPayload, error) {
	if credential == nil {
		return credentialPayload{}, fmt.Errorf("resolved credential is required")
	}
	if strings.TrimSpace(credential.Blob.LocalPath) == "" {
		return credentialPayload{}, fmt.Errorf("credential blob local_path is required")
	}
	raw, err := os.ReadFile(credential.Blob.LocalPath)
	if err != nil {
		return credentialPayload{}, fmt.Errorf("read credential payload: %w", err)
	}
	var payload credentialPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return credentialPayload{}, fmt.Errorf("parse credential payload: %w", err)
	}
	return payload, nil
}

func parseTLSMode(raw string) (tlsMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(tlsModeImplicit):
		return tlsModeImplicit, nil
	case string(tlsModeStartTLS):
		return tlsModeStartTLS, nil
	case string(tlsModeInsecure):
		return tlsModeInsecure, nil
	default:
		return "", fmt.Errorf("unsupported tls mode %q", raw)
	}
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", raw)
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port %d is out of range", port)
	}
	return port, nil
}

func defaultIMAPPort(mode tlsMode) int {
	switch mode {
	case tlsModeStartTLS, tlsModeInsecure:
		return 143
	default:
		return 993
	}
}

func defaultSMTPPort(mode tlsMode) int {
	switch mode {
	case tlsModeImplicit:
		return 465
	case tlsModeInsecure:
		return 25
	default:
		return 587
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func emailLike(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") {
		return value
	}
	return ""
}
