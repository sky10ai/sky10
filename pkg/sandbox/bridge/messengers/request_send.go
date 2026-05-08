// envelope: messengers.request_send
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

func (h *handlers) handleRequestSend(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseRequestSendParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateRequestSendParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	result, err := h.backend.RequestSend(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func parseRequestSendParams(raw json.RawMessage) (RequestSendParams, error) {
	var p RequestSendParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateRequestSendParams(p RequestSendParams) error {
	if strings.TrimSpace(string(p.DraftID)) == "" {
		return errors.New("draft_id is required")
	}
	return nil
}
