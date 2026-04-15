package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

func TestTerminalCommandHermesLaunchesHermesShared(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/tmp/fake/limactl"}, nil
	}

	args, err := m.terminalCommand(context.Background(), &Record{
		Provider: providerLima,
		Template: templateHermes,
		Slug:     "hermes-dev",
	})
	if err != nil {
		t.Fatalf("terminalCommand() error: %v", err)
	}

	want := []string{
		"/tmp/fake/limactl",
		"shell",
		"hermes-dev",
		"--",
		"bash",
		"-lc",
		"hermes-shared",
	}
	if len(args) != len(want) {
		t.Fatalf("terminalCommand() len = %d, want %d (%q)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("terminalCommand()[%d] = %q, want %q (%q)", i, args[i], want[i], args)
		}
	}
}

func TestLoadUsesTemplateSpecificShellCommand(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	body, err := json.Marshal(stateFile{
		Sandboxes: []Record{
			{
				Name:      "Hermes Dev",
				Slug:      "hermes-dev",
				Provider:  providerLima,
				Template:  templateHermes,
				Shell:     "limactl shell hermes-dev",
				CreatedAt: "2026-04-15T00:00:00Z",
				UpdatedAt: "2026-04-15T00:00:00Z",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if err := os.WriteFile(m.statePath(), body, 0o644); err != nil {
		t.Fatalf("WriteFile(state) error: %v", err)
	}

	m.records = map[string]Record{}
	if err := m.load(); err != nil {
		t.Fatalf("load() error: %v", err)
	}

	got, err := m.Get(context.Background(), "hermes-dev")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	want := defaultShellCommand("hermes-dev", templateHermes)
	if got.Shell != want {
		t.Fatalf("shell = %q, want %q", got.Shell, want)
	}
}

func TestHandleTerminalAllowsTauriOrigin(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}
	m.appStatus = func(id skyapps.ID) (*skyapps.Status, error) {
		return &skyapps.Status{ActivePath: "/usr/bin/true"}, nil
	}

	rec := Record{
		Name:      "Hermes Dev",
		Slug:      "hermes-dev",
		Provider:  providerLima,
		Template:  templateHermes,
		Status:    "ready",
		VMStatus:  "Running",
		CreatedAt: "2026-04-15T00:00:00Z",
		UpdatedAt: "2026-04-15T00:00:00Z",
	}
	m.records[rec.Slug] = rec

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc/sandboxes/{slug}/terminal", m.HandleTerminal)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	statusLine := websocketHandshakeStatusLine(t, srv.Listener.Addr().String(), "/rpc/sandboxes/hermes-dev/terminal", "tauri://localhost")
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("status line = %q, want WebSocket upgrade", statusLine)
	}
}

func websocketHandshakeStatusLine(t *testing.T, addr, path, origin string) string {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("net.Dial() error: %v", err)
	}
	defer conn.Close()

	req := strings.Join([]string{
		fmt.Sprintf("GET %s HTTP/1.1", path),
		fmt.Sprintf("Host: %s", addr),
		"Upgrade: websocket",
		"Connection: Upgrade",
		fmt.Sprintf("Origin: %s", origin),
		"Sec-WebSocket-Key: dGVzdGluZy10ZXJtaW5hbA==",
		"Sec-WebSocket-Version: 13",
		"",
		"",
	}, "\r\n")
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("conn.Write() error: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("ReadString() error: %v", err)
	}
	return strings.TrimSpace(line)
}
