package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Format controls how structured logs are encoded.
type Format string

const (
	// FormatLogfmt writes key=value records using slog's text handler.
	FormatLogfmt Format = "logfmt"
	// FormatJSON writes one JSON object per line.
	FormatJSON Format = "json"
)

// Config controls process-wide logger installation.
type Config struct {
	Level       slog.Level
	Format      Format
	AddSource   bool
	FilePath    string
	Stderr      bool
	Service     string
	Version     string
	BufferLines int
}

// Runtime is the installed logger plus the resources it owns.
type Runtime struct {
	Logger *slog.Logger
	Buffer *Buffer

	closeFn func() error
}

// Close releases any resources owned by the runtime.
func (r *Runtime) Close() error {
	if r == nil || r.closeFn == nil {
		return nil
	}
	return r.closeFn()
}

// New builds a logger runtime from cfg.
func New(cfg Config) (*Runtime, error) {
	cfg = withDefaults(cfg)

	buf := NewBuffer(cfg.BufferLines)
	bufferWriter := newLineBufferWriter(buf)

	writers := make([]io.Writer, 0, 3)
	var closeFns []func() error

	if cfg.FilePath != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.FilePath), 0700); err != nil {
			return nil, fmt.Errorf("creating log directory: %w", err)
		}
		f, err := os.OpenFile(cfg.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("opening log file: %w", err)
		}
		writers = append(writers, f)
		closeFns = append(closeFns, f.Close)
	}
	if cfg.Stderr {
		writers = append(writers, os.Stderr)
	}
	writers = append(writers, bufferWriter)

	writer := writers[0]
	if len(writers) > 1 {
		writer = io.MultiWriter(writers...)
	}

	opts := &slog.HandlerOptions{
		AddSource: cfg.AddSource,
		Level:     cfg.Level,
	}

	var handler slog.Handler
	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(writer, opts)
	case FormatLogfmt:
		handler = slog.NewTextHandler(writer, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Format)
	}

	logger := slog.New(handler)
	if cfg.Service != "" {
		logger = logger.With("service", cfg.Service)
	}
	if cfg.Version != "" {
		logger = logger.With("version", cfg.Version)
	}

	return &Runtime{
		Logger: logger,
		Buffer: buf,
		closeFn: func() error {
			var firstErr error
			for _, fn := range closeFns {
				if err := fn(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}, nil
}

// InstallDefault builds and installs the process-wide default logger.
func InstallDefault(cfg Config) (*Runtime, error) {
	rt, err := New(cfg)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(rt.Logger)
	return rt, nil
}

// WithComponent adds the component field to logger.
func WithComponent(logger *slog.Logger, component string) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if component == "" {
		return logger
	}
	return logger.With("component", component)
}

// ParseFormat parses a configured logging format.
func ParseFormat(value string) (Format, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "logfmt", "text", "txt":
		return FormatLogfmt, nil
	case "json", "jsonl":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unsupported log format %q", value)
	}
}

// ParseLevel parses a configured log level.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

func withDefaults(cfg Config) Config {
	if cfg.Format == "" {
		cfg.Format = FormatLogfmt
	}
	if cfg.FilePath == "" && !cfg.Stderr {
		cfg.Stderr = true
	}
	if cfg.BufferLines <= 0 {
		cfg.BufferLines = 1000
	}
	return cfg
}

// Buffer stores recent log lines for debug dumps.
type Buffer struct {
	mu      sync.Mutex
	entries []string
	max     int
}

// NewBuffer creates a buffer that keeps the last max lines.
func NewBuffer(max int) *Buffer {
	return &Buffer{
		entries: make([]string, 0, max),
		max:     max,
	}
}

// Lines returns a copy of the buffered log lines.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.entries))
	copy(out, b.entries)
	return out
}

func (b *Buffer) append(line string) {
	if b == nil || b.max <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= b.max {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = line
		return
	}
	b.entries = append(b.entries, line)
}

type lineBufferWriter struct {
	buf *Buffer

	mu      sync.Mutex
	pending string
}

func newLineBufferWriter(buf *Buffer) *lineBufferWriter {
	return &lineBufferWriter{buf: buf}
}

func (w *lineBufferWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.pending += string(p)
	for {
		idx := strings.IndexByte(w.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(w.pending[:idx], "\r")
		w.buf.append(line)
		w.pending = w.pending[idx+1:]
	}

	return len(p), nil
}
