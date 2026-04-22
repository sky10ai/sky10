package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultOAuthClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultAuthorizeURL    = "https://auth.openai.com/oauth/authorize"
	defaultTokenURL        = "https://auth.openai.com/oauth/token"
	defaultRedirectURL     = "http://localhost:1455/auth/callback"
	defaultCodexBaseURL    = "https://chatgpt.com/backend-api"
	defaultOAuthScope      = "openid profile email offline_access"
	defaultOAuthOriginator = "sky10"
)

type oauthConfig struct {
	ClientID     string
	AuthorizeURL string
	TokenURL     string
	RedirectURL  string
	Scope        string
	Originator   string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token,omitempty"`
}

type callbackResult struct {
	code string
	err  error
}

type callbackServer struct {
	server *http.Server
	codes  chan callbackResult
}

func defaultOAuthConfig() oauthConfig {
	return oauthConfig{
		ClientID:     defaultOAuthClientID,
		AuthorizeURL: defaultAuthorizeURL,
		TokenURL:     defaultTokenURL,
		RedirectURL:  defaultRedirectURL,
		Scope:        defaultOAuthScope,
		Originator:   defaultOAuthOriginator,
	}
}

func generatePKCE() (verifier string, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func generateOAuthState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func randomID() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func buildAuthorizeURL(cfg oauthConfig, challenge, state string) string {
	u, _ := url.Parse(cfg.AuthorizeURL)
	query := u.Query()
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", cfg.RedirectURL)
	query.Set("scope", cfg.Scope)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", cfg.Originator)
	u.RawQuery = query.Encode()
	return u.String()
}

func resolveCodexURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	switch {
	case strings.HasSuffix(normalized, "/codex/responses"):
		return normalized
	case strings.HasSuffix(normalized, "/codex"):
		return normalized + "/responses"
	default:
		return normalized + "/codex/responses"
	}
}

func parseAuthorizationInput(input string) (code string, state string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", fmt.Errorf("authorization input is required")
	}

	if parsedURL, parseErr := url.Parse(trimmed); parseErr == nil && parsedURL.Scheme != "" {
		code = parsedURL.Query().Get("code")
		state = parsedURL.Query().Get("state")
		if code != "" {
			return code, state, nil
		}
	}

	if strings.Contains(trimmed, "#") {
		parts := strings.SplitN(trimmed, "#", 2)
		code = strings.TrimSpace(parts[0])
		if len(parts) == 2 {
			state = strings.TrimSpace(parts[1])
		}
		return code, state, nil
	}

	if strings.Contains(trimmed, "code=") {
		values, parseErr := url.ParseQuery(trimmed)
		if parseErr == nil {
			code = values.Get("code")
			state = values.Get("state")
			if code != "" {
				return code, state, nil
			}
		}
	}

	return trimmed, "", nil
}

func startLocalOAuthServer(redirectURL string, expectedState string) (*callbackServer, error) {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("parse redirect url: %w", err)
	}

	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "1455"
	}
	path := u.Path
	if path == "" {
		path = "/"
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}

	server := &callbackServer{
		codes: make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	server.server = httpServer
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			writeOAuthPage(w, http.StatusBadRequest, "OpenAI authentication did not complete.", "State mismatch.")
			return
		}
		code := r.URL.Query().Get("code")
		if strings.TrimSpace(code) == "" {
			writeOAuthPage(w, http.StatusBadRequest, "OpenAI authentication did not complete.", "Missing authorization code.")
			return
		}
		writeOAuthPage(w, http.StatusOK, "OpenAI authentication completed.", "You can close this window and return to sky10.")
		select {
		case server.codes <- callbackResult{code: code}:
		default:
		}
	})

	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case server.codes <- callbackResult{err: err}:
			default:
			}
		}
	}()

	return server, nil
}

func (s *callbackServer) close() {
	if s == nil || s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}

func writeOAuthPage(w http.ResponseWriter, status int, title string, body string) {
	accent := "#10a37f"
	accentBackground := "rgba(16, 163, 127, 0.10)"
	accentLabel := "ChatGPT linked"
	if status >= http.StatusBadRequest {
		accent = "#c2410c"
		accentBackground = "rgba(194, 65, 12, 0.10)"
		accentLabel = "Action needed"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 32px;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
	      background:
	        radial-gradient(circle at top, %s, transparent 32%%),
	        linear-gradient(180deg, #f7f7f8 0%%, #efeff1 100%%);
      color: #202123;
    }
    main {
      width: min(100%%, 560px);
      border-radius: 24px;
      background: rgba(255, 255, 255, 0.94);
      border: 1px solid rgba(32, 33, 35, 0.08);
      box-shadow:
        0 24px 64px rgba(15, 23, 42, 0.10),
        0 2px 12px rgba(15, 23, 42, 0.04);
      padding: 32px;
    }
    .eyebrow {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      margin: 0 0 18px;
      padding: 8px 12px;
      border-radius: 999px;
      background: %s;
      color: %s;
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.12em;
      text-transform: uppercase;
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 999px;
      background: %s;
      box-shadow: 0 0 0 6px %s;
    }
    h1 {
      margin: 0 0 12px;
      font-size: 30px;
      line-height: 1.1;
      letter-spacing: -0.03em;
    }
    p {
      margin: 0;
      line-height: 1.6;
      color: #5f6368;
      font-size: 15px;
    }
  </style>
</head>
<body>
  <main>
    <div class="eyebrow"><span class="dot"></span>%s</div>
    <h1>%s</h1>
    <p>%s</p>
  </main>
</body>
</html>`,
		htmlEscape(title),
		htmlEscape(accentBackground),
		htmlEscape(accentBackground),
		htmlEscape(accent),
		htmlEscape(accent),
		htmlEscape(accentBackground),
		htmlEscape(accentLabel),
		htmlEscape(title),
		htmlEscape(body),
	))
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func (s *Service) exchangeAuthorizationCode(ctx context.Context, code string, verifier string) (*storedCredential, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", s.oauth.ClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", s.oauth.RedirectURL)

	var token tokenResponse
	if err := s.submitTokenRequest(ctx, form, &token); err != nil {
		return nil, err
	}
	return newStoredCredentialFromToken(token, s.now()), nil
}

func (s *Service) refreshCredential(ctx context.Context, cred *storedCredential) (*storedCredential, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", cred.RefreshToken)
	form.Set("client_id", s.oauth.ClientID)

	var token tokenResponse
	if err := s.submitTokenRequest(ctx, form, &token); err != nil {
		return nil, err
	}
	next := newStoredCredentialFromToken(token, s.now())
	if next.RefreshToken == "" {
		next.RefreshToken = cred.RefreshToken
	}
	if next.Email == "" {
		next.Email = cred.Email
	}
	if next.AccountID == "" {
		next.AccountID = cred.AccountID
	}
	return next, nil
}

func (s *Service) submitTokenRequest(ctx context.Context, form url.Values, out *tokenResponse) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.oauth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build oauth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request oauth token: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read oauth token response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = res.Status
		}
		return fmt.Errorf("%s", message)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode oauth token response: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return fmt.Errorf("oauth token response missing access_token")
	}
	if out.ExpiresIn <= 0 {
		return fmt.Errorf("oauth token response missing expires_in")
	}
	return nil
}
