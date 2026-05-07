package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	KindRequest  = "request"
	KindResponse = "response"
)

// Frame is the JSON wire unit sent over a sandbox bridge WebSocket.
// Type is capability-local: the endpoint that owns the socket decides
// what values are valid.
type Frame struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Type    string          `json:"type,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a structured bridge error returned in response frames.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return e.Code
	}
	if strings.TrimSpace(e.Code) == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func bridgeError(code, message string) *Error {
	return &Error{Code: strings.TrimSpace(code), Message: strings.TrimSpace(message)}
}

// HandlerError returns an error with a stable bridge error code. Handlers may
// return this when callers need to branch on the error category.
func HandlerError(code, message string) error {
	return bridgeError(code, message)
}

func errorFrame(id, typ string, err error) Frame {
	resp := Frame{
		Kind: KindResponse,
		ID:   id,
		Type: typ,
	}
	if err == nil {
		resp.Error = bridgeError("error", "unknown bridge error")
		return resp
	}
	var bridgeErr *Error
	if ok := errorAs(err, &bridgeErr); ok && bridgeErr != nil {
		resp.Error = bridgeErr
		return resp
	}
	resp.Error = bridgeError("handler_error", err.Error())
	return resp
}

func validateOutboundRequest(typ string) error {
	if strings.TrimSpace(typ) == "" {
		return fmt.Errorf("bridge: request type is required")
	}
	return nil
}

func validateInboundFrame(frame Frame) error {
	switch strings.TrimSpace(frame.Kind) {
	case KindRequest:
		if strings.TrimSpace(frame.ID) == "" {
			return fmt.Errorf("bridge: request id is required")
		}
		if strings.TrimSpace(frame.Type) == "" {
			return fmt.Errorf("bridge: request type is required")
		}
	case KindResponse:
		if strings.TrimSpace(frame.ID) == "" {
			return fmt.Errorf("bridge: response id is required")
		}
	default:
		return fmt.Errorf("bridge: unsupported frame kind %q", frame.Kind)
	}
	return nil
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		return append(json.RawMessage(nil), raw...), nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return body, nil
}
