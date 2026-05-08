// envelope: messengers.list_connections
//
// UNTRUSTED INPUT FROM A SANDBOXED AGENT.
// Treat env.Payload as adversarial. Validate every field before use.
// agent_id and device_id are plumbing-stamped and trustworthy.

package messengers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

type listConnectionsResult struct {
	Connections []messaging.Connection `json:"connections"`
}

func (h *handlers) handleListConnections(ctx context.Context, env bridge.Envelope) (json.RawMessage, error) {
	params, err := parseListConnectionsParams(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if err := validateListConnectionsParams(params); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	params.AgentID = env.AgentID
	connections, err := h.backend.ListConnections(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(listConnectionsResult{Connections: connections})
}

func parseListConnectionsParams(raw json.RawMessage) (ListConnectionsParams, error) {
	var p ListConnectionsParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateListConnectionsParams(p ListConnectionsParams) error {
	if strings.ContainsAny(string(p.AdapterID), "\x00\r\n") {
		return fmt.Errorf("adapter_id contains invalid characters")
	}
	return nil
}
