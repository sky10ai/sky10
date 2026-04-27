package x402

import "time"

// Tier classifies how essential a service is. The agent runtime uses
// it to decide whether to expose a service as a tool by default; the
// host uses it to drive overlay-based default-on / default-off
// decisions.
type Tier string

const (
	// TierPrimitive marks services whose capability cannot be
	// substituted with the local browser, web search, shell, or file
	// tools (Deepgram, fal.ai, E2B, Browserbase, etc.). Default-on
	// candidates in the overlay.
	TierPrimitive Tier = "primitive"

	// TierConvenience marks services whose function the local browser
	// can usually perform (Tripadvisor, Apollo, etc.). Default-off in
	// the overlay; user opts in when they have a specific reason.
	TierConvenience Tier = "convenience"
)

// Approval is the per-(agent, service) approval record. An approval
// is required before x402.serviceCall envelopes referencing that
// service succeed for that agent.
type Approval struct {
	AgentID      string    `json:"agent_id"`
	ServiceID    string    `json:"service_id"`
	ApprovedAt   time.Time `json:"approved_at"`
	MaxPriceUSDC string    `json:"max_price_usdc"`
	Tier         Tier      `json:"tier"`
}

// PolicyEntry is the catalog-level metadata that overlays the upstream
// directory listing with sky10's editorial layer (tier, default-on,
// hint shown to the LLM in tool descriptions). One entry per service
// id.
type PolicyEntry struct {
	ServiceID string `json:"service_id"`
	Tier      Tier   `json:"tier"`
	DefaultOn bool   `json:"default_on"`
	Hint      string `json:"hint,omitempty"`
}
