package comms

import (
	"fmt"
	"time"
)

// Direction describes the flow of an envelope type. The plumbing uses
// it to decide whether a handler return value implies a response
// envelope.
type Direction int

const (
	// DirectionRequestResponse is sync-on-async: the caller sets
	// RequestID and the plumbing sends one response envelope keyed by
	// the same RequestID.
	DirectionRequestResponse Direction = iota + 1

	// DirectionPush is one-way: caller sends, no response envelope.
	// Handler should return nil payload.
	DirectionPush

	// DirectionSubscribe is establish-stream: caller sends one envelope
	// to subscribe, host pushes update envelopes over time. The initial
	// handler call may return an acknowledgment payload; subsequent
	// pushes use a separate Push-direction type.
	DirectionSubscribe
)

// AuditLevel controls how much of an envelope is captured in the audit
// log. Payload bodies are never logged in full — when AuditFull is set
// the plumbing logs a SHA-256 hash of the payload, not the bytes.
type AuditLevel int

const (
	// AuditNone disables audit logging for this envelope type. Reserved
	// for high-volume non-security-relevant types; use sparingly.
	AuditNone AuditLevel = iota

	// AuditHeaders logs envelope metadata (type, agent, device, ts,
	// nonce, request_id, decision) but no payload information.
	AuditHeaders

	// AuditFull logs envelope metadata plus a SHA-256 hash of the
	// payload bytes. This is the right default for capabilities that
	// need traceability.
	AuditFull
)

// RateLimit configures per-(agent, envelope-type) token-bucket rate
// limiting. PerAgent is tokens per Window; Burst is the max bucket
// capacity. A call consumes one token; refused calls return
// ErrCodeRateLimited.
type RateLimit struct {
	PerAgent int
	Burst    int
	Window   time.Duration
}

// TypeSpec declares one envelope type that an Endpoint accepts. Every
// field is required: the registration helper panics if any field is
// zero, by design — there are no "reasonable defaults" because the
// right rate limit, payload size, etc. are different for every type
// and must be a deliberate decision at the moment the type is added.
type TypeSpec struct {
	// Name is the wire-level type identifier (e.g. "x402.service_call").
	// It is the dispatch key within one Endpoint and must be unique
	// within that Endpoint's registry.
	Name string

	// Direction controls response semantics. See Direction constants.
	Direction Direction

	// MaxPayloadSize is the largest accepted payload in bytes. Envelopes
	// with larger payloads are rejected before the handler runs.
	MaxPayloadSize int64

	// RateLimit is per-(agent, this-type) token bucket. Fields all
	// required.
	RateLimit RateLimit

	// NonceWindow is how long the plumbing remembers a (agent, type,
	// nonce) tuple for replay rejection. Should comfortably exceed the
	// expected end-to-end roundtrip time.
	NonceWindow time.Duration

	// AuditLevel controls audit log verbosity for this type.
	AuditLevel AuditLevel

	// Handler is the function the plumbing calls after identity
	// stamping, replay, quota, and size checks have passed.
	Handler Handler
}

// validate panics with a clear message if any required field is unset.
// Called from Endpoint.Register so misuse fails at registration time
// (typically daemon startup), not in production traffic.
func (s TypeSpec) validate() {
	if s.Name == "" {
		panic("comms: TypeSpec.Name is required")
	}
	if s.Direction == 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing Direction", s.Name))
	}
	if s.MaxPayloadSize <= 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing MaxPayloadSize", s.Name))
	}
	if s.RateLimit.PerAgent <= 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing RateLimit.PerAgent", s.Name))
	}
	if s.RateLimit.Burst <= 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing RateLimit.Burst", s.Name))
	}
	if s.RateLimit.Window <= 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing RateLimit.Window", s.Name))
	}
	if s.NonceWindow <= 0 {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing NonceWindow", s.Name))
	}
	if s.Handler == nil {
		panic(fmt.Sprintf("comms: TypeSpec %q is missing Handler", s.Name))
	}
}
