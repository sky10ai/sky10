// envelope: x402.service_call
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.
//
// This is the load-bearing envelope. The handler validates, then
// hands a CallParams to the Backend which performs the full 402
// round-trip on host. The agent never sees the 402 challenge or the
// signing wallet — only the upstream response plus a receipt.

package x402

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/sandbox/comms"
)

type serviceCallParams struct {
	ServiceID    string            `json:"service_id"`
	Path         string            `json:"path"`
	Method       string            `json:"method,omitempty"`
	Body         json.RawMessage   `json:"body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	MaxPriceUSDC string            `json:"max_price_usdc"`
	PaymentNonce string            `json:"payment_nonce"`
}

func (h *handlers) handleServiceCall(ctx context.Context, env comms.Envelope) (json.RawMessage, error) {
	params, err := parseServiceCallParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateServiceCallParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	result, err := h.backend.Call(ctx, CallParams{
		AgentID:      env.AgentID,
		ServiceID:    params.ServiceID,
		Path:         params.Path,
		Method:       params.Method,
		Body:         params.Body,
		Headers:      params.Headers,
		MaxPriceUSDC: params.MaxPriceUSDC,
		PaymentNonce: params.PaymentNonce,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func parseServiceCallParams(raw json.RawMessage) (serviceCallParams, error) {
	var p serviceCallParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateServiceCallParams(p serviceCallParams) error {
	if strings.TrimSpace(p.ServiceID) == "" {
		return errors.New("service_id is required")
	}
	if strings.TrimSpace(p.Path) == "" {
		return errors.New("path is required")
	}
	if !strings.HasPrefix(p.Path, "/") {
		return errors.New("path must start with /")
	}
	if strings.TrimSpace(p.MaxPriceUSDC) == "" {
		return errors.New("max_price_usdc is required")
	}
	if strings.TrimSpace(p.PaymentNonce) == "" {
		return errors.New("payment_nonce is required")
	}
	if p.Method != "" {
		switch strings.ToUpper(p.Method) {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
		default:
			return fmt.Errorf("method %q not allowed", p.Method)
		}
	}
	return nil
}
