package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
)

const messagingPolicySourceSettingsUI = "sky10-settings-ui"

type updateConnectionPolicyParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Rules        messaging.PolicyRules  `json:"rules"`
}

type updateConnectionPolicyResult struct {
	Connection messaging.Connection `json:"connection"`
	Policy     messaging.Policy     `json:"policy"`
}

func (h *Handler) rpcUpdateConnectionPolicy(ctx context.Context, params json.RawMessage) (updateConnectionPolicyResult, error) {
	var p updateConnectionPolicyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return updateConnectionPolicyResult{}, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return updateConnectionPolicyResult{}, fmt.Errorf("connection_id is required")
	}
	if err := p.Rules.Validate(); err != nil {
		return updateConnectionPolicyResult{}, err
	}

	connection, ok := h.store.GetConnection(p.ConnectionID)
	if !ok {
		return updateConnectionPolicyResult{}, fmt.Errorf("messaging connection %s not found", p.ConnectionID)
	}
	if connection.DefaultPolicyID == "" {
		configured, err := h.afterConnectionConfigured(ctx, connection)
		if err != nil {
			return updateConnectionPolicyResult{}, err
		}
		connection = configured
	}
	if connection.DefaultPolicyID == "" {
		connection.DefaultPolicyID = fallbackDefaultMessagingPolicyID(connection.ID)
		if err := h.store.PutConnection(ctx, connection); err != nil {
			return updateConnectionPolicyResult{}, err
		}
	}

	policy, ok := h.store.GetPolicy(connection.DefaultPolicyID)
	if !ok {
		policy = messaging.Policy{
			ID:   connection.DefaultPolicyID,
			Name: defaultPolicyName(connection),
		}
	}
	policy.Rules = p.Rules
	policy.Metadata = cloneRPCStringMap(policy.Metadata)
	if policy.Metadata == nil {
		policy.Metadata = make(map[string]string)
	}
	policy.Metadata["source"] = messagingPolicySourceSettingsUI
	if strings.TrimSpace(policy.Name) == "" {
		policy.Name = defaultPolicyName(connection)
	}
	if err := h.store.PutPolicy(ctx, policy); err != nil {
		return updateConnectionPolicyResult{}, err
	}

	return updateConnectionPolicyResult{
		Connection: connection,
		Policy:     policy,
	}, nil
}

func fallbackDefaultMessagingPolicyID(connectionID messaging.ConnectionID) messaging.PolicyID {
	return messaging.PolicyID("policy/messaging/default-runtime/" + string(connectionID))
}

func defaultPolicyName(connection messaging.Connection) string {
	label := strings.TrimSpace(connection.Label)
	if label == "" {
		return "Default runtime messaging access"
	}
	return label + " messaging access"
}
