// envelope: x402.budget_status
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.

package x402

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sky10/sky10/pkg/sandbox/comms"
)

type budgetStatusParams struct{}

func (h *handlers) handleBudgetStatus(ctx context.Context, env comms.Envelope) (json.RawMessage, error) {
	if err := parseBudgetStatusParams(env.Payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	snapshot, err := h.backend.BudgetStatus(ctx, env.AgentID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

func parseBudgetStatusParams(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var p budgetStatusParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	return nil
}
