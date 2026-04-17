package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const githubAcceptHeader = "application/vnd.github+json"

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Latest struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type GitHubClient struct {
	HTTPClient *http.Client
	UserAgent  string
}

func NewGitHubClient(userAgent string) *GitHubClient {
	return &GitHubClient{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		UserAgent:  strings.TrimSpace(userAgent),
	}
}

func (c *GitHubClient) Latest(ctx context.Context, url string) (*Latest, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building release request: %w", err)
	}
	if c != nil && strings.TrimSpace(c.UserAgent) != "" {
		req.Header.Set("User-Agent", strings.TrimSpace(c.UserAgent))
	}
	req.Header.Set("Accept", githubAcceptHeader)

	client := http.DefaultClient
	if c != nil && c.HTTPClient != nil {
		client = c.HTTPClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
		}
		return nil, fmt.Errorf("GitHub API returned %s: %s", resp.Status, msg)
	}

	var release Latest
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	return &release, nil
}
