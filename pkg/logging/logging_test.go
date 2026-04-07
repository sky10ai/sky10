package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogfmtRuntimeWritesFileAndBuffer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.log")
	rt, err := New(Config{
		FilePath: path,
		Service:  "sky10",
		Version:  "test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rt.Close()

	logger := WithComponent(rt.Logger, "kv")
	logger.Info("snapshot merged", "entries", 2)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, "service=sky10") {
		t.Fatalf("log line missing service: %q", line)
	}
	if !strings.Contains(line, "component=kv") {
		t.Fatalf("log line missing component: %q", line)
	}
	if !strings.Contains(line, "msg=\"snapshot merged\"") {
		t.Fatalf("log line missing message: %q", line)
	}

	lines := rt.Buffer.Lines()
	if len(lines) != 1 {
		t.Fatalf("buffer line count = %d, want 1", len(lines))
	}
	if lines[0] != line {
		t.Fatalf("buffer line = %q, want %q", lines[0], line)
	}
}

func TestJSONRuntimeWritesStructuredLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	rt, err := New(Config{
		FilePath: path,
		Format:   FormatJSON,
		Service:  "sky10",
		Version:  "test",
		Level:    slog.LevelDebug,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rt.Close()

	logger := WithComponent(rt.Logger, "fs")
	logger.Debug("sync started", "device", "dev-a")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\nline=%s", err, data)
	}
	if got["component"] != "fs" {
		t.Fatalf("component = %v, want fs", got["component"])
	}
	if got["msg"] != "sync started" {
		t.Fatalf("msg = %v, want sync started", got["msg"])
	}
	if got["device"] != "dev-a" {
		t.Fatalf("device = %v, want dev-a", got["device"])
	}
}

func TestBufferKeepsMostRecentLinesInOrder(t *testing.T) {
	t.Parallel()

	buf := NewBuffer(3)
	for _, line := range []string{"one", "two", "three", "four", "five"} {
		buf.append(line)
	}

	lines := buf.Lines()
	want := []string{"three", "four", "five"}
	if len(lines) != len(want) {
		t.Fatalf("line count = %d, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestParseFormatAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]Format{
		"":       FormatLogfmt,
		"logfmt": FormatLogfmt,
		"text":   FormatLogfmt,
		"txt":    FormatLogfmt,
		"json":   FormatJSON,
		"jsonl":  FormatJSON,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil {
			t.Fatalf("ParseFormat(%q) error = %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
