package x402

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Backend is the host-side API the agent-facing comms handlers
// delegate to (via a thin adapter at daemon wiring time). It is the
// only place that touches the wallet and the registry; everything
// outside Backend reads handler output, never internal state.
//
// Backend uses pkg/x402's native types (Pin, Approval, Receipt, etc.)
// rather than the comms-side ServiceListing/CallResult shapes so the
// package stays free of comms knowledge. Adapter code in the daemon
// wiring maps Backend methods onto comms/x402.Backend.
type Backend struct {
	registry  *Registry
	transport *Transport
	budget    *Budget
	clock     func() time.Time
}

// BackendOptions wires the Backend's collaborators. All fields are
// required; misuse panics in NewBackend.
type BackendOptions struct {
	Registry  *Registry
	Transport *Transport
	Budget    *Budget
	Clock     func() time.Time
}

// NewBackend constructs a Backend.
func NewBackend(opts BackendOptions) *Backend {
	if opts.Registry == nil {
		panic("x402: Backend requires non-nil Registry")
	}
	if opts.Transport == nil {
		panic("x402: Backend requires non-nil Transport")
	}
	if opts.Budget == nil {
		panic("x402: Backend requires non-nil Budget")
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Backend{
		registry:  opts.Registry,
		transport: opts.Transport,
		budget:    opts.Budget,
		clock:     clock,
	}
}

// ListServices returns the services this agent has been approved for,
// joined with overlay metadata. The returned slice is suitable for
// passing through the comms adapter to the agent.
func (b *Backend) ListServices(_ context.Context, agentID string) ([]ListApprovedListing, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, errors.New("agentID required")
	}
	return b.registry.ListApproved(agentID), nil
}

// CallParams is the input to Backend.Call. AgentID is bus-stamped by
// the comms layer before the adapter calls into the Backend; pkg/x402
// trusts it as authoritative.
type CallParams struct {
	AgentID      string
	ServiceID    string
	Path         string
	Method       string
	Headers      map[string]string
	Body         []byte
	MaxPriceUSDC string

	// PaymentNonce is the agent-supplied idempotency key. Backend
	// uses it to dedupe retries: a Call replayed with the same nonce
	// returns the cached prior result instead of double-charging.
	// (M1: nonce is recorded on receipt; full idempotency cache is
	// a future hardening.)
	PaymentNonce string
}

// CallResult is the output of Backend.Call.
type CallResult struct {
	Status  int
	Headers map[string]string
	Body    []byte
	Receipt *Receipt
}

// Call performs one approved x402 service invocation. The flow:
//
//  1. Verify the agent has approved this service.
//  2. Verify the live manifest still matches the pin.
//  3. Authorize against the per-call cap, per-service daily cap, and
//     daily total cap.
//  4. Issue the request through the x402 transport (which detects
//     402, signs, retries).
//  5. Record a Receipt and decrement the budget.
//
// Returns ErrServiceNotApproved, ErrPinMismatch, ErrBudgetExceeded,
// ErrPriceQuoteTooHigh, ErrPaymentNotAccepted, or
// ErrSignerNotConfigured for the corresponding failure modes.
func (b *Backend) Call(ctx context.Context, params CallParams) (*CallResult, error) {
	if strings.TrimSpace(params.AgentID) == "" {
		return nil, errors.New("agentID required")
	}
	if strings.TrimSpace(params.ServiceID) == "" {
		return nil, errors.New("serviceID required")
	}
	if strings.TrimSpace(params.Path) == "" {
		return nil, errors.New("path required")
	}
	if strings.TrimSpace(params.MaxPriceUSDC) == "" {
		return nil, errors.New("max_price_usdc required")
	}

	if _, err := b.registry.Approval(params.AgentID, params.ServiceID); err != nil {
		return nil, err
	}
	manifest, err := b.registry.Manifest(params.ServiceID)
	if err != nil {
		return nil, err
	}
	pin, err := b.registry.Pin(params.AgentID, params.ServiceID)
	if err != nil {
		return nil, err
	}
	if err := pin.Verify(manifest); err != nil {
		return nil, err
	}

	if err := b.budget.Authorize(params.AgentID, params.ServiceID, params.MaxPriceUSDC, manifest.MaxPriceUSDC); err != nil {
		return nil, err
	}

	target, err := joinEndpointPath(manifest.Endpoint, params.Path)
	if err != nil {
		return nil, err
	}
	resp, err := b.transport.Call(ctx, CallRequest{
		Method:  params.Method,
		URL:     target,
		Headers: params.Headers,
		Body:    params.Body,
	})
	if err != nil {
		return nil, err
	}

	receipt := Receipt{
		Ts:           b.clock().UTC(),
		AgentID:      params.AgentID,
		ServiceID:    params.ServiceID,
		Path:         params.Path,
		AmountUSDC:   manifest.MaxPriceUSDC,
		MaxPriceUSDC: params.MaxPriceUSDC,
	}
	if resp.Receipt != nil {
		receipt.Tx = resp.Receipt.Tx
		receipt.Network = resp.Receipt.Network
		if resp.Receipt.AmountUSDC != "" {
			receipt.AmountUSDC = resp.Receipt.AmountUSDC
		}
	}
	if err := b.budget.Charge(receipt); err != nil {
		return nil, fmt.Errorf("recording receipt: %w", err)
	}

	out := &CallResult{
		Status:  resp.Status,
		Headers: resp.Headers,
		Body:    resp.Body,
	}
	out.Receipt = &receipt
	return out, nil
}

// BudgetStatus returns the agent's current spend snapshot.
func (b *Backend) BudgetStatus(_ context.Context, agentID string) (Snapshot, error) {
	if strings.TrimSpace(agentID) == "" {
		return Snapshot{}, errors.New("agentID required")
	}
	return b.budget.Status(agentID), nil
}

// joinEndpointPath joins the manifest's endpoint URL with a per-call
// path. Both are trusted at this point (manifest came from the
// registry, path was validated by the comms layer to start with /),
// so the join is straightforward — but we explicitly disallow paths
// that would escape the endpoint host via scheme or absolute URLs.
func joinEndpointPath(endpoint, path string) (string, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	path = strings.TrimSpace(path)
	if endpoint == "" {
		return "", errors.New("manifest endpoint empty")
	}
	if path == "" {
		return endpoint, nil
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return "", fmt.Errorf("path %q must be relative", path)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return endpoint + path, nil
}

// MarshalCallResultJSON renders a CallResult into the JSON shape the
// comms adapter passes back to the agent. The body is preserved
// verbatim (raw JSON if the upstream returned JSON; opaque bytes
// otherwise base64-wrapped on the way through).
//
// Provided as a helper so callers don't reinvent the marshal shape.
func MarshalCallResultJSON(r *CallResult) (json.RawMessage, error) {
	out := struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers,omitempty"`
		Body    json.RawMessage   `json:"body,omitempty"`
		Receipt *Receipt          `json:"receipt,omitempty"`
	}{
		Status:  r.Status,
		Headers: r.Headers,
		Body:    r.Body,
		Receipt: r.Receipt,
	}
	return json.Marshal(out)
}
