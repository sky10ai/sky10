package bridge

import (
	"context"
	"encoding/json"
	"time"
)

// Envelope is the value an envelope handler receives. Identity fields
// are stamped by the plumbing from the authenticated transport channel
// and are trustworthy. Payload is opaque bytes the handler must parse
// and validate explicitly — treat it as adversarial input.
type Envelope struct {
	// Type is the registered envelope type name; the dispatcher used it
	// to select this handler.
	Type string

	// AgentID is the identity of the agent that opened the connection,
	// stamped by the plumbing from the authenticated transport. Caller-
	// supplied values in the wire JSON are dropped by the deserializer
	// before the handler sees this field.
	AgentID string

	// DeviceID is the originating device identity, stamped by the
	// plumbing the same way as AgentID.
	DeviceID string

	// RequestID, when set by the caller, is echoed back on the response
	// envelope so the client can correlate sync-on-async calls.
	RequestID string

	// Timestamp is the receipt time on the host clock. The wire envelope
	// may carry a caller-supplied ts but the plumbing always restamps
	// with its own clock for replay-window enforcement and audit
	// ordering. Handlers see the host-stamped value.
	Timestamp time.Time

	// Nonce is the caller-supplied replay-dedup key. The plumbing uses
	// it for replay protection; handlers should treat it as opaque.
	Nonce string

	// Payload is the handler-specific JSON payload. UNTRUSTED. Handlers
	// must json.Unmarshal into a typed struct and validate every field
	// before any business logic. See handler-discipline rule 1.
	Payload json.RawMessage
}

// EnvelopeHandler is the function signature every envelope handler must match.
//
// A handler returning a non-nil response payload triggers the plumbing
// to send a response envelope keyed by the same RequestID. A handler
// that returns nil payload and nil error sends no response (e.g. for
// Push direction envelopes).
//
// A handler returning a non-nil error causes the plumbing to send an
// error envelope keyed by the same RequestID. The error message is
// sent to the caller as-is, so handlers should avoid leaking sensitive
// detail in error text.
type EnvelopeHandler func(ctx context.Context, env Envelope) (json.RawMessage, error)

// wireEnvelope is the structure read from the websocket. By design it
// has no AgentID or DeviceID fields: any payload-supplied identity is
// silently dropped because the field does not exist on this struct.
// json.Unmarshal ignores unknown top-level keys by default, which is
// exactly the structural protection we want.
type wireEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Nonce     string          `json:"nonce"`
	Timestamp string          `json:"ts,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// responseEnvelope is the wire shape the plumbing writes back for sync-
// on-async responses and error returns from handlers.
type responseEnvelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Ts        string          `json:"ts"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     *envelopeError  `json:"error,omitempty"`
}

// envelopeError is the structured error shape the plumbing emits when
// it rejects an envelope or a handler returns an error.
type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Error codes used in envelopeError.Code. Consumers should branch on
// these codes (not on Message) to decide what to do.
const (
	ErrCodeTypeUnregistered = "type_unregistered"
	ErrCodePayloadTooLarge  = "payload_too_large"
	ErrCodeRateLimited      = "rate_limited"
	ErrCodeReplay           = "replay"
	ErrCodeParseError       = "parse_error"
	ErrCodeHandlerError     = "handler_error"
)
