package comms

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJSONLAuditWriterEmitsOneLinePerEntry(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewJSONLAuditWriter(&buf)
	w.WriteAudit(AuditLine{
		Ts:        time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
		Endpoint:  "x402",
		AgentID:   "A-1",
		Type:      "x402.list_services",
		Decision:  "accepted",
		RequestID: "r1",
	})
	w.WriteAudit(AuditLine{
		Ts:       time.Date(2026, 4, 27, 12, 0, 1, 0, time.UTC),
		Endpoint: "x402",
		AgentID:  "A-1",
		Type:     "x402.service_call",
		Decision: "rate_limited",
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var got AuditLine
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d not valid JSON: %v", i, err)
		}
	}
}

func TestJSONLAuditWriterIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewJSONLAuditWriter(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				w.WriteAudit(AuditLine{Decision: "accepted"})
			}
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 32*32 {
		t.Fatalf("expected %d lines under concurrency, got %d", 32*32, len(lines))
	}
	for i, line := range lines {
		var got AuditLine
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d corrupted: %v", i, err)
		}
	}
}

func TestPayloadHash(t *testing.T) {
	t.Parallel()
	if got := payloadHash(nil); got != "" {
		t.Fatalf("nil payload hash = %q, want empty", got)
	}
	if got := payloadHash([]byte{}); got != "" {
		t.Fatalf("empty payload hash = %q, want empty", got)
	}
	got := payloadHash([]byte("hello"))
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("payload hash should be sha256-prefixed, got %q", got)
	}
	if got != payloadHash([]byte("hello")) {
		t.Fatal("payload hash should be deterministic")
	}
	if got == payloadHash([]byte("world")) {
		t.Fatal("different payloads should hash differently")
	}
}

func TestNoopAuditWriterDoesNothing(t *testing.T) {
	t.Parallel()
	// The contract is "WriteAudit doesn't panic and produces no
	// observable side effect." Calling it under nil/zero values is
	// the most honest verification.
	NoopAuditWriter{}.WriteAudit(AuditLine{})
}
