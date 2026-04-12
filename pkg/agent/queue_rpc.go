package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

type queueDiscoverParams struct {
	Skill string `json:"skill,omitempty"`
	Queue string `json:"queue,omitempty"`
}

type queueClaimParams struct {
	Offer      agentmailbox.QueueOffer `json:"offer"`
	ActorID    string                  `json:"actor_id"`
	TTLSeconds int                     `json:"ttl_seconds,omitempty"`
}

func (h *RPCHandler) rpcQueueDiscover(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.router == nil {
		return nil, fmt.Errorf("public queue not configured")
	}

	var p queueDiscoverParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
	}

	offers, err := h.router.DiscoverPublicQueue(ctx, p.Skill, p.Queue)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"offers": offers,
		"count":  len(offers),
	}, nil
}

func (h *RPCHandler) rpcQueueClaim(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.router == nil {
		return nil, fmt.Errorf("public queue not configured")
	}

	var p queueClaimParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(p.ActorID) == "" {
		return nil, fmt.Errorf("actor_id is required")
	}

	claim, err := h.router.ClaimPublicQueue(ctx, p.Offer, agentmailbox.Principal{
		ID:        strings.TrimSpace(p.ActorID),
		Kind:      agentmailbox.PrincipalKindNetworkAgent,
		Scope:     agentmailbox.ScopeSky10Network,
		RouteHint: h.defaultRouteHint(),
	}, time.Duration(p.TTLSeconds)*time.Second)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"claim":  claim,
		"status": "claimed",
	}, nil
}
