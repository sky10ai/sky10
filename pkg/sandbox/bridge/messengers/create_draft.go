// envelope: messengers.create_draft
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

	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func (h *handlers) handleCreateDraft(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseCreateDraftParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateCreateDraftParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	result, err := h.backend.CreateDraft(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func parseCreateDraftParams(raw json.RawMessage) (CreateDraftParams, error) {
	var p CreateDraftParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateCreateDraftParams(p CreateDraftParams) error {
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return errors.New("connection_id is required")
	}
	if strings.TrimSpace(string(p.ConversationID)) == "" {
		return errors.New("conversation_id is required")
	}
	if len(p.Parts) == 0 {
		return errors.New("parts are required")
	}
	for idx, part := range p.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("parts[%d]: %w", idx, err)
		}
	}
	return nil
}
