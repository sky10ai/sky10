package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// AuditLine is the structured record the plumbing emits for every
// envelope it accepts or rejects. Payload bodies are not captured;
// when AuditLevel is AuditFull the line carries a SHA-256 hash of the
// payload bytes, never the bytes themselves.
type AuditLine struct {
	Ts          time.Time `json:"ts"`
	Endpoint    string    `json:"endpoint"`
	AgentID     string    `json:"agent_id,omitempty"`
	DeviceID    string    `json:"device_id,omitempty"`
	Type        string    `json:"type,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	Nonce       string    `json:"nonce,omitempty"`
	PayloadHash string    `json:"payload_hash,omitempty"`
	Decision    string    `json:"decision"`
	Detail      string    `json:"detail,omitempty"`
}

// AuditWriter consumes AuditLines emitted by the plumbing. Endpoints
// hold a single writer instance shared across connections; concurrent
// calls are expected.
type AuditWriter interface {
	WriteAudit(line AuditLine)
}

// NoopAuditWriter discards every audit line. Useful in tests where
// audit assertions are made via a test-specific writer instead.
type NoopAuditWriter struct{}

// WriteAudit implements AuditWriter.
func (NoopAuditWriter) WriteAudit(AuditLine) {}

// JSONLAuditWriter writes one JSON object per line to an io.Writer
// (typically a file). Concurrent WriteAudit calls are serialized by
// an internal mutex.
type JSONLAuditWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLAuditWriter wraps w. Caller is responsible for closing w
// when the daemon shuts down.
func NewJSONLAuditWriter(w io.Writer) *JSONLAuditWriter {
	return &JSONLAuditWriter{w: w}
}

// WriteAudit implements AuditWriter. Encoding errors are swallowed —
// audit logging must never block the data plane. Operators concerned
// about audit fidelity should monitor the underlying writer separately.
func (a *JSONLAuditWriter) WriteAudit(line AuditLine) {
	if a == nil || a.w == nil {
		return
	}
	buf, err := json.Marshal(line)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(buf)
	_, _ = a.w.Write([]byte{'\n'})
}

// payloadHash returns the SHA-256 hash of payload prefixed by "sha256:"
// for use in AuditLine.PayloadHash when the type's AuditLevel is
// AuditFull. Returns the empty string when payload is empty.
func payloadHash(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:]))
}
