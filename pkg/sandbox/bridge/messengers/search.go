// envelope: messengers.search_conversations, messengers.search_messages
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

	"github.com/sky10/sky10/pkg/messaging/protocol"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const maxSearchLimit = 100

func (h *handlers) handleSearchConversations(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseSearchConversationsParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateSearchConversationsParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	result, err := h.backend.SearchConversations(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func parseSearchConversationsParams(raw json.RawMessage) (SearchConversationsParams, error) {
	var p SearchConversationsParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateSearchConversationsParams(p SearchConversationsParams) error {
	if err := validateSearchCommon(string(p.ConnectionID), p.Query, p.Source, p.Limit); err != nil {
		return err
	}
	return nil
}

func (h *handlers) handleSearchMessages(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseSearchMessagesParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateSearchMessagesParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	result, err := h.backend.SearchMessages(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func parseSearchMessagesParams(raw json.RawMessage) (SearchMessagesParams, error) {
	var p SearchMessagesParams
	if len(raw) == 0 {
		return p, errors.New("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateSearchMessagesParams(p SearchMessagesParams) error {
	return validateSearchCommon(string(p.ConnectionID), p.Query, p.Source, p.Limit)
}

func validateSearchCommon(connectionID string, query string, source protocol.SearchSource, limit int) error {
	if strings.TrimSpace(connectionID) == "" {
		return errors.New("connection_id is required")
	}
	if strings.TrimSpace(query) == "" {
		return errors.New("query is required")
	}
	switch source {
	case "", protocol.SearchSourceIndexed, protocol.SearchSourceRemote:
	default:
		return fmt.Errorf("source must be %q or %q", protocol.SearchSourceIndexed, protocol.SearchSourceRemote)
	}
	if limit < 0 || limit > maxSearchLimit {
		return fmt.Errorf("limit must be between 0 and %d", maxSearchLimit)
	}
	return nil
}
