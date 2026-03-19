package fs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// LogBuffer is a ring buffer that captures slog output for debug dumps.
type LogBuffer struct {
	mu      sync.Mutex
	entries []string
	max     int
}

// NewLogBuffer creates a buffer that keeps the last max log lines.
func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{
		entries: make([]string, 0, max),
		max:     max,
	}
}

// Lines returns a copy of all captured log lines.
func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.entries))
	copy(out, b.entries)
	return out
}

func (b *LogBuffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= b.max {
		// Shift left by 1
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = line
	} else {
		b.entries = append(b.entries, line)
	}
}

// LogBufferHandler is a slog.Handler that writes to a LogBuffer and
// forwards to a wrapped handler (e.g. the default stderr handler).
type LogBufferHandler struct {
	buf   *LogBuffer
	inner slog.Handler
}

// NewLogBufferHandler wraps an existing handler and tees output to the buffer.
func NewLogBufferHandler(buf *LogBuffer, inner slog.Handler) *LogBufferHandler {
	return &LogBufferHandler{buf: buf, inner: inner}
}

func (h *LogBufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *LogBufferHandler) Handle(ctx context.Context, r slog.Record) error {
	// Format: "2006-01-02T15:04:05 LEVEL msg key=val key=val"
	ts := r.Time.Format("15:04:05")
	line := fmt.Sprintf("%s %s %s", ts, r.Level.String(), r.Message)
	r.Attrs(func(a slog.Attr) bool {
		line += fmt.Sprintf(" %s=%v", a.Key, a.Value)
		return true
	})
	h.buf.append(line)
	return h.inner.Handle(ctx, r)
}

func (h *LogBufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogBufferHandler{buf: h.buf, inner: h.inner.WithAttrs(attrs)}
}

func (h *LogBufferHandler) WithGroup(name string) slog.Handler {
	return &LogBufferHandler{buf: h.buf, inner: h.inner.WithGroup(name)}
}

// NewBufferedLogger creates a slog.Logger that captures to a LogBuffer
// and also writes to stderr.
func NewBufferedLogger(buf *LogBuffer) *slog.Logger {
	handler := NewLogBufferHandler(buf, slog.Default().Handler())
	return slog.New(handler)
}

// NewDaemonLogger creates a logger that writes to stderr, a log file,
// and an in-memory ring buffer. The log file is at ~/.sky10/fs/daemon.log.
func NewDaemonLogger(buf *LogBuffer) *slog.Logger {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".sky10", "fs")
	os.MkdirAll(logDir, 0700)
	logPath := filepath.Join(logDir, "daemon.log")

	// Truncate on startup so we don't grow forever
	f, err := os.Create(logPath)
	if err != nil {
		// Fall back to stderr + buffer only
		return NewBufferedLogger(buf)
	}

	// Write to both stderr and file
	multiWriter := io.MultiWriter(os.Stderr, f)
	textHandler := slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	handler := NewLogBufferHandler(buf, textHandler)
	return slog.New(handler)
}
