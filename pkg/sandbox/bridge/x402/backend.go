package x402

import (
	"context"
	"encoding/json"
)

// Backend is the host-side surface the bridge handlers delegate to.
// pkg/x402 (when it lands) implements this interface; tests substitute
// a fake.
//
// All methods receive an agentID stamped by the bridge plumbing; the
// Backend uses it as the requester for scope filtering — agents only
// see services they have approved, only spend within their own caps,
// only see their own budget.
type Backend interface {
	// ListServices returns the services this agent has been approved
	// to call, with metadata the agent's LLM uses to decide whether
	// to invoke. Implementations should filter by the calling agent's
	// approval set; never return services the agent has not approved.
	ListServices(ctx context.Context, agentID string) ([]ServiceListing, error)

	// Call invokes one approved service on behalf of the agent.
	// Implementations perform the full 402 round-trip, sign with the
	// host wallet, enforce per-call and budget caps, and return the
	// upstream response plus a settlement receipt. The wallet stays
	// on host; the agent never sees the 402 challenge.
	Call(ctx context.Context, params CallParams) (*CallResult, error)

	// BudgetStatus returns the agent's current spend caps and today's
	// spend totals.
	BudgetStatus(ctx context.Context, agentID string) (*BudgetSnapshot, error)
}

// ServiceListing is one entry in the ListServices response. The
// description, tier, and hint fields are passed through to the LLM's
// tool list; the price field lets the agent reason about cost vs
// alternatives.
type ServiceListing struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	ServiceURL  string            `json:"service_url,omitempty"`
	Endpoints   []ServiceEndpoint `json:"endpoints,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	Tier        string            `json:"tier"`
	PriceUSDC   string            `json:"price_usdc,omitempty"`
	Hint        string            `json:"hint,omitempty"`
}

// ServiceEndpoint is one callable URL advertised by a service catalog.
// It is display/routing metadata for agents; service calls still pass
// service_id plus a relative path through the adapter.
type ServiceEndpoint struct {
	URL         string `json:"url"`
	Method      string `json:"method,omitempty"`
	Description string `json:"description,omitempty"`
	PriceUSDC   string `json:"price_usdc,omitempty"`
	Network     string `json:"network,omitempty"`
}

// CallParams describes one metered-service invocation. AgentID is filled
// in by the handler from the bus-stamped envelope identity, never
// from the payload.
type CallParams struct {
	AgentID      string            `json:"-"`
	ServiceID    string            `json:"service_id"`
	Path         string            `json:"path"`
	Method       string            `json:"method,omitempty"`
	Body         json.RawMessage   `json:"body,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	MaxPriceUSDC string            `json:"max_price_usdc"`

	// PaymentNonce binds this call to a specific payment authorization
	// so that retries with the same nonce are idempotent: the Backend
	// returns the cached signed result rather than double-charging.
	PaymentNonce string `json:"payment_nonce"`
}

// CallResult is the upstream HTTP response plus the settlement
// receipt. The receipt fields are present when the call actually
// charged; for retried-idempotent or otherwise free calls they may be
// absent.
type CallResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Receipt *Receipt          `json:"receipt,omitempty"`
}

// Receipt is the settlement record for one paid service call.
type Receipt struct {
	Tx         string `json:"tx"`
	Network    string `json:"network"`
	AmountUSDC string `json:"amount_usdc"`
	SettledAt  string `json:"settled_at"`
}

// BudgetSnapshot captures the agent's current spend caps and today's
// spend. PerService lists per-service caps and today's spend for any
// service the agent has been approved for.
type BudgetSnapshot struct {
	PerCallMaxUSDC string          `json:"per_call_max_usdc"`
	DailyCapUSDC   string          `json:"daily_cap_usdc"`
	SpentTodayUSDC string          `json:"spent_today_usdc"`
	PerService     []PerServiceCap `json:"per_service,omitempty"`
}

// PerServiceCap is one entry in BudgetSnapshot.PerService.
type PerServiceCap struct {
	ServiceID      string `json:"service_id"`
	DailyCapUSDC   string `json:"daily_cap_usdc"`
	SpentTodayUSDC string `json:"spent_today_usdc"`
}
