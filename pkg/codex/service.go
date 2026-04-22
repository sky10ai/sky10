package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/logging"
)

type Emitter func(event string, data interface{})

type Status struct {
	Installed    bool          `json:"installed"`
	BinPath      string        `json:"bin_path,omitempty"`
	Linked       bool          `json:"linked"`
	AuthMode     string        `json:"auth_mode,omitempty"`
	AuthLabel    string        `json:"auth_label,omitempty"`
	AuthSource   string        `json:"auth_source,omitempty"`
	Email        string        `json:"email,omitempty"`
	AccountID    string        `json:"account_id,omitempty"`
	PendingLogin *PendingLogin `json:"pending_login,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatParams struct {
	Model        string        `json:"model,omitempty"`
	SystemPrompt string        `json:"system_prompt,omitempty"`
	Messages     []ChatMessage `json:"messages"`
}

type ChatUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

type ChatResult struct {
	Model      string     `json:"model"`
	ResponseID string     `json:"response_id,omitempty"`
	Text       string     `json:"text"`
	Usage      *ChatUsage `json:"usage,omitempty"`
}

type PendingLogin struct {
	ID                string    `json:"id"`
	Mode              string    `json:"mode,omitempty"`
	VerificationURL   string    `json:"verification_url"`
	RedirectURI       string    `json:"redirect_uri,omitempty"`
	CallbackListening bool      `json:"callback_listening,omitempty"`
	UserCode          string    `json:"user_code,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type CompleteLoginParams struct {
	AuthorizationInput string `json:"authorization_input"`
}

type Service struct {
	emit       Emitter
	logger     *slog.Logger
	now        func() time.Time
	httpClient *http.Client
	oauth      oauthConfig
	codexBase  string
	storePath  func() (string, error)
	findBinary func() (string, error)

	refreshMu sync.Mutex
	mu        sync.Mutex

	pending          *loginSession
	lastError        string
	nextRefreshAfter time.Time
}

type loginSession struct {
	info     PendingLogin
	verifier string
	state    string
	server   *callbackServer
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewService(emit Emitter) *Service {
	return &Service{
		emit: emit,
		logger: logging.WithComponent(
			slog.Default(),
			"codex",
		),
		now:        time.Now,
		httpClient: &http.Client{Timeout: 90 * time.Second},
		oauth:      defaultOAuthConfig(),
		codexBase:  defaultCodexBaseURL,
		storePath:  defaultStorePath,
		findBinary: defaultBinaryFinder,
	}
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	s.mu.Lock()
	s.expirePendingLocked()
	pending := clonePending(s.pending)
	lastError := s.lastError
	s.mu.Unlock()

	status := &Status{
		Installed:    true,
		PendingLogin: pending,
		LastError:    lastError,
	}

	cred, err := s.loadCredential()
	if err != nil {
		return nil, err
	}
	if cred != nil {
		refreshed, refreshErr := s.refreshCredentialIfNeeded(ctx, cred)
		if refreshErr == nil {
			cred = refreshed
		}

		status.AuthMode = "chatgpt"
		status.AuthLabel = "ChatGPT"
		status.AuthSource = "host_oauth"
		status.Email = cred.Email
		status.AccountID = cred.AccountID
		status.Linked = cred.isActive(s.now())
		s.mu.Lock()
		status.LastError = s.lastError
		s.mu.Unlock()
		return status, nil
	}

	legacyStatus, err := readLegacyCLIStatus(ctx, s.findBinary)
	if err != nil {
		return nil, err
	}
	if legacyStatus != nil {
		status.BinPath = legacyStatus.BinPath
		if legacyStatus.Linked {
			status.Linked = true
			status.AuthMode = legacyStatus.AuthMode
			status.AuthLabel = legacyStatus.AuthLabel
			status.AuthSource = "cli_managed"
		}
	}

	return status, nil
}

func (s *Service) StartLogin(ctx context.Context) (*Status, error) {
	s.mu.Lock()
	s.expirePendingLocked()
	if s.pending != nil {
		s.mu.Unlock()
		return s.Status(ctx)
	}
	s.lastError = ""
	s.nextRefreshAfter = time.Time{}
	s.mu.Unlock()

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating pkce verifier: %w", err)
	}
	state, err := generateOAuthState()
	if err != nil {
		return nil, fmt.Errorf("generating oauth state: %w", err)
	}

	loginCtx, cancel := context.WithCancel(context.Background())
	session := &loginSession{
		info: PendingLogin{
			ID:              randomID(),
			Mode:            "oauth",
			VerificationURL: buildAuthorizeURL(s.oauth, challenge, state),
			RedirectURI:     s.oauth.RedirectURL,
			StartedAt:       s.now().UTC(),
			ExpiresAt:       s.now().UTC().Add(15 * time.Minute),
		},
		verifier: verifier,
		state:    state,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	server, err := startLocalOAuthServer(s.oauth.RedirectURL, state)
	if err == nil {
		session.server = server
		session.info.CallbackListening = true
		go s.awaitCallback(loginCtx, session)
	} else {
		s.logger.Warn("failed to bind codex oauth callback listener; manual completion required", "error", err)
	}

	s.mu.Lock()
	s.pending = session
	s.mu.Unlock()
	s.emitStatus("")
	return s.Status(ctx)
}

func (s *Service) CompleteLogin(ctx context.Context, params CompleteLoginParams) (*Status, error) {
	input := params.AuthorizationInput
	if input == "" {
		return nil, fmt.Errorf("authorization_input is required")
	}

	s.mu.Lock()
	session := s.pending
	s.expirePendingLocked()
	if session == nil || session != s.pending {
		s.mu.Unlock()
		return nil, fmt.Errorf("no ChatGPT login is in progress")
	}
	s.mu.Unlock()

	code, state, err := parseAuthorizationInput(input)
	if err != nil {
		s.setLastError(err.Error())
		return nil, err
	}
	if state != "" && state != session.state {
		err := fmt.Errorf("oauth state mismatch")
		s.setLastError(err.Error())
		return nil, err
	}
	if code == "" {
		err := fmt.Errorf("missing authorization code")
		s.setLastError(err.Error())
		return nil, err
	}

	return s.completeLoginWithCode(ctx, session, code)
}

func (s *Service) CancelLogin(ctx context.Context) (*Status, error) {
	s.mu.Lock()
	session := s.pending
	if session != nil {
		s.pending = nil
	}
	s.lastError = ""
	s.mu.Unlock()

	if session != nil {
		session.cancel()
		if session.server != nil {
			session.server.close()
		}
		<-session.done
	}

	s.emitStatus("")
	return s.Status(ctx)
}

func (s *Service) Logout(ctx context.Context) (*Status, error) {
	s.mu.Lock()
	session := s.pending
	if session != nil {
		s.pending = nil
	}
	s.lastError = ""
	s.nextRefreshAfter = time.Time{}
	s.mu.Unlock()

	if session != nil {
		session.cancel()
		if session.server != nil {
			session.server.close()
		}
		<-session.done
	}

	if err := s.clearCredential(); err != nil {
		return nil, err
	}

	legacyStatus, err := readLegacyCLIStatus(ctx, s.findBinary)
	if err != nil {
		return nil, err
	}
	if legacyStatus != nil && legacyStatus.Linked {
		if err := logoutLegacyCLI(ctx, legacyStatus.BinPath); err != nil {
			return nil, err
		}
	}

	s.emitStatus("")
	return s.Status(ctx)
}

func (s *Service) Chat(ctx context.Context, params ChatParams) (*ChatResult, error) {
	cred, err := s.activeCredential(ctx)
	if err != nil {
		return nil, err
	}

	messages, err := buildChatInput(params.Messages)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(params.Model)
	if model == "" {
		model = defaultCodexChatModel
	}

	accountID := strings.TrimSpace(cred.AccountID)
	if accountID == "" {
		accountID = accountIDFromClaims(decodeCodexJWTClaims(cred.AccessToken))
	}
	if accountID == "" {
		return nil, fmt.Errorf("linked ChatGPT account is missing a Codex account id")
	}

	body := map[string]interface{}{
		"model":   model,
		"store":   false,
		"stream":  true,
		"input":   messages,
		"text":    map[string]string{"verbosity": "medium"},
		"include": []string{"reasoning.encrypted_content"},
	}
	body["instructions"] = defaultCodexInstructions
	if prompt := strings.TrimSpace(params.SystemPrompt); prompt != "" {
		body["instructions"] = prompt
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode codex chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveCodexURL(s.codexBase), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build codex chat request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("originator", s.oauth.Originator)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())

	res, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request codex chat: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil {
			return nil, fmt.Errorf("read codex chat error response: %w", err)
		}
		return nil, parseCodexAPIError(res.StatusCode, raw)
	}
	return parseCodexAPIStream(io.LimitReader(res.Body, 4<<20), model)
}

func (s *Service) activeCredential(ctx context.Context) (*storedCredential, error) {
	cred, err := s.loadCredential()
	if err != nil {
		return nil, err
	}
	if cred == nil {
		legacyStatus, legacyErr := readLegacyCLIStatus(ctx, s.findBinary)
		if legacyErr != nil {
			return nil, legacyErr
		}
		if legacyStatus != nil && legacyStatus.Linked {
			return nil, fmt.Errorf("this device is linked through the Codex CLI; reconnect in sky10 to use /codex chat")
		}
		return nil, fmt.Errorf("no ChatGPT Codex account is linked in sky10")
	}

	refreshed, refreshErr := s.refreshCredentialIfNeeded(ctx, cred)
	if refreshErr == nil {
		cred = refreshed
	}
	if !cred.isActive(s.now()) {
		if refreshErr != nil {
			return nil, refreshErr
		}
		return nil, fmt.Errorf("linked ChatGPT Codex credential is not active")
	}

	return cred, nil
}

func (s *Service) awaitCallback(ctx context.Context, session *loginSession) {
	defer close(session.done)
	if session.server == nil {
		return
	}

	select {
	case result := <-session.server.codes:
		if result.err != nil {
			s.setLastError(result.err.Error())
			s.emitStatus("")
			return
		}
		if _, err := s.completeLoginWithCode(context.Background(), session, result.code); err != nil {
			s.logger.Warn("failed to complete codex oauth callback", "error", err)
		}
	case <-ctx.Done():
		return
	}
}

func (s *Service) completeLoginWithCode(ctx context.Context, session *loginSession, code string) (*Status, error) {
	s.mu.Lock()
	if s.pending != session {
		s.mu.Unlock()
		return nil, fmt.Errorf("oauth login session is no longer active")
	}
	s.mu.Unlock()

	cred, err := s.exchangeAuthorizationCode(ctx, code, session.verifier)
	if err != nil {
		err = fmt.Errorf("OpenAI Codex OAuth failed: %w", err)
		s.setLastError(err.Error())
		s.emitStatus("")
		return nil, err
	}

	if err := s.saveCredential(cred); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.pending == session {
		s.pending = nil
	}
	s.lastError = ""
	s.nextRefreshAfter = time.Time{}
	s.mu.Unlock()

	if session.server != nil {
		session.server.close()
	}
	session.cancel()
	s.emitStatus("")
	return s.Status(ctx)
}

func (s *Service) refreshCredentialIfNeeded(ctx context.Context, cred *storedCredential) (*storedCredential, error) {
	if !cred.needsRefresh(s.now()) {
		return cred, nil
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	reloaded, err := s.loadCredential()
	if err != nil {
		return nil, err
	}
	if reloaded == nil {
		return nil, fmt.Errorf("stored credential disappeared during refresh")
	}
	if !reloaded.needsRefresh(s.now()) {
		return reloaded, nil
	}

	s.mu.Lock()
	lastError := s.lastError
	nextRefreshAfter := s.nextRefreshAfter
	s.mu.Unlock()
	if !nextRefreshAfter.IsZero() && s.now().Before(nextRefreshAfter) {
		if lastError == "" {
			lastError = "OpenAI Codex token refresh is temporarily throttled"
		}
		return reloaded, errors.New(lastError)
	}

	refreshed, err := s.refreshCredential(ctx, reloaded)
	if err != nil {
		message := fmt.Sprintf("OpenAI Codex token refresh failed: %v", err)
		s.mu.Lock()
		s.lastError = message
		s.nextRefreshAfter = s.now().Add(time.Minute)
		s.mu.Unlock()
		return reloaded, errors.New(message)
	}

	if err := s.saveCredential(refreshed); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.lastError = ""
	s.nextRefreshAfter = time.Time{}
	s.mu.Unlock()
	return refreshed, nil
}

func (s *Service) expirePendingLocked() {
	if s.pending == nil {
		return
	}
	if s.now().UTC().Before(s.pending.info.ExpiresAt) {
		return
	}
	session := s.pending
	s.pending = nil
	s.lastError = "ChatGPT login expired before completion"
	if session.server != nil {
		session.server.close()
	}
	session.cancel()
}

func (s *Service) emitStatus(binPath string) {
	if s.emit == nil {
		return
	}
	status, err := s.Status(context.Background())
	if err != nil {
		s.logger.Warn("failed to emit codex status", "error", err)
		return
	}
	if binPath != "" && status.BinPath == "" {
		status.BinPath = binPath
	}
	s.emit("codex:login:updated", status)
}

func (s *Service) setLastError(message string) {
	s.mu.Lock()
	s.lastError = message
	s.mu.Unlock()
}

func clonePending(session *loginSession) *PendingLogin {
	if session == nil {
		return nil
	}
	pending := session.info
	return &pending
}
