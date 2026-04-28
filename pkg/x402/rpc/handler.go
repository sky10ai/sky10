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

	"github.com/sky10/sky10/pkg/x402"
)

// Handler dispatches x402.* RPC methods.
type Handler struct {
	registry *x402.Registry
}

// NewHandler constructs a Handler. registry must be the same
// pkg/x402.Registry instance the daemon's comms-side x402 endpoint
// uses, so changes here affect agent-facing state immediately.
func NewHandler(registry *x402.Registry) *Handler {
	return &Handler{registry: registry}
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
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

// ServiceListing is the per-service shape the listServices method
// returns. Carries enough information for the Web UI to render the
// service card without secondary RPC calls.
type ServiceListing struct {
	ID           string         `json:"id"`
	DisplayName  string         `json:"display_name"`
	Description  string         `json:"description,omitempty"`
	Category     string         `json:"category,omitempty"`
	Networks     []x402.Network `json:"networks,omitempty"`
	MaxPriceUSDC string         `json:"max_price_usdc,omitempty"`
	Tier         x402.Tier      `json:"tier"`
	Hint         string         `json:"hint,omitempty"`
	Enabled      bool           `json:"enabled"`
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
			Networks:     m.Networks,
			MaxPriceUSDC: m.MaxPriceUSDC,
			Tier:         x402.TierConvenience,
		}
		if p, ok := h.registry.Policy(m.ID); ok {
			entry.Tier = p.Tier
			entry.Hint = p.Hint
		}
		if _, ok := h.registry.UserEnabled(m.ID); ok {
			entry.Enabled = true
		}
		out = append(out, entry)
	}
	return ListServicesResult{Services: out}, nil, true
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
