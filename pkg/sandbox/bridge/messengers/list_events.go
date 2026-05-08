// envelope: messengers.list_events
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.

package messengers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const maxListEventsLimit = 500

type listEventsResult struct {
	Events []messaging.Event `json:"events"`
}

func (h *handlers) handleListEvents(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseListEventsParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateListEventsParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	events, err := h.backend.ListEvents(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(listEventsResult{Events: events})
}

func parseListEventsParams(raw json.RawMessage) (ListEventsParams, error) {
	var p ListEventsParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateListEventsParams(p ListEventsParams) error {
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return errors.New("connection_id is required")
	}
	if p.Limit < 0 || p.Limit > maxListEventsLimit {
		return fmt.Errorf("limit must be between 0 and %d", maxListEventsLimit)
	}
	return nil
}
