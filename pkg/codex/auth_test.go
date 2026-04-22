package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAuthorizeURLIncludesCodexOAuthParams(t *testing.T) {
	t.Parallel()

	got := buildAuthorizeURL(defaultOAuthConfig(), "challenge-value", "state-value")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	query := parsed.Query()
	if parsed.Scheme != "https" || parsed.Host != "auth.openai.com" || parsed.Path != "/oauth/authorize" {
		t.Fatalf("authorize url = %q", got)
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q, want code", query.Get("response_type"))
	}
	if query.Get("client_id") != defaultOAuthClientID {
		t.Fatalf("client_id = %q, want %q", query.Get("client_id"), defaultOAuthClientID)
	}
	if query.Get("redirect_uri") != defaultRedirectURL {
		t.Fatalf("redirect_uri = %q, want %q", query.Get("redirect_uri"), defaultRedirectURL)
	}
	if query.Get("scope") != defaultOAuthScope {
		t.Fatalf("scope = %q, want %q", query.Get("scope"), defaultOAuthScope)
	}
	if query.Get("code_challenge") != "challenge-value" {
		t.Fatalf("code_challenge = %q", query.Get("code_challenge"))
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", query.Get("code_challenge_method"))
	}
	if query.Get("state") != "state-value" {
		t.Fatalf("state = %q, want state-value", query.Get("state"))
	}
	if query.Get("id_token_add_organizations") != "true" {
		t.Fatalf("id_token_add_organizations = %q, want true", query.Get("id_token_add_organizations"))
	}
	if query.Get("codex_cli_simplified_flow") != "true" {
		t.Fatalf("codex_cli_simplified_flow = %q, want true", query.Get("codex_cli_simplified_flow"))
	}
	if query.Get("originator") != defaultOAuthOriginator {
		t.Fatalf("originator = %q, want %q", query.Get("originator"), defaultOAuthOriginator)
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		code   string
		state  string
		hasErr bool
	}{
		{
			name:  "full callback url",
			input: "http://localhost:1455/auth/callback?code=abc123&state=xyz789",
			code:  "abc123",
			state: "xyz789",
		},
		{
			name:  "raw query string",
			input: "code=abc123&state=xyz789",
			code:  "abc123",
			state: "xyz789",
		},
		{
			name:  "manual code and state",
			input: "abc123#xyz789",
			code:  "abc123",
			state: "xyz789",
		},
		{
			name:  "raw code",
			input: "abc123",
			code:  "abc123",
			state: "",
		},
		{
			name:   "empty",
			input:  "   ",
			hasErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, state, err := parseAuthorizationInput(tt.input)
			if tt.hasErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAuthorizationInput(%q): %v", tt.input, err)
			}
			if code != tt.code {
				t.Fatalf("code = %q, want %q", code, tt.code)
			}
			if state != tt.state {
				t.Fatalf("state = %q, want %q", state, tt.state)
			}
		})
	}
}

func TestBuildChatInput(t *testing.T) {
	t.Parallel()

	input, err := buildChatInput([]ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	if err != nil {
		t.Fatalf("buildChatInput: %v", err)
	}
	if len(input) != 2 {
		t.Fatalf("len(input) = %d, want 2", len(input))
	}

	user := input[0]
	if user["role"] != "user" {
		t.Fatalf("user role = %v, want user", user["role"])
	}

	assistant := input[1]
	if assistant["type"] != "message" {
		t.Fatalf("assistant type = %v, want message", assistant["type"])
	}
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant role = %v, want assistant", assistant["role"])
	}
}

func TestServiceStartLoginCompletesViaCallback(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	server := newFakeTokenServer(t, fakeTokenServerConfig{
		authorizeCodeAccessToken: mustJWT(t, now.Add(2*time.Hour), "callback@example.com", "acct_callback"),
		authorizeCodeRefresh:     "refresh-callback",
	})

	service := newTestService(t, server.URL, now)
	started, err := service.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if started.PendingLogin == nil {
		t.Fatalf("StartLogin pending_login = nil")
	}
	if !started.PendingLogin.CallbackListening {
		t.Fatalf("callback listener was not started")
	}

	service.mu.Lock()
	session := service.pending
	service.mu.Unlock()
	if session == nil {
		t.Fatalf("service.pending = nil")
	}

	callbackURL := fmt.Sprintf("%s?code=callback-code&state=%s", started.PendingLogin.RedirectURI, session.state)
	res, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("calling oauth callback: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", res.StatusCode)
	}

	waitFor(t, 2*time.Second, func() bool {
		status, err := service.Status(context.Background())
		return err == nil && status.Linked && status.PendingLogin == nil
	})

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after callback: %v", err)
	}
	if !status.Linked {
		t.Fatalf("linked = false, want true")
	}
	if status.AuthSource != "host_oauth" {
		t.Fatalf("auth_source = %q, want host_oauth", status.AuthSource)
	}
	if status.Email != "callback@example.com" {
		t.Fatalf("email = %q, want callback@example.com", status.Email)
	}
	if status.AccountID != "acct_callback" {
		t.Fatalf("account_id = %q, want acct_callback", status.AccountID)
	}
}

func TestServiceCompleteLoginSupportsManualPaste(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	server := newFakeTokenServer(t, fakeTokenServerConfig{
		authorizeCodeAccessToken: mustJWT(t, now.Add(2*time.Hour), "manual@example.com", "acct_manual"),
		authorizeCodeRefresh:     "refresh-manual",
	})

	service := newTestService(t, server.URL, now)
	started, err := service.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if started.PendingLogin == nil {
		t.Fatalf("StartLogin pending_login = nil")
	}

	service.mu.Lock()
	session := service.pending
	service.mu.Unlock()
	if session == nil {
		t.Fatalf("service.pending = nil")
	}

	input := fmt.Sprintf("%s?code=manual-code&state=%s", started.PendingLogin.RedirectURI, session.state)
	status, err := service.CompleteLogin(context.Background(), CompleteLoginParams{
		AuthorizationInput: input,
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if !status.Linked {
		t.Fatalf("linked = false, want true")
	}
	if status.Email != "manual@example.com" {
		t.Fatalf("email = %q, want manual@example.com", status.Email)
	}

	cred, err := service.loadCredential()
	if err != nil {
		t.Fatalf("loadCredential: %v", err)
	}
	if cred == nil {
		t.Fatalf("credential was not persisted")
	}
	if cred.Email != "manual@example.com" {
		t.Fatalf("stored email = %q, want manual@example.com", cred.Email)
	}
}

func TestServiceStatusRefreshesExpiringCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	server := newFakeTokenServer(t, fakeTokenServerConfig{
		refreshAccessToken: mustJWT(t, now.Add(3*time.Hour), "refresh@example.com", "acct_refresh"),
		refreshToken:       "",
	})

	service := newTestService(t, server.URL, now)
	if err := service.saveCredential(&storedCredential{
		Version:      credentialVersion,
		Type:         "oauth",
		Provider:     "openai-codex",
		AccessToken:  mustJWT(t, now.Add(30*time.Second), "old@example.com", "acct_old"),
		RefreshToken: "refresh-old",
		ExpiresAt:    now.Add(30 * time.Second),
		Email:        "old@example.com",
		AccountID:    "acct_old",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("saveCredential: %v", err)
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Linked {
		t.Fatalf("linked = false, want true")
	}
	if status.Email != "refresh@example.com" {
		t.Fatalf("email = %q, want refresh@example.com", status.Email)
	}

	cred, err := service.loadCredential()
	if err != nil {
		t.Fatalf("loadCredential: %v", err)
	}
	if cred == nil {
		t.Fatalf("credential disappeared after refresh")
	}
	if cred.Email != "refresh@example.com" {
		t.Fatalf("stored email = %q, want refresh@example.com", cred.Email)
	}
	if cred.RefreshToken != "refresh-old" {
		t.Fatalf("refresh token = %q, want original refresh-old", cred.RefreshToken)
	}
}

func TestServiceStatusKeepsLinkedWhenRefreshFailsButTokenStillValid(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "refresh failed", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	service := newTestService(t, server.URL, now)
	if err := service.saveCredential(&storedCredential{
		Version:      credentialVersion,
		Type:         "oauth",
		Provider:     "openai-codex",
		AccessToken:  mustJWT(t, now.Add(30*time.Second), "still-valid@example.com", "acct_still_valid"),
		RefreshToken: "refresh-old",
		ExpiresAt:    now.Add(30 * time.Second),
		Email:        "still-valid@example.com",
		AccountID:    "acct_still_valid",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("saveCredential: %v", err)
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Linked {
		t.Fatalf("linked = false, want true while token is still valid")
	}
	if !strings.Contains(status.LastError, "refresh failed") {
		t.Fatalf("last_error = %q, want refresh failure", status.LastError)
	}
}

func TestServiceLogoutClearsHostCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	service := newTestService(t, "http://127.0.0.1:1", now)
	if err := service.saveCredential(&storedCredential{
		Version:      credentialVersion,
		Type:         "oauth",
		Provider:     "openai-codex",
		AccessToken:  mustJWT(t, now.Add(2*time.Hour), "logout@example.com", "acct_logout"),
		RefreshToken: "refresh-logout",
		ExpiresAt:    now.Add(2 * time.Hour),
		Email:        "logout@example.com",
		AccountID:    "acct_logout",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("saveCredential: %v", err)
	}

	status, err := service.Logout(context.Background())
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if status.Linked {
		t.Fatalf("linked = true, want false")
	}

	cred, err := service.loadCredential()
	if err != nil {
		t.Fatalf("loadCredential after logout: %v", err)
	}
	if cred != nil {
		t.Fatalf("credential still present after logout")
	}

	storePath, err := service.storePath()
	if err != nil {
		t.Fatalf("storePath: %v", err)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("auth store still exists: %v", err)
	}
}

func TestServiceChatUsesStoredCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	var seenAuth string
	var seenAccount string
	var seenOriginator string
	var seenBeta string
	var body map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		seenAccount = r.Header.Get("chatgpt-account-id")
		seenOriginator = r.Header.Get("originator")
		seenBeta = r.Header.Get("OpenAI-Beta")

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "resp_123",
			"output": []map[string]interface{}{
				{
					"type": "message",
					"role": "assistant",
					"content": []map[string]interface{}{
						{
							"type": "output_text",
							"text": "hello from codex",
						},
					},
				},
			},
			"usage": map[string]int{
				"input_tokens":  12,
				"output_tokens": 7,
				"total_tokens":  19,
			},
		})
	}))
	t.Cleanup(server.Close)

	service := newTestService(t, server.URL, now)
	if err := service.saveCredential(&storedCredential{
		Version:      credentialVersion,
		Type:         "oauth",
		Provider:     "openai-codex",
		AccessToken:  mustJWT(t, now.Add(2*time.Hour), "chat@example.com", "acct_chat"),
		RefreshToken: "refresh-chat",
		ExpiresAt:    now.Add(2 * time.Hour),
		Email:        "chat@example.com",
		AccountID:    "acct_chat",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("saveCredential: %v", err)
	}

	result, err := service.Chat(context.Background(), ChatParams{
		Model: "gpt-5.4",
		Messages: []ChatMessage{
			{Role: "user", Content: "say hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.Text != "hello from codex" {
		t.Fatalf("result.Text = %q, want hello from codex", result.Text)
	}
	if result.ResponseID != "resp_123" {
		t.Fatalf("response_id = %q, want resp_123", result.ResponseID)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 19 {
		t.Fatalf("usage = %#v, want total 19", result.Usage)
	}
	if seenAuth == "" || !strings.HasPrefix(seenAuth, "Bearer ") {
		t.Fatalf("authorization header = %q, want bearer token", seenAuth)
	}
	if seenAccount != "acct_chat" {
		t.Fatalf("chatgpt-account-id = %q, want acct_chat", seenAccount)
	}
	if seenOriginator != "sky10-tests" {
		t.Fatalf("originator = %q, want sky10-tests", seenOriginator)
	}
	if seenBeta != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q, want responses=experimental", seenBeta)
	}
	if body["model"] != "gpt-5.4" {
		t.Fatalf("model = %v, want gpt-5.4", body["model"])
	}
	if body["stream"] != false {
		t.Fatalf("stream = %v, want false", body["stream"])
	}
}

type fakeTokenServerConfig struct {
	authorizeCodeAccessToken string
	authorizeCodeRefresh     string
	refreshAccessToken       string
	refreshToken             string
}

func newFakeTokenServer(t *testing.T, cfg fakeTokenServerConfig) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var resp tokenResponse
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			resp = tokenResponse{
				AccessToken:  cfg.authorizeCodeAccessToken,
				RefreshToken: cfg.authorizeCodeRefresh,
				ExpiresIn:    7200,
			}
		case "refresh_token":
			resp = tokenResponse{
				AccessToken:  cfg.refreshAccessToken,
				RefreshToken: cfg.refreshToken,
				ExpiresIn:    7200,
			}
		default:
			http.Error(w, "unexpected grant_type", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode token response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newTestService(t *testing.T, tokenURL string, now time.Time) *Service {
	t.Helper()

	storeDir := t.TempDir()
	service := NewService(nil)
	service.now = func() time.Time { return now }
	service.findBinary = func() (string, error) { return "", exec.ErrNotFound }
	service.storePath = func() (string, error) {
		return filepath.Join(storeDir, "codex", "auth.json"), nil
	}
	service.oauth = oauthConfig{
		ClientID:     "test-client-id",
		AuthorizeURL: "https://auth.openai.com/oauth/authorize",
		TokenURL:     tokenURL,
		RedirectURL:  freeRedirectURL(t),
		Scope:        defaultOAuthScope,
		Originator:   "sky10-tests",
	}
	service.codexBase = tokenURL
	return service
}

func freeRedirectURL(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatalf("close free port listener: %v", err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/auth/callback", addr.Port)
}

func mustJWT(t *testing.T, exp time.Time, email string, accountID string) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"exp":                                  exp.Unix(),
		"email":                                email,
		"https://api.openai.com/profile.email": email,
		"https://api.openai.com/auth.chatgpt_account_id": accountID,
	})
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".signature"
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not satisfied within %s", timeout)
}
