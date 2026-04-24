package policy

import (
	"fmt"

	"github.com/sky10/sky10/pkg/messaging"
)

// Outcome is the result of evaluating one policy-controlled operation.
type Outcome string

const (
	OutcomeAllow           Outcome = "allow"
	OutcomeRequireApproval Outcome = "require_approval"
	OutcomeDeny            Outcome = "deny"
)

// SearchScope identifies one searchable messaging surface.
type SearchScope string

const (
	SearchScopeIdentities    SearchScope = "identities"
	SearchScopeConversations SearchScope = "conversations"
	SearchScopeMessages      SearchScope = "messages"
)

// Decision is one policy evaluation result.
type Decision struct {
	Outcome  Outcome            `json:"outcome"`
	PolicyID messaging.PolicyID `json:"policy_id,omitempty"`
	Reason   string             `json:"reason,omitempty"`
}

// Allowed reports whether the operation may continue at all.
func (d Decision) Allowed() bool {
	return d.Outcome == OutcomeAllow || d.Outcome == OutcomeRequireApproval
}

// RequiresApproval reports whether the operation is allowed only after
// operator approval.
func (d Decision) RequiresApproval() bool {
	return d.Outcome == OutcomeRequireApproval
}

// DraftInput is the policy context for draft creation/update.
type DraftInput struct {
	RequestedIdentityID messaging.IdentityID `json:"requested_identity_id,omitempty"`
	HasAttachments      bool                 `json:"has_attachments,omitempty"`
}

// SendInput is the policy context for outbound send evaluation.
type SendInput struct {
	RequestedIdentityID messaging.IdentityID `json:"requested_identity_id,omitempty"`
	HasAttachments      bool                 `json:"has_attachments,omitempty"`
	NewConversation     bool                 `json:"new_conversation,omitempty"`
}

// ManageInput is the policy context for provider-side message management.
type ManageInput struct {
	DestinationContainerID messaging.ContainerID   `json:"destination_container_id,omitempty"`
	AddContainerIDs        []messaging.ContainerID `json:"add_container_ids,omitempty"`
	RemoveContainerIDs     []messaging.ContainerID `json:"remove_container_ids,omitempty"`
}

// ReadInbound evaluates whether a subject may read inbound content under the
// given policy.
func ReadInbound(policy messaging.Policy) Decision {
	if !policy.Rules.ReadInbound {
		return deny(policy.ID, "policy does not allow reading inbound messages")
	}
	return allow(policy.ID)
}

// CreateDraft evaluates draft creation/update under the given policy.
func CreateDraft(policy messaging.Policy, input DraftInput) Decision {
	if !policy.Rules.CreateDrafts {
		return deny(policy.ID, "policy does not allow draft creation")
	}
	if denied := validateIdentity(policy, input.RequestedIdentityID); denied.Reason != "" {
		return denied
	}
	if input.HasAttachments && !policy.Rules.AllowAttachments {
		return deny(policy.ID, "policy does not allow attachments")
	}
	return allow(policy.ID)
}

// SendMessage evaluates outbound send permissions under the given policy.
func SendMessage(policy messaging.Policy, input SendInput) Decision {
	if !policy.Rules.SendMessages {
		return deny(policy.ID, "policy does not allow sending messages")
	}
	if denied := validateIdentity(policy, input.RequestedIdentityID); denied.Reason != "" {
		return denied
	}
	if input.HasAttachments && !policy.Rules.AllowAttachments {
		return deny(policy.ID, "policy does not allow attachments")
	}
	if input.NewConversation && policy.Rules.ReplyOnly {
		return deny(policy.ID, "policy only allows replies in existing conversations")
	}
	if input.NewConversation && !policy.Rules.AllowNewConversations {
		return deny(policy.ID, "policy does not allow starting new conversations")
	}
	if policy.Rules.RequireApproval {
		return requireApproval(policy.ID, "policy requires approval before sending")
	}
	return allow(policy.ID)
}

// MarkRead evaluates whether read/seen state updates are allowed.
func MarkRead(policy messaging.Policy) Decision {
	if !policy.Rules.MarkRead {
		return deny(policy.ID, "policy does not allow marking messages read")
	}
	return allow(policy.ID)
}

// ManageMessages evaluates whether provider-side message management operations
// such as move, archive, and label mutation are allowed.
func ManageMessages(policy messaging.Policy, input ManageInput) Decision {
	if !policy.Rules.ManageMessages {
		return deny(policy.ID, "policy does not allow message management")
	}
	for _, containerID := range managedContainerIDs(input) {
		if denied := validateContainer(policy, containerID); denied.Reason != "" {
			return denied
		}
	}
	return allow(policy.ID)
}

// Search evaluates one search operation under the given policy.
func Search(policy messaging.Policy, scope SearchScope) Decision {
	switch scope {
	case SearchScopeIdentities:
		if !policy.Rules.SearchIdentities {
			return deny(policy.ID, "policy does not allow identity search")
		}
	case SearchScopeConversations:
		if !policy.Rules.SearchConversations {
			return deny(policy.ID, "policy does not allow conversation search")
		}
	case SearchScopeMessages:
		if !policy.Rules.SearchMessages {
			return deny(policy.ID, "policy does not allow message search")
		}
	default:
		return deny(policy.ID, fmt.Sprintf("unsupported search scope %q", scope))
	}
	return allow(policy.ID)
}

func validateIdentity(policy messaging.Policy, requested messaging.IdentityID) Decision {
	if len(policy.Rules.AllowedIdentityIDs) == 0 || requested == "" {
		return Decision{}
	}
	for _, allowed := range policy.Rules.AllowedIdentityIDs {
		if allowed == requested {
			return Decision{}
		}
	}
	return deny(policy.ID, fmt.Sprintf("policy does not allow identity %q", requested))
}

func validateContainer(policy messaging.Policy, requested messaging.ContainerID) Decision {
	if len(policy.Rules.AllowedContainerIDs) == 0 || requested == "" {
		return Decision{}
	}
	for _, allowed := range policy.Rules.AllowedContainerIDs {
		if allowed == requested {
			return Decision{}
		}
	}
	return deny(policy.ID, fmt.Sprintf("policy does not allow container %q", requested))
}

func managedContainerIDs(input ManageInput) []messaging.ContainerID {
	ids := make([]messaging.ContainerID, 0, 1+len(input.AddContainerIDs)+len(input.RemoveContainerIDs))
	if input.DestinationContainerID != "" {
		ids = append(ids, input.DestinationContainerID)
	}
	ids = append(ids, input.AddContainerIDs...)
	ids = append(ids, input.RemoveContainerIDs...)
	return ids
}

func allow(policyID messaging.PolicyID) Decision {
	return Decision{Outcome: OutcomeAllow, PolicyID: policyID}
}

func requireApproval(policyID messaging.PolicyID, reason string) Decision {
	return Decision{Outcome: OutcomeRequireApproval, PolicyID: policyID, Reason: reason}
}

func deny(policyID messaging.PolicyID, reason string) Decision {
	return Decision{Outcome: OutcomeDeny, PolicyID: policyID, Reason: reason}
}
