package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// ProcessSpec describes one adapter executable invocation.
type ProcessSpec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// Validate reports whether a process spec can be started.
func (s ProcessSpec) Validate() error {
	if s.Path == "" {
		return fmt.Errorf("process path is required")
	}
	return nil
}

// ProcessHost supervises one adapter child process connected over stdio.
type ProcessHost struct {
	client *Client
	cmd    *exec.Cmd
	cancel context.CancelFunc

	stderr *lockedBuffer

	waitDone   chan struct{}
	stderrDone chan struct{}

	mu             sync.Mutex
	waitErr        error
	closeRequested bool
}

// StartProcess launches one adapter child process and wires its stdio transport.
func StartProcess(ctx context.Context, spec ProcessSpec, notify NotificationHandler) (*ProcessHost, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, spec.Path, spec.Args...)
	if len(spec.Env) > 0 {
		cmd.Env = append(cmd.Environ(), spec.Env...)
	}
	cmd.Dir = spec.Dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	host := &ProcessHost{
		cmd:        cmd,
		cancel:     cancel,
		stderr:     newLockedBuffer(64 << 10),
		waitDone:   make(chan struct{}),
		stderrDone: make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start process %s: %w", spec.Path, err)
	}

	host.client = NewClient(stdout, stdin, notify)
	go host.captureStderr(stderr)
	go host.waitLoop()
	return host, nil
}

// PID returns the current process identifier or zero when unavailable.
func (h *ProcessHost) PID() int {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// Call proxies one JSON-RPC request to the child process.
func (h *ProcessHost) Call(ctx context.Context, method string, params any, out any) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("process host is not running")
	}
	return h.client.Call(ctx, method, params, out)
}

// Stderr returns the buffered child-process stderr output.
func (h *ProcessHost) Stderr() string {
	if h == nil || h.stderr == nil {
		return ""
	}
	return h.stderr.String()
}

// Wait waits for the process and transport to exit.
func (h *ProcessHost) Wait() error {
	if h == nil {
		return nil
	}
	<-h.waitDone
	<-h.stderrDone
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.waitErr
}

// Done closes when the process wait loop exits.
func (h *ProcessHost) Done() <-chan struct{} {
	if h == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return h.waitDone
}

// Close requests process shutdown and waits for completion.
func (h *ProcessHost) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.closeRequested = true
	h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
	if h.client != nil {
		_ = h.client.Close()
	}
	return h.Wait()
}

func (h *ProcessHost) waitLoop() {
	defer close(h.waitDone)

	err := h.cmd.Wait()
	if clientErr := h.client.Err(); clientErr != nil &&
		!errors.Is(clientErr, io.EOF) &&
		!errors.Is(clientErr, io.ErrClosedPipe) {
		if err == nil {
			err = clientErr
		} else {
			err = fmt.Errorf("%w; transport: %v", err, clientErr)
		}
	}

	h.mu.Lock()
	closeRequested := h.closeRequested
	if closeRequested {
		err = nil
	}
	h.waitErr = err
	h.mu.Unlock()
}

func (h *ProcessHost) captureStderr(r io.Reader) {
	defer close(h.stderrDone)
	_, _ = io.Copy(h.stderr, r)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func newLockedBuffer(maxBytes int) *lockedBuffer {
	return &lockedBuffer{max: maxBytes}
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.max > 0 && len(p) >= b.max {
		b.buf.Reset()
		_, _ = b.buf.Write(p[len(p)-b.max:])
		return n, nil
	}
	if b.max > 0 && b.buf.Len()+len(p) > b.max {
		drop := b.buf.Len() + len(p) - b.max
		remaining := b.buf.Bytes()
		if drop < len(remaining) {
			var trimmed bytes.Buffer
			_, _ = trimmed.Write(remaining[drop:])
			b.buf = trimmed
		} else {
			b.buf.Reset()
		}
	}
	_, err := b.buf.Write(p)
	return n, err
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
