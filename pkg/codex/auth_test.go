package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStatusOutput(t *testing.T) {
	t.Run("chatgpt", func(t *testing.T) {
		mode, label := parseStatusOutput("Logged in using ChatGPT\n")
		if mode != "chatgpt" {
			t.Fatalf("mode = %q, want chatgpt", mode)
		}
		if label != "ChatGPT" {
			t.Fatalf("label = %q, want ChatGPT", label)
		}
	})

	t.Run("api key", func(t *testing.T) {
		mode, label := parseStatusOutput("Logged in using API key\n")
		if mode != "apikey" {
			t.Fatalf("mode = %q, want apikey", mode)
		}
		if label != "API key" {
			t.Fatalf("label = %q, want API key", label)
		}
	})
}

func TestServiceStartLoginAndLogout(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	bin := writeFakeCodex(t, dir, stateDir)

	service := NewService(nil)
	service.findBinary = func() (string, error) { return bin, nil }

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status before login: %v", err)
	}
	if status.Linked {
		t.Fatalf("Status before login linked = true, want false")
	}

	started, err := service.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if started.PendingLogin == nil {
		t.Fatalf("StartLogin pending login = nil")
	}
	if started.PendingLogin.VerificationURL != "https://auth.openai.com/codex/device" {
		t.Fatalf("verification url = %q", started.PendingLogin.VerificationURL)
	}
	if started.PendingLogin.UserCode != "ABCD-EFG12" {
		t.Fatalf("user code = %q", started.PendingLogin.UserCode)
	}

	if err := os.WriteFile(filepath.Join(stateDir, "finish"), []byte("done"), 0644); err != nil {
		t.Fatalf("write finish file: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		current, err := service.Status(context.Background())
		if err != nil {
			return false
		}
		return current.Linked && current.AuthMode == "chatgpt" && current.PendingLogin == nil
	})

	loggedIn, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after login: %v", err)
	}
	if !loggedIn.Linked {
		t.Fatalf("Status after login linked = false, want true")
	}

	loggedOut, err := service.Logout(context.Background())
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if loggedOut.Linked {
		t.Fatalf("Status after logout linked = true, want false")
	}
}

func TestServiceCancelLogin(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	bin := writeFakeCodex(t, dir, stateDir)

	service := NewService(nil)
	service.findBinary = func() (string, error) { return bin, nil }

	started, err := service.StartLogin(context.Background())
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if started.PendingLogin == nil {
		t.Fatalf("StartLogin pending login = nil")
	}

	cancelled, err := service.CancelLogin(context.Background())
	if err != nil {
		t.Fatalf("CancelLogin: %v", err)
	}
	if cancelled.PendingLogin != nil {
		t.Fatalf("CancelLogin pending login = %+v, want nil", cancelled.PendingLogin)
	}
	if cancelled.Linked {
		t.Fatalf("CancelLogin linked = true, want false")
	}
	if cancelled.LastError != "" {
		t.Fatalf("CancelLogin last error = %q, want empty", cancelled.LastError)
	}
}

func writeFakeCodex(t *testing.T, dir, stateDir string) string {
	t.Helper()
	script := filepath.Join(dir, "codex")
	content := strings.ReplaceAll(`#!/bin/sh
set -eu
STATE_DIR="{{STATE_DIR}}"

if [ "${1:-}" = "login" ] && [ "${2:-}" = "status" ]; then
  if [ -f "$STATE_DIR/logged_in" ]; then
    cat "$STATE_DIR/logged_in"
    exit 0
  fi
  echo "Not logged in"
  exit 1
fi

if [ "${1:-}" = "login" ] && [ "${2:-}" = "--device-auth" ]; then
  echo "Follow these steps to sign in with ChatGPT using device code authorization:"
  echo "https://auth.openai.com/codex/device"
  echo "ABCD-EFG12"
  while [ ! -f "$STATE_DIR/finish" ]; do
    sleep 0.05
  done
  printf 'Logged in using ChatGPT\n' > "$STATE_DIR/logged_in"
  exit 0
fi

if [ "${1:-}" = "logout" ]; then
  rm -f "$STATE_DIR/logged_in"
  echo "Not logged in"
  exit 0
fi

echo "unexpected args: $*" >&2
exit 99
`, "{{STATE_DIR}}", stateDir)
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	return script
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
