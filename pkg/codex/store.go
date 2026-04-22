package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	skyconfig "github.com/sky10/sky10/pkg/config"
)

const credentialVersion = 1

var errCodexNotInstalled = errors.New("codex cli not installed")

type storedCredential struct {
	Version      int       `json:"version"`
	Type         string    `json:"type"`
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type codexJWTAuthClaims struct {
	ChatGPTAccountID     string `json:"chatgpt_account_id"`
	ChatGPTAccountUserID string `json:"chatgpt_account_user_id"`
	ChatGPTUserID        string `json:"chatgpt_user_id"`
	ChatGPTPlanType      string `json:"chatgpt_plan_type"`
	UserID               string `json:"user_id"`
}

type codexJWTProfileClaims struct {
	Email string `json:"email"`
}

type codexJWTClaims struct {
	Exp              int64                 `json:"exp"`
	Sub              string                `json:"sub"`
	Email            string                `json:"email"`
	ProfileEmail     string                `json:"https://api.openai.com/profile.email"`
	ChatGPTAccountID string                `json:"https://api.openai.com/auth.chatgpt_account_id"`
	Auth             codexJWTAuthClaims    `json:"https://api.openai.com/auth"`
	Profile          codexJWTProfileClaims `json:"https://api.openai.com/profile"`
}

func defaultStorePath() (string, error) {
	root, err := skyconfig.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "codex", "auth.json"), nil
}

func (s *Service) loadCredential() (*storedCredential, error) {
	path, err := s.storePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading codex auth store: %w", err)
	}
	var cred storedCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("decoding codex auth store: %w", err)
	}
	if cred.Version == 0 {
		cred.Version = credentialVersion
	}
	hydrateCredentialMetadata(&cred)
	return &cred, nil
}

func (s *Service) saveCredential(cred *storedCredential) error {
	path, err := s.storePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating codex auth directory: %w", err)
	}
	payload, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding codex credential: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "auth-*.json")
	if err != nil {
		return fmt.Errorf("creating temp codex auth file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temp codex auth file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp codex auth file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp codex auth file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp codex auth file: %w", err)
	}
	return nil
}

func (s *Service) clearCredential() error {
	path, err := s.storePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing codex auth store: %w", err)
	}
	return nil
}

func newStoredCredentialFromToken(token tokenResponse, now time.Time) *storedCredential {
	accessClaims := decodeCodexJWTClaims(token.AccessToken)
	idClaims := decodeCodexJWTClaims(token.IDToken)

	accountID := firstNonEmpty(
		accountIDFromClaims(accessClaims),
		accountIDFromClaims(idClaims),
	)
	email := firstNonEmpty(
		emailFromClaims(accessClaims),
		emailFromClaims(idClaims),
	)
	expiresAt := now.UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	if accessClaims != nil && accessClaims.Exp > 0 {
		expiresAt = time.Unix(accessClaims.Exp, 0).UTC()
	}
	return &storedCredential{
		Version:      credentialVersion,
		Type:         "oauth",
		Provider:     "openai-codex",
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		IDToken:      token.IDToken,
		ExpiresAt:    expiresAt,
		AccountID:    accountID,
		Email:        email,
		UpdatedAt:    now.UTC(),
	}
}

func hydrateCredentialMetadata(cred *storedCredential) {
	if cred == nil {
		return
	}

	accessClaims := decodeCodexJWTClaims(cred.AccessToken)
	idClaims := decodeCodexJWTClaims(cred.IDToken)

	if strings.TrimSpace(cred.AccountID) == "" {
		cred.AccountID = firstNonEmpty(
			accountIDFromClaims(accessClaims),
			accountIDFromClaims(idClaims),
		)
	}
	if strings.TrimSpace(cred.Email) == "" {
		cred.Email = firstNonEmpty(
			emailFromClaims(accessClaims),
			emailFromClaims(idClaims),
		)
	}
}

func (c *storedCredential) needsRefresh(now time.Time) bool {
	if c == nil {
		return false
	}
	return now.UTC().Add(time.Minute).After(c.ExpiresAt.UTC())
}

func (c *storedCredential) isActive(now time.Time) bool {
	if c == nil {
		return false
	}
	return now.UTC().Before(c.ExpiresAt.UTC())
}

func decodeCodexJWTClaims(accessToken string) *codexJWTClaims {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims codexJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return &claims
}

func accountIDFromClaims(claims *codexJWTClaims) string {
	if claims == nil {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(claims.ChatGPTAccountID),
		strings.TrimSpace(claims.Auth.ChatGPTAccountID),
		strings.TrimSpace(claims.Auth.UserID),
		strings.TrimSpace(claims.Auth.ChatGPTUserID),
	)
}

func emailFromClaims(claims *codexJWTClaims) string {
	if claims == nil {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(claims.ProfileEmail),
		strings.TrimSpace(claims.Profile.Email),
		strings.TrimSpace(claims.Email),
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func readLegacyCLIStatus(ctx context.Context, findBinary func() (string, error)) (*Status, error) {
	if findBinary == nil {
		findBinary = defaultBinaryFinder
	}
	binPath, err := findBinary()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errCodexNotInstalled) {
			return nil, nil
		}
		return nil, err
	}

	output, err := exec.CommandContext(ctx, binPath, "login", "status").CombinedOutput()
	if err != nil {
		clean := sanitizeOutput(string(output))
		if strings.Contains(clean, "Not logged in") {
			return &Status{Installed: true, BinPath: binPath}, nil
		}
		return nil, fmt.Errorf("checking codex login status: %w: %s", err, clean)
	}

	mode, label := parseStatusOutput(string(output))
	if mode == "" {
		return &Status{Installed: true, BinPath: binPath}, nil
	}

	return &Status{
		Installed:  true,
		BinPath:    binPath,
		Linked:     true,
		AuthMode:   mode,
		AuthLabel:  label,
		AuthSource: "cli_managed",
	}, nil
}

func logoutLegacyCLI(ctx context.Context, binPath string) error {
	output, err := exec.CommandContext(ctx, binPath, "logout").CombinedOutput()
	if err != nil {
		return fmt.Errorf("logging out of codex cli: %w: %s", err, sanitizeOutput(string(output)))
	}
	return nil
}

func parseStatusOutput(output string) (mode string, label string) {
	clean := sanitizeOutput(output)
	switch {
	case strings.Contains(strings.ToLower(clean), "logged in using chatgpt"):
		return "chatgpt", "ChatGPT"
	case strings.Contains(strings.ToLower(clean), "logged in using api key"):
		return "apikey", "API key"
	default:
		return "", ""
	}
}

func sanitizeOutput(output string) string {
	return strings.TrimSpace(output)
}

func defaultBinaryFinder() (string, error) {
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}

	home, _ := os.UserHomeDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}

	candidates := []string{}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", name),
			filepath.Join(home, ".bin", name),
		)
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			filepath.Join(string(filepath.Separator), "opt", "homebrew", "bin", name),
			filepath.Join(string(filepath.Separator), "usr", "local", "bin", name),
		)
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
