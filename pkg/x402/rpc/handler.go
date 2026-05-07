// Package rpc exposes pkg/x402's host-side state to the daemon's
// JSON-RPC server. Web UI and CLI tools call these methods to manage
// the service catalog, approve services for use by agents, and
// inspect budget state.
//
// The methods here are *host* RPC: they live on the daemon's local
// unix socket alongside wallet.*, secrets.*, etc. They are not part
// of the sandbox-comms surface — agents do not call x402.* directly;
// they go through pkg/sandbox/comms/x402's envelope handlers, which
// in turn delegate to pkg/x402.Backend.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

// Handler dispatches x402.* RPC methods.
type Handler struct {
	registry *x402.Registry
	budget   *x402.Budget
}

// NewHandler constructs a Handler. registry must be the same
// pkg/x402.Registry instance the daemon's comms-side x402 endpoint
// uses, so changes here affect agent-facing state immediately. The
// budget pointer may be nil; budget-related methods return zero
// values when it is.
func NewHandler(registry *x402.Registry, budget *x402.Budget) *Handler {
	return &Handler{registry: registry, budget: budget}
}

// Dispatch implements the repo's RPC handler contract.
func (h *Handler) Dispatch(_ context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "x402.") {
		return nil, nil, false
	}
	if h == nil || h.registry == nil {
		return nil, fmt.Errorf("x402 RPC: registry not configured"), true
	}
	switch method {
	case "x402.listServices":
		return h.listServices(params)
	case "x402.setEnabled":
		return h.setEnabled(params)
	case "x402.budgetStatus":
		return h.budgetStatus(params)
	case "x402.receipts":
		return h.receipts(params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

// BudgetStatusResult is the response shape for x402.budgetStatus.
type BudgetStatusResult struct {
	PerCallMaxUSDC string `json:"per_call_max_usdc"`
	DailyCapUSDC   string `json:"daily_cap_usdc"`
	SpentTodayUSDC string `json:"spent_today_usdc"`
	Agents         int    `json:"agents"`
}

func (h *Handler) budgetStatus(_ json.RawMessage) (interface{}, error, bool) {
	if h.budget == nil {
		return BudgetStatusResult{}, nil, true
	}
	snap := h.budget.AggregateStatus()
	return BudgetStatusResult{
		PerCallMaxUSDC: snap.PerCallMaxUSDC,
		DailyCapUSDC:   snap.DailyCapUSDC,
		SpentTodayUSDC: snap.SpentTodayUSDC,
		Agents:         snap.Agents,
	}, nil, true
}

// ReceiptsParams is the request shape for x402.receipts. Limit caps
// the number of receipts returned, newest first; 0 or absent means
// return up to a sensible default.
type ReceiptsParams struct {
	Limit int `json:"limit,omitempty"`
}

// ReceiptEntry is one row in the receipts list. The handler joins
// the underlying budget receipt with the catalog's display name so
// the UI does not need a separate lookup.
type ReceiptEntry struct {
	Ts          string `json:"ts"`
	AgentID     string `json:"agent_id"`
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name,omitempty"`
	Path        string `json:"path,omitempty"`
	AmountUSDC  string `json:"amount_usdc"`
	Network     string `json:"network,omitempty"`
	Tx          string `json:"tx,omitempty"`
}

// ReceiptsResult is the response shape for x402.receipts.
type ReceiptsResult struct {
	Receipts []ReceiptEntry `json:"receipts"`
}

func (h *Handler) receipts(params json.RawMessage) (interface{}, error, bool) {
	limit := 50
	if len(params) > 0 {
		var p ReceiptsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err), true
		}
		if p.Limit > 0 && p.Limit <= 500 {
			limit = p.Limit
		}
	}
	if h.budget == nil {
		return ReceiptsResult{}, nil, true
	}
	rec := h.budget.AllReceipts()
	if len(rec) > limit {
		rec = rec[:limit]
	}
	out := make([]ReceiptEntry, 0, len(rec))
	for _, r := range rec {
		entry := ReceiptEntry{
			Ts:         r.Ts.UTC().Format(time.RFC3339),
			AgentID:    r.AgentID,
			ServiceID:  r.ServiceID,
			Path:       r.Path,
			AmountUSDC: r.AmountUSDC,
			Network:    string(r.Network),
			Tx:         r.Tx,
		}
		if m, err := h.registry.Manifest(r.ServiceID); err == nil {
			entry.ServiceName = m.DisplayName
		}
		out = append(out, entry)
	}
	return ReceiptsResult{Receipts: out}, nil, true
}

// ServiceListing is the per-service shape the listServices method
// returns. Carries enough information for the Web UI to render the
// service card without secondary RPC calls.
type ServiceListing struct {
	ID                   string                 `json:"id"`
	DisplayName          string                 `json:"display_name"`
	Description          string                 `json:"description,omitempty"`
	Category             string                 `json:"category,omitempty"`
	Endpoint             string                 `json:"endpoint,omitempty"`
	ServiceURL           string                 `json:"service_url,omitempty"`
	Endpoints            []x402.ServiceEndpoint `json:"endpoints,omitempty"`
	Networks             []x402.Network         `json:"networks,omitempty"`
	MaxPriceUSDC         string                 `json:"max_price_usdc,omitempty"`
	Tier                 x402.Tier              `json:"tier"`
	Hint                 string                 `json:"hint,omitempty"`
	Enabled              bool                   `json:"enabled"`
	ApprovedAt           string                 `json:"approved_at,omitempty"`
	ApprovedMaxPriceUSDC string                 `json:"approved_max_price_usdc,omitempty"`
}

// ListServicesResult is the response shape for x402.listServices.
type ListServicesResult struct {
	Services []ServiceListing `json:"services"`
}

func (h *Handler) listServices(_ json.RawMessage) (interface{}, error, bool) {
	manifests := h.registry.AllManifests()
	out := make([]ServiceListing, 0, len(manifests))
	for _, m := range manifests {
		entry := ServiceListing{
			ID:           m.ID,
			DisplayName:  m.DisplayName,
			Description:  m.Description,
			Category:     m.Category,
			Endpoint:     m.Endpoint,
			ServiceURL:   listingServiceURL(m),
			Endpoints:    listingEndpoints(m),
			Networks:     m.Networks,
			MaxPriceUSDC: m.MaxPriceUSDC,
			Tier:         x402.TierConvenience,
		}
		if p, ok := h.registry.Policy(m.ID); ok {
			entry.Tier = p.Tier
			entry.Hint = p.Hint
		}
		if rec, ok := h.registry.UserEnabled(m.ID); ok {
			entry.Enabled = true
			if !rec.EnabledAt.IsZero() {
				entry.ApprovedAt = rec.EnabledAt.UTC().Format(time.RFC3339)
			}
			entry.ApprovedMaxPriceUSDC = rec.MaxPriceUSDC
		}
		out = append(out, entry)
	}
	return ListServicesResult{Services: out}, nil, true
}

func listingServiceURL(m x402.ServiceManifest) string {
	if home := x402.ServiceHomeURL(m.ServiceURL); home != "" {
		return home
	}
	if home := x402.ServiceHomeURL(m.Endpoint); home != "" {
		return home
	}
	for _, ep := range m.Endpoints {
		if home := x402.ServiceHomeURL(ep.URL); home != "" {
			return home
		}
	}
	return ""
}

func listingEndpoints(m x402.ServiceManifest) []x402.ServiceEndpoint {
	out := make([]x402.ServiceEndpoint, 0, len(m.Endpoints))
	for _, ep := range m.Endpoints {
		if strings.TrimSpace(ep.URL) == "" {
			continue
		}
		out = append(out, ep)
	}
	if len(out) > 0 || strings.TrimSpace(m.Endpoint) == "" {
		return out
	}
	fallback := x402.ServiceEndpoint{
		URL:       m.Endpoint,
		PriceUSDC: m.MaxPriceUSDC,
	}
	if len(m.Networks) > 0 {
		fallback.Network = m.Networks[0]
	}
	return []x402.ServiceEndpoint{fallback}
}

// SetEnabledParams is the request shape for x402.setEnabled.
type SetEnabledParams struct {
	ServiceID    string `json:"service_id"`
	Enabled      bool   `json:"enabled"`
	MaxPriceUSDC string `json:"max_price_usdc,omitempty"`
}

// SetEnabledResult is the response shape for x402.setEnabled.
type SetEnabledResult struct {
	ServiceID string `json:"service_id"`
	Enabled   bool   `json:"enabled"`
}

func (h *Handler) setEnabled(params json.RawMessage) (interface{}, error, bool) {
	var p SetEnabledParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err), true
	}
	if strings.TrimSpace(p.ServiceID) == "" {
		return nil, errors.New("service_id is required"), true
	}
	if p.Enabled {
		if err := h.registry.SetUserEnabled(p.ServiceID, p.MaxPriceUSDC); err != nil {
			return nil, err, true
		}
	} else {
		if err := h.registry.SetUserDisabled(p.ServiceID); err != nil {
			return nil, err, true
		}
	}
	return SetEnabledResult{ServiceID: p.ServiceID, Enabled: p.Enabled}, nil, true
}
