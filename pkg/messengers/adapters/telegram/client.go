package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

type telegramAPI interface {
	GetMe(context.Context) (*models.User, error)
	DeleteWebhook(context.Context, *tgbot.DeleteWebhookParams) (bool, error)
	GetUpdates(context.Context, getUpdatesRequest) ([]models.Update, error)
	GetFile(context.Context, *tgbot.GetFileParams) (*models.File, error)
	FileDownloadLink(*models.File) string
	SendMessage(context.Context, *tgbot.SendMessageParams) (*models.Message, error)
	DownloadFile(context.Context, string, string, int64) (protocol.BlobRef, error)
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type telegramAPIClient struct {
	bot        *tgbot.Bot
	httpClient *http.Client
	token      string
	baseURL    string
}

func newTelegramAPIClient(cfg adapterConfig) (telegramAPI, error) {
	httpClient := &http.Client{
		Timeout: time.Duration(cfg.PollTimeoutSeconds+15) * time.Second,
	}
	b, err := tgbot.New(
		cfg.BotToken,
		tgbot.WithServerURL(cfg.APIBaseURL),
		tgbot.WithSkipGetMe(),
		tgbot.WithHTTPClient(time.Duration(cfg.PollTimeoutSeconds+1)*time.Second, httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot client: %w", err)
	}
	return &telegramAPIClient{
		bot:        b,
		httpClient: httpClient,
		token:      cfg.BotToken,
		baseURL:    cfg.APIBaseURL,
	}, nil
}

func (c *telegramAPIClient) GetMe(ctx context.Context) (*models.User, error) {
	return c.bot.GetMe(ctx)
}

func (c *telegramAPIClient) DeleteWebhook(ctx context.Context, params *tgbot.DeleteWebhookParams) (bool, error) {
	return c.bot.DeleteWebhook(ctx, params)
}

func (c *telegramAPIClient) GetFile(ctx context.Context, params *tgbot.GetFileParams) (*models.File, error) {
	return c.bot.GetFile(ctx, params)
}

func (c *telegramAPIClient) FileDownloadLink(file *models.File) string {
	return c.bot.FileDownloadLink(file)
}

func (c *telegramAPIClient) SendMessage(ctx context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	return c.bot.SendMessage(ctx, params)
}

func (c *telegramAPIClient) GetUpdates(ctx context.Context, params getUpdatesRequest) ([]models.Update, error) {
	var result []models.Update
	if err := c.rawRequest(ctx, "getUpdates", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *telegramAPIClient) rawRequest(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal telegram %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s request failed: %w", method, err)
	}
	defer res.Body.Close()

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code,omitempty"`
		Description string          `json:"description,omitempty"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode telegram %s response: %w", method, err)
	}
	if !envelope.OK || res.StatusCode < 200 || res.StatusCode >= 300 {
		if envelope.Description != "" {
			return fmt.Errorf("telegram %s failed: %s", method, envelope.Description)
		}
		return fmt.Errorf("telegram %s failed with status %d", method, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode telegram %s result: %w", method, err)
	}
	return nil
}

func (c *telegramAPIClient) DownloadFile(ctx context.Context, url, destPath string, maxBytes int64) (protocol.BlobRef, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return protocol.BlobRef{}, fmt.Errorf("create telegram file download request: %w", err)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return protocol.BlobRef{}, fmt.Errorf("telegram file download failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return protocol.BlobRef{}, fmt.Errorf("telegram file download failed with status %d", res.StatusCode)
	}
	if maxBytes > 0 && res.ContentLength > maxBytes {
		return protocol.BlobRef{}, fmt.Errorf("telegram file is %d bytes, above max_download_bytes %d", res.ContentLength, maxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("create telegram blob dir: %w", err)
	}

	tmpPath := destPath + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return protocol.BlobRef{}, fmt.Errorf("create telegram blob: %w", err)
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(file, hasher)
	reader := res.Body
	if maxBytes > 0 {
		reader = io.NopCloser(io.LimitReader(res.Body, maxBytes+1))
	}
	written, copyErr := io.Copy(writer, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return protocol.BlobRef{}, fmt.Errorf("write telegram blob: %w", copyErr)
	}
	if closeErr != nil {
		return protocol.BlobRef{}, fmt.Errorf("close telegram blob: %w", closeErr)
	}
	if maxBytes > 0 && written > maxBytes {
		return protocol.BlobRef{}, fmt.Errorf("telegram file is larger than max_download_bytes %d", maxBytes)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("commit telegram blob: %w", err)
	}
	removeTmp = false

	return protocol.BlobRef{
		ID:        "telegram:" + filepath.Base(destPath),
		LocalPath: destPath,
		SizeBytes: written,
		SHA256:    hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}
