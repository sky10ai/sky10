// envelope: x402.list_services
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.

package x402

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

type listServicesParams struct {
	// Optional category filter; empty means "all approved services".
	Category string `json:"category,omitempty"`
}

type listServicesResult struct {
	Services []ServiceListing `json:"services"`
}

func (h *handlers) handleListServices(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseListServicesParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	services, err := h.backend.ListServices(ctx, env.AgentID)
	if err != nil {
		return nil, err
	}
	if params.Category != "" {
		filtered := services[:0]
		for _, s := range services {
			if s.Category == params.Category {
				filtered = append(filtered, s)
			}
		}
		services = filtered
	}
	return json.Marshal(listServicesResult{Services: services})
}

func parseListServicesParams(raw json.RawMessage) (listServicesParams, error) {
	var p listServicesParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}
