package broker

import (
	"fmt"

	"github.com/sky10/sky10/pkg/messaging"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
)

// EffectivePolicy is the broker's resolved policy context for one connection
// and optional exposure.
type EffectivePolicy struct {
	Connection messaging.Connection `json:"connection"`
	Exposure   *messaging.Exposure  `json:"exposure,omitempty"`
	Policy     messaging.Policy     `json:"policy"`
}

// ResolvePolicy chooses the effective policy for one connection and optional
// exposure. Exposure-specific policy overrides the connection default when set.
func (b *Broker) ResolvePolicy(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) (EffectivePolicy, error) {
	connection, ok := b.store.GetConnection(connectionID)
	if !ok {
		return EffectivePolicy{}, fmt.Errorf("messaging connection %s not found", connectionID)
	}

	var exposure *messaging.Exposure
	if exposureID != "" {
		storedExposure, ok := b.store.GetExposure(exposureID)
		if !ok {
			return EffectivePolicy{}, fmt.Errorf("messaging exposure %s not found", exposureID)
		}
		if !storedExposure.Enabled {
			return EffectivePolicy{}, fmt.Errorf("messaging exposure %s is disabled", exposureID)
		}
		if storedExposure.ConnectionID != connectionID {
			return EffectivePolicy{}, fmt.Errorf("messaging exposure %s does not belong to connection %s", exposureID, connectionID)
		}
		exposure = &storedExposure
	}

	policyID := connection.DefaultPolicyID
	if exposure != nil && exposure.PolicyID != "" {
		policyID = exposure.PolicyID
	}
	if policyID == "" {
		return EffectivePolicy{}, fmt.Errorf("no policy configured for connection %s", connectionID)
	}
	policy, ok := b.store.GetPolicy(policyID)
	if !ok {
		return EffectivePolicy{}, fmt.Errorf("messaging policy %s not found", policyID)
	}
	return EffectivePolicy{
		Connection: connection,
		Exposure:   exposure,
		Policy:     policy,
	}, nil
}

// EvaluateReadInbound evaluates whether the effective policy allows reading
// inbound content.
func (b *Broker) EvaluateReadInbound(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) (messagingpolicy.Decision, error) {
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return messagingpolicy.Decision{}, err
	}
	return messagingpolicy.ReadInbound(effective.Policy), nil
}

// EvaluateCreateDraft evaluates draft creation/update under the effective
// policy.
func (b *Broker) EvaluateCreateDraft(connectionID messaging.ConnectionID, exposureID messaging.ExposureID, draft messaging.Draft) (messagingpolicy.Decision, error) {
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return messagingpolicy.Decision{}, err
	}
	return messagingpolicy.CreateDraft(effective.Policy, messagingpolicy.DraftInput{
		RequestedIdentityID: draft.LocalIdentityID,
		HasAttachments:      hasAttachmentParts(draft.Parts),
	}), nil
}

// EvaluateSend evaluates outbound send policy for a draft.
func (b *Broker) EvaluateSend(connectionID messaging.ConnectionID, exposureID messaging.ExposureID, draft messaging.Draft, newConversation bool) (messagingpolicy.Decision, error) {
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return messagingpolicy.Decision{}, err
	}
	return messagingpolicy.SendMessage(effective.Policy, messagingpolicy.SendInput{
		RequestedIdentityID: draft.LocalIdentityID,
		HasAttachments:      hasAttachmentParts(draft.Parts),
		NewConversation:     newConversation,
	}), nil
}

// EvaluateMarkRead evaluates whether read-state mutations are allowed.
func (b *Broker) EvaluateMarkRead(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) (messagingpolicy.Decision, error) {
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return messagingpolicy.Decision{}, err
	}
	return messagingpolicy.MarkRead(effective.Policy), nil
}

// EvaluateSearch evaluates one search operation under the effective policy.
func (b *Broker) EvaluateSearch(connectionID messaging.ConnectionID, exposureID messaging.ExposureID, scope messagingpolicy.SearchScope) (messagingpolicy.Decision, error) {
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return messagingpolicy.Decision{}, err
	}
	return messagingpolicy.Search(effective.Policy, scope), nil
}

func hasAttachmentParts(parts []messaging.MessagePart) bool {
	for _, part := range parts {
		switch part.Kind {
		case messaging.MessagePartKindFile, messaging.MessagePartKindImage:
			return true
		}
	}
	return false
}
