package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	PendingLogin *PendingLogin `json:"pending_login,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
}

type PendingLogin struct {
	ID              string    `json:"id"`
	VerificationURL string    `json:"verification_url"`
	UserCode        string    `json:"user_code"`
	StartedAt       time.Time `json:"started_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Service struct {
	emit       Emitter
	logger     *slog.Logger
	findBinary func() (string, error)
	now        func() time.Time

	mu        sync.Mutex
	pending   *loginSession
	lastError string
}

type loginSession struct {
	info      PendingLogin
	cancel    context.CancelFunc
	cancelled bool
	ready     chan struct{}
	done      chan struct{}
}

var (
	errCodexNotInstalled   = errors.New("codex cli not installed")
	ansiPattern            = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	verificationURLPattern = regexp.MustCompile(`https://auth\.openai\.com/codex/device`)
	userCodePattern        = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{5}\b`)
	loggedInPattern        = regexp.MustCompile(`Logged in using (.+)`)
)

func NewService(emit Emitter) *Service {
	return &Service{
		emit:       emit,
		logger:     logging.WithComponent(slog.Default(), "codex"),
		findBinary: defaultBinaryFinder,
		now:        time.Now,
	}
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	binPath, err := s.findBinary()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errCodexNotInstalled) {
			return s.snapshot("", nil), nil
		}
		return nil, err
	}

	result := &Status{
		Installed: true,
		BinPath:   binPath,
	}

	output, err := exec.CommandContext(ctx, binPath, "login", "status").CombinedOutput()
	if err != nil {
		clean := sanitizeOutput(string(output))
		if strings.Contains(clean, "Not logged in") {
			return s.snapshot(binPath, result), nil
		}
		return nil, fmt.Errorf("checking codex login status: %w: %s", err, clean)
	}

	mode, label := parseStatusOutput(string(output))
	result.Linked = mode != ""
	result.AuthMode = mode
	result.AuthLabel = label
	return s.snapshot(binPath, result), nil
}

func (s *Service) StartLogin(ctx context.Context) (*Status, error) {
	binPath, err := s.findBinary()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errCodexNotInstalled) {
			return nil, errCodexNotInstalled
		}
		return nil, err
	}

	s.mu.Lock()
	if s.pending != nil {
		status := s.snapshotLocked(binPath, &Status{
			Installed: true,
			BinPath:   binPath,
		})
		s.mu.Unlock()
		return status, nil
	}
	s.lastError = ""
	s.mu.Unlock()

	loginCtx, cancel := context.WithCancel(context.Background())
	session := &loginSession{
		info: PendingLogin{
			ID:        randomID(),
			StartedAt: s.now().UTC(),
			ExpiresAt: s.now().UTC().Add(15 * time.Minute),
		},
		cancel: cancel,
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
	}

	cmd := exec.CommandContext(loginCtx, binPath, "login", "--device-auth")
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cmd.Stderr = writer

	if err := cmd.Start(); err != nil {
		cancel()
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("starting codex login: %w", err)
	}

	s.mu.Lock()
	s.pending = session
	s.mu.Unlock()
	go s.emitStatus(binPath)

	go s.monitorLogin(binPath, session, cmd, reader, writer)

	select {
	case <-session.ready:
		return s.Status(ctx)
	case <-session.done:
		status, statusErr := s.Status(ctx)
		if statusErr != nil {
			return nil, statusErr
		}
		if status.LastError != "" {
			return status, errors.New(status.LastError)
		}
		return status, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		cancel()
		s.clearPending(session, "timed out waiting for codex device code")
		return nil, fmt.Errorf("timed out waiting for codex device code")
	}
}

func (s *Service) CancelLogin(ctx context.Context) (*Status, error) {
	_, err := s.findBinary()
	if err != nil && !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, errCodexNotInstalled) {
		return nil, err
	}

	s.mu.Lock()
	session := s.pending
	if session != nil {
		session.cancelled = true
	}
	s.mu.Unlock()
	if session != nil {
		session.cancel()
		<-session.done
	}
	return s.Status(ctx)
}

func (s *Service) Logout(ctx context.Context) (*Status, error) {
	binPath, err := s.findBinary()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errCodexNotInstalled) {
			return s.snapshot("", nil), nil
		}
		return nil, err
	}

	s.mu.Lock()
	session := s.pending
	if session != nil {
		session.cancelled = true
	}
	s.mu.Unlock()
	if session != nil {
		session.cancel()
		<-session.done
	}

	output, err := exec.CommandContext(ctx, binPath, "logout").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("logging out of codex: %w: %s", err, sanitizeOutput(string(output)))
	}

	s.mu.Lock()
	s.lastError = ""
	s.mu.Unlock()
	s.emitStatus(binPath)
	return s.Status(ctx)
}

func (s *Service) monitorLogin(binPath string, session *loginSession, cmd *exec.Cmd, reader *io.PipeReader, writer *io.PipeWriter) {
	defer close(session.done)
	defer reader.Close()

	waitCh := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		_ = writer.Close()
		waitCh <- waitErr
	}()

	scanner := bufio.NewScanner(reader)
	var output strings.Builder
	readySent := false

	for scanner.Scan() {
		line := sanitizeOutput(scanner.Text())
		if line == "" {
			continue
		}
		output.WriteString(line)
		output.WriteByte('\n')

		url, code := parseDeviceAuthOutput(output.String())
		if url == "" || code == "" || readySent {
			continue
		}

		s.mu.Lock()
		if s.pending == session {
			s.pending.info.VerificationURL = url
			s.pending.info.UserCode = code
		}
		s.mu.Unlock()

		readySent = true
		close(session.ready)
		s.emitStatus(binPath)
	}

	waitErr := <-waitCh
	scanErr := scanner.Err()
	if scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	if errors.Is(waitErr, context.Canceled) || s.isCancelled(session) {
		waitErr = nil
	}
	if waitErr != nil {
		s.clearPending(session, output.String())
		return
	}

	s.mu.Lock()
	if s.pending != session {
		s.mu.Unlock()
		return
	}
	s.pending = nil
	s.lastError = ""
	s.mu.Unlock()
	s.emitStatus(binPath)
}

func (s *Service) clearPending(session *loginSession, rawOutput string) {
	message := strings.TrimSpace(sanitizeOutput(rawOutput))
	if message == "" {
		message = "codex login failed"
	}

	s.mu.Lock()
	if s.pending == session {
		s.pending = nil
	}
	s.lastError = message
	s.mu.Unlock()

	binPath, err := s.findBinary()
	if err == nil {
		s.emitStatus(binPath)
	}
}

func (s *Service) snapshot(binPath string, base *Status) *Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(binPath, base)
}

func (s *Service) isCancelled(session *loginSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == session {
		return s.pending.cancelled
	}
	return session.cancelled
}

func (s *Service) snapshotLocked(binPath string, base *Status) *Status {
	status := &Status{}
	if base != nil {
		*status = *base
	}
	if binPath != "" {
		status.Installed = true
		status.BinPath = binPath
	}
	if s.pending != nil {
		pending := s.pending.info
		status.PendingLogin = &pending
	}
	status.LastError = s.lastError
	return status
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
		status.Installed = true
	}
	s.emit("codex:login:updated", status)
}

func parseStatusOutput(output string) (mode string, label string) {
	clean := sanitizeOutput(output)
	match := loggedInPattern.FindStringSubmatch(clean)
	if len(match) != 2 {
		return "", ""
	}
	label = strings.TrimSpace(match[1])
	normalized := strings.ToLower(label)
	switch {
	case strings.Contains(normalized, "chatgpt"):
		mode = "chatgpt"
	case strings.Contains(normalized, "api key"):
		mode = "apikey"
	default:
		mode = "unknown"
	}
	return mode, label
}

func parseDeviceAuthOutput(output string) (url string, code string) {
	clean := sanitizeOutput(output)
	url = verificationURLPattern.FindString(clean)
	code = userCodePattern.FindString(clean)
	return url, code
}

func sanitizeOutput(output string) string {
	clean := ansiPattern.ReplaceAllString(output, "")
	return strings.TrimSpace(clean)
}

func randomID() string {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("codex-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
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
