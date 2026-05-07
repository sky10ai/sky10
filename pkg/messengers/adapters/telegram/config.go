package telegram

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
	defaultAPIBaseURL       = "https://api.telegram.org"
	defaultPollLimit        = 100
	defaultPollTimeout      = 10
	defaultMaxDownloadBytes = 20 << 20

	credentialBotToken = "bot_token"

	metaAPIBaseURL           = "telegram_api_base_url"
	metaPollLimit            = "poll_limit"
	metaPollTimeoutSeconds   = "poll_timeout_seconds"
	metaDownloadMedia        = "download_media"
	metaMaxDownloadBytes     = "max_download_bytes"
	metaDropPendingOnConnect = "drop_pending_updates_on_connect"
)

type adapterConfig struct {
	ConnectionID         messaging.ConnectionID
	Label                string
	BotToken             string
	APIBaseURL           string
	PollLimit            int
	PollTimeoutSeconds   int
	DownloadMedia        bool
	MaxDownloadBytes     int64
	DropPendingOnConnect bool
	Paths                protocol.RuntimePaths
}

type credentialPayload struct {
	BotToken      string `json:"bot_token,omitempty"`
	Token         string `json:"token,omitempty"`
	TelegramToken string `json:"telegram_bot_token,omitempty"`
}

func parseConfig(connection messaging.Connection, paths protocol.RuntimePaths, credential *protocol.ResolvedCredential) (adapterConfig, error) {
	cfg := adapterConfig{
		ConnectionID:       connection.ID,
		Label:              connection.Label,
		APIBaseURL:         defaultAPIBaseURL,
		PollLimit:          defaultPollLimit,
		PollTimeoutSeconds: defaultPollTimeout,
		DownloadMedia:      true,
		MaxDownloadBytes:   defaultMaxDownloadBytes,
		Paths:              paths,
	}
	if connection.Metadata != nil {
		cfg.APIBaseURL = firstNonEmpty(connection.Metadata[metaAPIBaseURL], cfg.APIBaseURL)
		if value := strings.TrimSpace(connection.Metadata[metaPollLimit]); value != "" {
			limit, err := parsePositiveInt(value, metaPollLimit)
			if err != nil {
				return adapterConfig{}, err
			}
			cfg.PollLimit = clampTelegramPollLimit(limit)
		}
		if value := strings.TrimSpace(connection.Metadata[metaPollTimeoutSeconds]); value != "" {
			timeout, err := parseNonNegativeInt(value, metaPollTimeoutSeconds)
			if err != nil {
				return adapterConfig{}, err
			}
			cfg.PollTimeoutSeconds = timeout
		}
		if value := strings.TrimSpace(connection.Metadata[metaDownloadMedia]); value != "" {
			download, err := strconv.ParseBool(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s must be a boolean", metaDownloadMedia)
			}
			cfg.DownloadMedia = download
		}
		if value := strings.TrimSpace(connection.Metadata[metaMaxDownloadBytes]); value != "" {
			maxBytes, err := parseNonNegativeInt64(value, metaMaxDownloadBytes)
			if err != nil {
				return adapterConfig{}, err
			}
			cfg.MaxDownloadBytes = maxBytes
		}
		if value := strings.TrimSpace(connection.Metadata[metaDropPendingOnConnect]); value != "" {
			drop, err := strconv.ParseBool(value)
			if err != nil {
				return adapterConfig{}, fmt.Errorf("%s must be a boolean", metaDropPendingOnConnect)
			}
			cfg.DropPendingOnConnect = drop
		}
	}

	payload, err := parseCredentialPayload(credential)
	if err != nil {
		return adapterConfig{}, err
	}
	cfg.BotToken = firstNonEmpty(payload.BotToken, payload.Token, payload.TelegramToken)

	if connection.AdapterID != "telegram" {
		return adapterConfig{}, fmt.Errorf("connection adapter_id %q does not match telegram", connection.AdapterID)
	}
	if connection.Auth.Method != messaging.AuthMethodBotToken {
		return adapterConfig{}, fmt.Errorf("auth method %q is not supported by telegram", connection.Auth.Method)
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		return adapterConfig{}, fmt.Errorf("%s is required", credentialBotToken)
	}
	cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	cfg.PollLimit = clampTelegramPollLimit(cfg.PollLimit)
	if cfg.PollTimeoutSeconds < 0 {
		cfg.PollTimeoutSeconds = defaultPollTimeout
	}
	if cfg.MaxDownloadBytes < 0 {
		cfg.MaxDownloadBytes = defaultMaxDownloadBytes
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

func parsePositiveInt(raw, field string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	return value, nil
}

func parseNonNegativeInt(raw, field string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	return value, nil
}

func parseNonNegativeInt64(raw, field string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	return value, nil
}

func clampTelegramPollLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultPollLimit
	case limit > 100:
		return 100
	default:
		return limit
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
