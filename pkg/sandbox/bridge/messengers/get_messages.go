// envelope: messengers.get_messages
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

type getMessagesResult struct {
	Messages []messaging.Message `json:"messages"`
}

func (h *handlers) handleGetMessages(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseGetMessagesParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateGetMessagesParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	messages, err := h.backend.GetMessages(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(getMessagesResult{Messages: messages})
}

func parseGetMessagesParams(raw json.RawMessage) (GetMessagesParams, error) {
	var p GetMessagesParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateGetMessagesParams(p GetMessagesParams) error {
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return errors.New("connection_id is required")
	}
	if strings.TrimSpace(string(p.ConversationID)) == "" {
		return errors.New("conversation_id is required")
	}
	return nil
}
