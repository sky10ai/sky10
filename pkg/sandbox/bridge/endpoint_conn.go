package bridge

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// connWriter wraps a websocket connection with a mutex so the read
// loop's response sends do not race with future host-initiated pushes
// (Direction == DirectionPush envelopes emitted on the same socket).
type connWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func newConnWriter(conn *websocket.Conn) *connWriter {
	return &connWriter{conn: conn}
}

func (w *connWriter) write(ctx context.Context, env responseEnvelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return wsjson.Write(ctx, w.conn, env)
}

// runConnection drives one accepted websocket connection through its
// read-dispatch-respond loop until the peer disconnects, the context
// is cancelled, or an unrecoverable error occurs.
//
// Identity is established by the caller (the Endpoint's HTTP handler)
// and passed in fixed; this loop never reads identity from the wire.
func (e *Endpoint) runConnection(ctx context.Context, conn *websocket.Conn, agentID, deviceID string) {
	conn.SetReadLimit(e.readLimit())
	writer := newConnWriter(conn)

	for {
		var wire wireEnvelope
		if err := wsjson.Read(ctx, conn, &wire); err != nil {
			status := websocket.CloseStatus(err)
			if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway {
				e.logger.Debug("bridge read failed",
					"endpoint", e.name,
					"agent_id", agentID,
					"error", err,
				)
			}
			return
		}
		e.dispatchOne(ctx, writer, wire, agentID, deviceID)
	}
}

// dispatchOne validates one wire envelope and either invokes its
// handler or writes a typed error response. Each dispatch is fully
// independent — one bad envelope does not affect the next.
func (e *Endpoint) dispatchOne(ctx context.Context, writer *connWriter, wire wireEnvelope, agentID, deviceID string) {
	now := e.clock()

	spec, ok := e.types[wire.Type]
	if !ok {
		e.audit.WriteAudit(AuditLine{
			Ts:        now,
			Endpoint:  e.name,
			AgentID:   agentID,
			DeviceID:  deviceID,
			Type:      wire.Type,
			RequestID: wire.RequestID,
			Nonce:     wire.Nonce,
			Decision:  ErrCodeTypeUnregistered,
		})
		e.respondError(ctx, writer, wire, now, ErrCodeTypeUnregistered, "")
		return
	}

	if int64(len(wire.Payload)) > spec.MaxPayloadSize {
		e.recordRejection(now, spec, wire, agentID, deviceID, ErrCodePayloadTooLarge, "")
		e.respondError(ctx, writer, wire, now, ErrCodePayloadTooLarge, "")
		return
	}

	callerTs := now
	if wire.Timestamp != "" {
		t, err := time.Parse(time.RFC3339Nano, wire.Timestamp)
		if err != nil {
			e.recordRejection(now, spec, wire, agentID, deviceID, ErrCodeParseError, "invalid ts")
			e.respondError(ctx, writer, wire, now, ErrCodeParseError, "invalid ts")
			return
		}
		callerTs = t
	}

	if err := e.replay.Check(agentID, wire.Type, wire.Nonce, callerTs, spec.NonceWindow); err != nil {
		e.recordRejection(now, spec, wire, agentID, deviceID, ErrCodeReplay, err.Error())
		e.respondError(ctx, writer, wire, now, ErrCodeReplay, "")
		return
	}

	if !e.quota.Check(agentID, wire.Type, spec.RateLimit) {
		e.recordRejection(now, spec, wire, agentID, deviceID, ErrCodeRateLimited, "")
		e.respondError(ctx, writer, wire, now, ErrCodeRateLimited, "")
		return
	}

	env := Envelope{
		Type:      wire.Type,
		AgentID:   agentID,
		DeviceID:  deviceID,
		RequestID: wire.RequestID,
		Timestamp: now,
		Nonce:     wire.Nonce,
		Payload:   wire.Payload,
	}

	respPayload, handlerErr := spec.Handler(ctx, env)

	if handlerErr != nil {
		e.recordRejection(now, spec, wire, agentID, deviceID, ErrCodeHandlerError, handlerErr.Error())
		e.respondError(ctx, writer, wire, now, ErrCodeHandlerError, handlerErr.Error())
		return
	}

	e.recordAccepted(now, spec, wire, agentID, deviceID)

	if spec.Direction == DirectionPush {
		// Push envelopes carry no response. Handler-supplied payload
		// for a push is a contract violation but we silently drop it
		// to avoid surprising callers; auditing already recorded
		// acceptance.
		return
	}

	if respPayload == nil {
		respPayload = json.RawMessage("null")
	}
	resp := responseEnvelope{
		Type:      wire.Type,
		RequestID: wire.RequestID,
		Ts:        now.UTC().Format(time.RFC3339Nano),
		Payload:   respPayload,
	}
	if err := writer.write(ctx, resp); err != nil {
		e.logger.Debug("bridge response write failed",
			"endpoint", e.name,
			"agent_id", agentID,
			"type", wire.Type,
			"error", err,
		)
	}
}

func (e *Endpoint) respondError(ctx context.Context, writer *connWriter, wire wireEnvelope, now time.Time, code, message string) {
	resp := responseEnvelope{
		Type:      wire.Type,
		RequestID: wire.RequestID,
		Ts:        now.UTC().Format(time.RFC3339Nano),
		Error:     &envelopeError{Code: code, Message: message},
	}
	if err := writer.write(ctx, resp); err != nil {
		e.logger.Debug("bridge error write failed",
			"endpoint", e.name,
			"type", wire.Type,
			"code", code,
			"error", err,
		)
	}
}

func (e *Endpoint) recordAccepted(now time.Time, spec TypeSpec, wire wireEnvelope, agentID, deviceID string) {
	if spec.AuditLevel == AuditNone {
		return
	}
	line := AuditLine{
		Ts:        now,
		Endpoint:  e.name,
		AgentID:   agentID,
		DeviceID:  deviceID,
		Type:      wire.Type,
		RequestID: wire.RequestID,
		Nonce:     wire.Nonce,
		Decision:  "accepted",
	}
	if spec.AuditLevel == AuditFull {
		line.PayloadHash = payloadHash(wire.Payload)
	}
	e.audit.WriteAudit(line)
}

func (e *Endpoint) recordRejection(now time.Time, spec TypeSpec, wire wireEnvelope, agentID, deviceID, decision, detail string) {
	if spec.AuditLevel == AuditNone {
		return
	}
	line := AuditLine{
		Ts:        now,
		Endpoint:  e.name,
		AgentID:   agentID,
		DeviceID:  deviceID,
		Type:      wire.Type,
		RequestID: wire.RequestID,
		Nonce:     wire.Nonce,
		Decision:  decision,
		Detail:    detail,
	}
	if spec.AuditLevel == AuditFull {
		line.PayloadHash = payloadHash(wire.Payload)
	}
	e.audit.WriteAudit(line)
}

// readLimit returns the effective per-message read limit: the largest
// MaxPayloadSize across registered types plus a generous overhead for
// envelope framing (type, request_id, nonce, ts).
func (e *Endpoint) readLimit() int64 {
	const framingOverhead = 4 * 1024
	var maxPayload int64
	for _, spec := range e.types {
		if spec.MaxPayloadSize > maxPayload {
			maxPayload = spec.MaxPayloadSize
		}
	}
	return maxPayload + framingOverhead
}
