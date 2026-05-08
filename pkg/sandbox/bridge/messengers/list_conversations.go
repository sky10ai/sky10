// envelope: messengers.list_conversations
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

type listConversationsResult struct {
	Conversations []messaging.Conversation `json:"conversations"`
}

func (h *handlers) handleListConversations(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseListConversationsParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateListConversationsParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	conversations, err := h.backend.ListConversations(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(listConversationsResult{Conversations: conversations})
}

func parseListConversationsParams(raw json.RawMessage) (ListConversationsParams, error) {
	var p ListConversationsParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateListConversationsParams(p ListConversationsParams) error {
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return errors.New("connection_id is required")
	}
	return nil
}
