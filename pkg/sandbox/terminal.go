package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	skyapps "github.com/sky10/sky10/pkg/apps"
)

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func terminalOriginPatterns() []string {
	return []string{
		"localhost",
		"localhost:*",
		"127.0.0.1",
		"127.0.0.1:*",
		"[::1]",
		"[::1]:*",
		"*.localhost",
		"*.localhost:*",
	}
}

func (m *Manager) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		http.Error(w, "missing sandbox slug", http.StatusBadRequest)
		return
	}

	rec, err := m.requireRecord(slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if rec.Status != "ready" && rec.VMStatus != "Running" {
		http.Error(w, "sandbox terminal is available once the runtime is running", http.StatusConflict)
		return
	}

	args, err := m.terminalCommand(r.Context(), rec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: terminalOriginPatterns(),
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 36})
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("failed to start shell: %v\r\n", err)))
		_ = conn.Close(websocket.StatusInternalError, "failed to start shell")
		return
	}
	defer func() { _ = ptmx.Close() }()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	streamDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				writeErr := conn.Write(ctx, websocket.MessageBinary, buf[:n])
				if writeErr != nil {
					streamDone <- writeErr
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					streamDone <- nil
					return
				}
				streamDone <- err
				return
			}
		}
	}()

	inputDone := make(chan error, 1)
	inputMessages := make(chan terminalMessage, 16)
	go func() {
		defer close(inputMessages)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				inputDone <- err
				return
			}

			var msg terminalMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			inputMessages <- msg
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "")
			_ = killProcess(cmd.Process)
			return
		case err := <-streamDone:
			if err != nil {
				_ = killProcess(cmd.Process)
				_ = conn.Close(websocket.StatusInternalError, err.Error())
				return
			}
		case err := <-waitDone:
			status := websocket.StatusNormalClosure
			reason := ""
			if err != nil {
				status = websocket.StatusInternalError
				reason = err.Error()
			}
			_ = conn.Close(status, reason)
			return
		case err := <-inputDone:
			_ = killProcess(cmd.Process)
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return
			}
			return
		case msg, ok := <-inputMessages:
			if !ok {
				_ = killProcess(cmd.Process)
				return
			}
			switch msg.Type {
			case "input":
				if msg.Data != "" {
					_, _ = ptmx.Write([]byte(msg.Data))
				}
			case "resize":
				if msg.Cols == 0 || msg.Rows == 0 {
					continue
				}
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
			}
		}
	}
}

func (m *Manager) terminalCommand(ctx context.Context, rec *Record) ([]string, error) {
	if rec == nil {
		return nil, fmt.Errorf("sandbox is required")
	}

	switch rec.Provider {
	case providerLima:
		limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
		if err != nil {
			return nil, err
		}
		if isHermesTemplate(rec.Template) {
			return []string{limactl, "shell", rec.Slug, "--", "bash", "-lc", "hermes-shared"}, nil
		}
		return []string{limactl, "shell", rec.Slug}, nil
	default:
		return nil, fmt.Errorf("terminal access is not supported for provider %q", rec.Provider)
	}
}

func killProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
