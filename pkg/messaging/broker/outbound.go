package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/messaging"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

// ApprovalMailbox is the subset of mailbox approval operations the broker uses
// to mirror messaging approvals into the repo's durable mailbox workflow when
// configured.
type ApprovalMailbox interface {
	CreateApprovalRequest(ctx context.Context, item agentmailbox.Item, payload agentmailbox.ApprovalRequestPayload) (agentmailbox.Record, error)
	Approve(ctx context.Context, itemID string, actor agentmailbox.Principal, decisionID string) (agentmailbox.Record, error)
	Reject(ctx context.Context, itemID string, actor agentmailbox.Principal, decisionID string) (agentmailbox.Record, error)
}

// DraftMutationResult reports the persisted draft/workflow state after create
// or update.
type DraftMutationResult struct {
	Draft    messaging.Draft    `json:"draft"`
	Workflow messaging.Workflow `json:"workflow"`
}

// RequestSendDraftResult reports the persisted state after a send request. The
// request may produce an approval requirement, or it may send immediately.
type RequestSendDraftResult struct {
	Draft    messaging.Draft     `json:"draft"`
	Workflow messaging.Workflow  `json:"workflow"`
	Approval *messaging.Approval `json:"approval,omitempty"`
	Message  *messaging.Message  `json:"message,omitempty"`
}

// ApprovalDecisionResult reports the persisted state after an approval
// decision.
type ApprovalDecisionResult struct {
	Approval messaging.Approval `json:"approval"`
	Draft    messaging.Draft    `json:"draft"`
	Workflow messaging.Workflow `json:"workflow"`
}

// CreateDraft validates and persists one broker-owned draft, optionally asking
// the adapter to normalize it when the adapter supports native draft handling.
func (b *Broker) CreateDraft(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft) (DraftMutationResult, error) {
	return b.saveDraft(ctx, exposureID, draft, false)
}

// UpdateDraft updates an existing draft and invalidates any prior approval so
// content changes cannot silently reuse an old decision.
func (b *Broker) UpdateDraft(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft) (DraftMutationResult, error) {
	return b.saveDraft(ctx, exposureID, draft, true)
}

// RequestSendDraft attempts to send a draft through the broker. If policy
// requires approval, it creates a durable approval request instead.
func (b *Broker) RequestSendDraft(ctx context.Context, exposureID messaging.ExposureID, draftID messaging.DraftID, newConversation bool) (RequestSendDraftResult, error) {
	draft, ok := b.store.GetDraft(draftID)
	if !ok {
		return RequestSendDraftResult{}, fmt.Errorf("messaging draft %s not found", draftID)
	}
	workflow, err := b.ensureDraftWorkflow(ctx, exposureID, draft, "")
	if err != nil {
		return RequestSendDraftResult{}, err
	}
	if draft.Status == messaging.DraftStatusSent && workflow.OutboundMessageID != "" {
		storedMessage, ok := b.store.GetMessage(workflow.OutboundMessageID)
		if ok {
			var approval *messaging.Approval
			if workflow.ApprovalID != "" {
				if storedApproval, ok := b.store.GetApproval(workflow.ApprovalID); ok {
					approval = &storedApproval
				}
			}
			return RequestSendDraftResult{
				Draft:    draft,
				Workflow: workflow,
				Approval: approval,
				Message:  &storedMessage,
			}, nil
		}
	}
	decision, err := b.EvaluateSend(draft.ConnectionID, exposureID, draft, newConversation)
	if err != nil {
		return RequestSendDraftResult{}, err
	}

	latestApproval, hasApproval := b.latestDraftApproval(draft.ID)
	if decision.Outcome == messagingpolicy.OutcomeDeny {
		now := b.now()
		workflow.Status = messaging.WorkflowStatusFailed
		workflow.LastActivityAt = now
		workflow.NeedsAttention = true
		workflow.AttentionReason = "policy_denied"
		workflow.Error = decision.Reason
		if err := b.store.PutWorkflow(ctx, workflow); err != nil {
			return RequestSendDraftResult{}, err
		}
		if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeApprovalResolved, draft, exposureID, map[string]string{
			"outcome": "deny",
			"reason":  decision.Reason,
		}, decision.Reason); err != nil {
			return RequestSendDraftResult{}, err
		}
		return RequestSendDraftResult{}, fmt.Errorf("send denied by policy: %s", decision.Reason)
	}

	if decision.RequiresApproval() {
		if hasApproval {
			switch latestApproval.Status {
			case messaging.ApprovalStatusPending:
				draft.Status = messaging.DraftStatusApprovalRequired
				draft.ApprovalID = latestApproval.ID
				if err := b.store.PutDraft(ctx, draft); err != nil {
					return RequestSendDraftResult{}, err
				}
				workflow.Status = messaging.WorkflowStatusAwaitingApproval
				workflow.ApprovalID = latestApproval.ID
				workflow.LastActivityAt = b.now()
				if workflow.SendRequestedAt == nil {
					now := b.now()
					workflow.SendRequestedAt = &now
				}
				if err := b.store.PutWorkflow(ctx, workflow); err != nil {
					return RequestSendDraftResult{}, err
				}
				return RequestSendDraftResult{
					Draft:    draft,
					Workflow: workflow,
					Approval: &latestApproval,
				}, nil
			case messaging.ApprovalStatusApproved:
				// Approval already exists; proceed to send below.
			case messaging.ApprovalStatusRejected, messaging.ApprovalStatusCancelled, messaging.ApprovalStatusExpired:
				// A new send request should create a new approval record.
			}
		} else {
			latestApproval.Status = ""
		}

		if latestApproval.Status != messaging.ApprovalStatusApproved {
			approval, updatedDraft, updatedWorkflow, err := b.createSendApproval(ctx, exposureID, workflow, draft, decision)
			if err != nil {
				return RequestSendDraftResult{}, err
			}
			return RequestSendDraftResult{
				Draft:    updatedDraft,
				Workflow: updatedWorkflow,
				Approval: &approval,
			}, nil
		}
	}

	message, updatedDraft, updatedWorkflow, err := b.sendDraftNow(ctx, exposureID, workflow, draft, newConversation)
	if err != nil {
		return RequestSendDraftResult{}, err
	}
	return RequestSendDraftResult{
		Draft:    updatedDraft,
		Workflow: updatedWorkflow,
		Message:  &message,
	}, nil
}

// ApproveDraftSend records an approval decision for a pending send request.
func (b *Broker) ApproveDraftSend(ctx context.Context, approvalID messaging.ApprovalID, actorID string) (ApprovalDecisionResult, error) {
	return b.resolveApproval(ctx, approvalID, actorID, true, "")
}

// RejectDraftSend records a rejection for a pending send request.
func (b *Broker) RejectDraftSend(ctx context.Context, approvalID messaging.ApprovalID, actorID, reason string) (ApprovalDecisionResult, error) {
	return b.resolveApproval(ctx, approvalID, actorID, false, reason)
}

func (b *Broker) saveDraft(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft, invalidatePriorApproval bool) (DraftMutationResult, error) {
	decision, err := b.EvaluateCreateDraft(draft.ConnectionID, exposureID, draft)
	if err != nil {
		return DraftMutationResult{}, err
	}
	if !decision.Allowed() {
		return DraftMutationResult{}, fmt.Errorf("draft denied by policy: %s", decision.Reason)
	}

	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, draft.ConnectionID)
	if err != nil {
		return DraftMutationResult{}, err
	}

	storedDraft := cloneDraft(draft)
	storedDraft.Status = messaging.DraftStatusPending

	if invalidatePriorApproval {
		if existing, ok := b.store.GetDraft(storedDraft.ID); ok && existing.ApprovalID != "" {
			if err := b.cancelApproval(ctx, existing.ApprovalID); err != nil {
				return DraftMutationResult{}, err
			}
			storedDraft.ApprovalID = ""
		}
	}

	record := protocol.DraftRecord{
		Draft:       storedDraft,
		Attachments: attachmentsFromParts(storedDraft.Parts),
	}
	if invalidatePriorApproval && describe.Adapter.Capabilities.UpdateDrafts {
		result, err := adapterClient.UpdateDraft(ctx, protocol.UpdateDraftParams{Draft: record})
		if err != nil {
			var protocolErr *protocol.Error
			if !errors.As(err, &protocolErr) || protocolErr.Code != protocol.ProtocolErrorNotSupported {
				return DraftMutationResult{}, err
			}
		} else {
			storedDraft = cloneDraft(result.Draft.Draft)
		}
	} else if describe.Adapter.Capabilities.CreateDrafts {
		result, err := adapterClient.CreateDraft(ctx, protocol.CreateDraftParams{Draft: record})
		if err != nil {
			var protocolErr *protocol.Error
			if !errors.As(err, &protocolErr) || protocolErr.Code != protocol.ProtocolErrorNotSupported {
				return DraftMutationResult{}, err
			}
		} else {
			storedDraft = cloneDraft(result.Draft.Draft)
		}
	}

	if err := b.store.PutDraft(ctx, storedDraft); err != nil {
		return DraftMutationResult{}, err
	}

	effective, err := b.ResolvePolicy(storedDraft.ConnectionID, exposureID)
	if err != nil {
		return DraftMutationResult{}, err
	}
	workflow, err := b.ensureDraftWorkflow(ctx, exposureID, storedDraft, effective.Policy.ID)
	if err != nil {
		return DraftMutationResult{}, err
	}
	now := b.now()
	workflow.Status = messaging.WorkflowStatusDrafted
	workflow.PolicyID = effective.Policy.ID
	workflow.ExposureID = exposureID
	workflow.DraftID = storedDraft.ID
	workflow.ApprovalID = ""
	workflow.Error = ""
	workflow.NeedsAttention = false
	workflow.AttentionReason = ""
	workflow.LastActivityAt = now
	if workflow.DraftCreatedAt == nil {
		workflow.DraftCreatedAt = &now
	}
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return DraftMutationResult{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeDraftUpdated,
		ConnectionID: storedDraft.ConnectionID,
		DraftID:      storedDraft.ID,
		ExposureID:   exposureID,
		Timestamp:    now,
	}); err != nil {
		return DraftMutationResult{}, err
	}
	if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeDraftUpdated, storedDraft, exposureID, nil, ""); err != nil {
		return DraftMutationResult{}, err
	}
	return DraftMutationResult{
		Draft:    storedDraft,
		Workflow: workflow,
	}, nil
}

func (b *Broker) createSendApproval(ctx context.Context, exposureID messaging.ExposureID, workflow messaging.Workflow, draft messaging.Draft, decision messagingpolicy.Decision) (messaging.Approval, messaging.Draft, messaging.Workflow, error) {
	now := b.now()
	approval := messaging.Approval{
		ID:           messaging.ApprovalID(b.newID()),
		ConnectionID: draft.ConnectionID,
		DraftID:      draft.ID,
		WorkflowID:   workflow.ID,
		PolicyID:     decision.PolicyID,
		ExposureID:   exposureID,
		Action:       "send_draft",
		Summary:      draftApprovalSummary(workflow, draft),
		Reason:       decision.Reason,
		Status:       messaging.ApprovalStatusPending,
		RequestedBy:  requestedBy(b.store, exposureID),
		RequestedAt:  now,
	}

	if b.approvalMailbox != nil && b.approvalTo != nil {
		itemID, err := b.createMailboxApproval(ctx, approval, workflow, draft)
		if err != nil {
			return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
		}
		approval.MailboxItemID = itemID
	}

	draft.Status = messaging.DraftStatusApprovalRequired
	draft.ApprovalID = approval.ID
	if err := b.store.PutApproval(ctx, approval); err != nil {
		return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.store.PutDraft(ctx, draft); err != nil {
		return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
	}

	workflow.Status = messaging.WorkflowStatusAwaitingApproval
	workflow.PolicyID = decision.PolicyID
	workflow.ExposureID = exposureID
	workflow.ApprovalID = approval.ID
	workflow.LastActivityAt = now
	if workflow.SendRequestedAt == nil {
		workflow.SendRequestedAt = &now
	}
	if approval.MailboxItemID != "" {
		workflow.OperatorNotifiedAt = &now
	}
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeApprovalRequired,
		ConnectionID: draft.ConnectionID,
		DraftID:      draft.ID,
		ExposureID:   exposureID,
		Timestamp:    now,
		Metadata: map[string]string{
			"approval_id": string(approval.ID),
		},
	}); err != nil {
		return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeApprovalRequired, draft, exposureID, map[string]string{
		"approval_id": string(approval.ID),
	}, ""); err != nil {
		return messaging.Approval{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	return approval, draft, workflow, nil
}

func (b *Broker) sendDraftNow(ctx context.Context, exposureID messaging.ExposureID, workflow messaging.Workflow, draft messaging.Draft, newConversation bool) (messaging.Message, messaging.Draft, messaging.Workflow, error) {
	_, adapterClient, _, _, err := b.prepareAdapterCall(ctx, draft.ConnectionID)
	if err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}

	now := b.now()
	draft.Status = messaging.DraftStatusSending
	if err := b.store.PutDraft(ctx, draft); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	workflow.Status = messaging.WorkflowStatusSending
	workflow.LastActivityAt = now
	if workflow.SendRequestedAt == nil {
		workflow.SendRequestedAt = &now
	}
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeDraftSendRequested,
		ConnectionID: draft.ConnectionID,
		DraftID:      draft.ID,
		ExposureID:   exposureID,
		Timestamp:    now,
	}); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeDraftSendRequested, draft, exposureID, nil, ""); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}

	record := protocol.DraftRecord{
		Draft:       draft,
		Attachments: attachmentsFromParts(draft.Parts),
	}
	var sendResult protocol.SendResult
	if strings.TrimSpace(draft.ReplyToRemoteID) != "" {
		sendResult, err = adapterClient.ReplyMessage(ctx, protocol.ReplyMessageParams{
			Draft:           record,
			ReplyToRemoteID: draft.ReplyToRemoteID,
			SendOptions: protocol.SendOptions{
				IdempotencyKey: string(draft.ID),
			},
		})
	} else {
		sendResult, err = adapterClient.SendMessage(ctx, protocol.SendMessageParams{
			Draft: record,
			SendOptions: protocol.SendOptions{
				IdempotencyKey: string(draft.ID),
			},
		})
	}
	if err != nil {
		failNow := b.now()
		draft.Status = messaging.DraftStatusFailed
		workflow.Status = messaging.WorkflowStatusFailed
		workflow.LastActivityAt = failNow
		workflow.NeedsAttention = true
		workflow.AttentionReason = "send_failed"
		workflow.Error = err.Error()
		_ = b.store.PutDraft(ctx, draft)
		_ = b.store.PutWorkflow(ctx, workflow)
		_ = b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeDeliveryUpdated, draft, exposureID, map[string]string{
			"status": string(messaging.MessageStatusFailed),
		}, err.Error())
		return messaging.Message{}, draft, workflow, err
	}

	message := normalizeOutboundMessage(sendResult, draft, b.now())
	if err := b.store.PutMessage(ctx, message); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	draft.Status = messaging.DraftStatusSent
	if err := b.store.PutDraft(ctx, draft); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}

	sentAt := message.CreatedAt
	workflow.Status = messaging.WorkflowStatusSent
	workflow.OutboundMessageID = message.ID
	workflow.LastActivityAt = sentAt
	workflow.SourceSentAt = &sentAt
	workflow.FulfilledAt = &sentAt
	workflow.NeedsAttention = false
	workflow.AttentionReason = ""
	workflow.Error = ""
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:             messaging.EventID(b.newID()),
		Type:           messaging.EventTypeDeliveryUpdated,
		ConnectionID:   draft.ConnectionID,
		ConversationID: draft.ConversationID,
		MessageID:      message.ID,
		DraftID:        draft.ID,
		ExposureID:     exposureID,
		Timestamp:      sentAt,
		Metadata: map[string]string{
			"status": string(message.Status),
		},
	}); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeDeliveryUpdated, draft, exposureID, map[string]string{
		"status": string(message.Status),
	}, ""); err != nil {
		return messaging.Message{}, messaging.Draft{}, messaging.Workflow{}, err
	}
	return message, draft, workflow, nil
}

func (b *Broker) resolveApproval(ctx context.Context, approvalID messaging.ApprovalID, actorID string, approved bool, reason string) (ApprovalDecisionResult, error) {
	approval, ok := b.store.GetApproval(approvalID)
	if !ok {
		return ApprovalDecisionResult{}, fmt.Errorf("messaging approval %s not found", approvalID)
	}
	draft, ok := b.store.GetDraft(approval.DraftID)
	if !ok {
		return ApprovalDecisionResult{}, fmt.Errorf("messaging draft %s not found", approval.DraftID)
	}
	workflow, ok := b.store.GetWorkflow(approval.WorkflowID)
	if !ok {
		return ApprovalDecisionResult{}, fmt.Errorf("messaging workflow %s not found", approval.WorkflowID)
	}

	if approval.Status == messaging.ApprovalStatusApproved || approval.Status == messaging.ApprovalStatusRejected {
		return ApprovalDecisionResult{
			Approval: approval,
			Draft:    draft,
			Workflow: workflow,
		}, nil
	}
	if approval.Status != messaging.ApprovalStatusPending {
		return ApprovalDecisionResult{}, fmt.Errorf("approval %s is %s", approvalID, approval.Status)
	}

	now := b.now()
	approval.ResolvedBy = strings.TrimSpace(actorID)
	approval.ResolvedAt = &now
	if approved {
		approval.Status = messaging.ApprovalStatusApproved
		draft.Status = messaging.DraftStatusApproved
		workflow.Status = messaging.WorkflowStatusApproved
		workflow.ApprovedAt = &now
		workflow.OperatorRespondedAt = &now
		workflow.NeedsAttention = false
		workflow.AttentionReason = ""
		workflow.Error = ""
	} else {
		approval.Status = messaging.ApprovalStatusRejected
		if strings.TrimSpace(reason) != "" {
			approval.Reason = reason
		}
		draft.Status = messaging.DraftStatusRejected
		workflow.Status = messaging.WorkflowStatusDismissed
		workflow.OperatorRespondedAt = &now
		workflow.NeedsAttention = false
		workflow.AttentionReason = ""
		workflow.Error = ""
	}
	workflow.LastActivityAt = now

	if err := b.resolveMailboxApproval(ctx, approval, actorID, approved); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := b.store.PutApproval(ctx, approval); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := b.store.PutDraft(ctx, draft); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeApprovalResolved,
		ConnectionID: approval.ConnectionID,
		DraftID:      approval.DraftID,
		ExposureID:   approval.ExposureID,
		Timestamp:    now,
		Metadata: map[string]string{
			"approval_id": string(approval.ID),
			"outcome":     string(approval.Status),
		},
	}); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if err := b.appendWorkflowActivity(ctx, workflow, messaging.EventTypeApprovalResolved, draft, approval.ExposureID, map[string]string{
		"approval_id": string(approval.ID),
		"outcome":     string(approval.Status),
	}, reason); err != nil {
		return ApprovalDecisionResult{}, err
	}
	return ApprovalDecisionResult{
		Approval: approval,
		Draft:    draft,
		Workflow: workflow,
	}, nil
}

func (b *Broker) cancelApproval(ctx context.Context, approvalID messaging.ApprovalID) error {
	approval, ok := b.store.GetApproval(approvalID)
	if !ok || approval.Status != messaging.ApprovalStatusPending {
		return nil
	}
	now := b.now()
	approval.Status = messaging.ApprovalStatusCancelled
	approval.ResolvedAt = &now
	approval.ResolvedBy = "system:messaging"
	return b.store.PutApproval(ctx, approval)
}

func (b *Broker) createMailboxApproval(ctx context.Context, approval messaging.Approval, workflow messaging.Workflow, draft messaging.Draft) (string, error) {
	details, err := json.Marshal(map[string]any{
		"approval_id":     approval.ID,
		"connection_id":   approval.ConnectionID,
		"draft_id":        approval.DraftID,
		"workflow_id":     approval.WorkflowID,
		"policy_id":       approval.PolicyID,
		"conversation_id": draft.ConversationID,
		"reason":          approval.Reason,
		"summary":         workflow.Summary,
	})
	if err != nil {
		return "", fmt.Errorf("marshal approval details: %w", err)
	}
	record, err := b.approvalMailbox.CreateApprovalRequest(ctx, agentmailbox.Item{
		From:           cloneMailboxPrincipal(b.approvalFrom),
		To:             cloneMailboxPrincipalPtr(b.approvalTo),
		RequestID:      string(approval.ID),
		IdempotencyKey: string(approval.ID),
		Priority:       "high",
	}, agentmailbox.ApprovalRequestPayload{
		Action:  approval.Action,
		Summary: approval.Summary,
		Details: details,
	})
	if err != nil {
		return "", err
	}
	return record.Item.ID, nil
}

func (b *Broker) resolveMailboxApproval(ctx context.Context, approval messaging.Approval, actorID string, approved bool) error {
	if b.approvalMailbox == nil || approval.MailboxItemID == "" {
		return nil
	}
	actor := agentmailbox.Principal{
		ID:    actorID,
		Kind:  agentmailbox.PrincipalKindHuman,
		Scope: agentmailbox.ScopePrivateNetwork,
	}
	var err error
	if approved {
		_, err = b.approvalMailbox.Approve(ctx, approval.MailboxItemID, actor, string(approval.ID))
	} else {
		_, err = b.approvalMailbox.Reject(ctx, approval.MailboxItemID, actor, string(approval.ID))
	}
	return err
}

func (b *Broker) ensureDraftWorkflow(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft, policyID messaging.PolicyID) (messaging.Workflow, error) {
	for _, workflow := range b.store.ListWorkflows() {
		if workflow.DraftID != draft.ID {
			continue
		}
		return workflow, nil
	}
	now := b.now()
	workflow := messaging.Workflow{
		ID:                   messaging.WorkflowID(b.newID()),
		Kind:                 "draft_send",
		Status:               messaging.WorkflowStatusDrafted,
		SourceConnectionID:   draft.ConnectionID,
		SourceIdentityID:     draft.LocalIdentityID,
		SourceConversationID: draft.ConversationID,
		PolicyID:             policyID,
		ExposureID:           exposureID,
		Sender:               b.workflowSenderForDraft(draft),
		Summary:              summarizeDraft(draft),
		DraftID:              draft.ID,
		BrokerReceivedAt:     now,
		LastActivityAt:       now,
		DraftCreatedAt:       &now,
	}
	if err := b.store.PutWorkflow(ctx, workflow); err != nil {
		return messaging.Workflow{}, err
	}
	return workflow, nil
}

func (b *Broker) workflowSenderForDraft(draft messaging.Draft) messaging.Participant {
	if conversation, ok := b.store.GetConversation(draft.ConversationID); ok {
		for _, participant := range conversation.Participants {
			if participant.IsLocal {
				continue
			}
			return participant
		}
	}
	if identity, ok := b.store.GetIdentity(draft.LocalIdentityID); ok {
		return participantFromIdentity(identity)
	}
	return messaging.Participant{
		Kind:       messaging.ParticipantKindSystem,
		IdentityID: draft.LocalIdentityID,
		IsLocal:    true,
	}
}

func (b *Broker) latestDraftApproval(draftID messaging.DraftID) (messaging.Approval, bool) {
	approvals := b.store.ListDraftApprovals(draftID)
	if len(approvals) == 0 {
		return messaging.Approval{}, false
	}
	return approvals[len(approvals)-1], true
}

func (b *Broker) appendWorkflowActivity(ctx context.Context, workflow messaging.Workflow, eventType messaging.EventType, draft messaging.Draft, exposureID messaging.ExposureID, metadata map[string]string, errorText string) error {
	return b.store.AppendActivityEvent(ctx, messaging.ActivityEvent{
		ID:             messaging.EventID(b.newID()),
		WorkflowID:     workflow.ID,
		Type:           eventType,
		OccurredAt:     b.now(),
		ConnectionID:   draft.ConnectionID,
		ConversationID: draft.ConversationID,
		DraftID:        draft.ID,
		ExposureID:     exposureID,
		Metadata:       metadata,
		Error:          errorText,
	})
}

func attachmentsFromParts(parts []messaging.MessagePart) []protocol.Attachment {
	attachments := make([]protocol.Attachment, 0)
	for _, part := range parts {
		switch part.Kind {
		case messaging.MessagePartKindFile, messaging.MessagePartKindImage:
			attachments = append(attachments, protocol.Attachment{
				Name:        part.FileName,
				ContentType: part.ContentType,
				SizeBytes:   part.SizeBytes,
				Blob: protocol.BlobRef{
					ID:          part.Ref,
					LocalPath:   part.Ref,
					Name:        part.FileName,
					ContentType: part.ContentType,
					SizeBytes:   part.SizeBytes,
				},
				Metadata: cloneStringMap(part.Metadata),
			})
		}
	}
	return attachments
}

func normalizeOutboundMessage(result protocol.SendResult, draft messaging.Draft, now time.Time) messaging.Message {
	message := result.Message.Message
	if strings.TrimSpace(string(message.ID)) == "" {
		message.ID = messaging.MessageID(string(draft.ID) + "/sent")
	}
	if message.ConnectionID == "" {
		message.ConnectionID = draft.ConnectionID
	}
	if message.ConversationID == "" {
		message.ConversationID = draft.ConversationID
	}
	if message.LocalIdentityID == "" {
		message.LocalIdentityID = draft.LocalIdentityID
	}
	if strings.TrimSpace(string(message.Direction)) == "" {
		message.Direction = messaging.MessageDirectionOutbound
	}
	if len(message.Parts) == 0 {
		message.Parts = cloneMessageParts(draft.Parts)
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	if strings.TrimSpace(string(message.Status)) == "" {
		message.Status = result.Status
		if strings.TrimSpace(string(message.Status)) == "" {
			message.Status = messaging.MessageStatusSent
		}
	}
	if err := message.Sender.Validate(); err != nil {
		message.Sender = messaging.Participant{
			Kind:       messaging.ParticipantKindBot,
			IdentityID: draft.LocalIdentityID,
			IsLocal:    true,
		}
	}
	return message
}

func participantFromIdentity(identity messaging.Identity) messaging.Participant {
	kind := messaging.ParticipantKindAccount
	switch identity.Kind {
	case messaging.IdentityKindBot, messaging.IdentityKindWebhook:
		kind = messaging.ParticipantKindBot
	case messaging.IdentityKindPage:
		kind = messaging.ParticipantKindPage
	}
	return messaging.Participant{
		Kind:        kind,
		RemoteID:    identity.RemoteID,
		Address:     identity.Address,
		DisplayName: identity.DisplayName,
		IdentityID:  identity.ID,
		IsLocal:     true,
	}
}

func summarizeDraft(draft messaging.Draft) string {
	for _, part := range draft.Parts {
		switch part.Kind {
		case messaging.MessagePartKindText, messaging.MessagePartKindMarkdown, messaging.MessagePartKindHTML:
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			if len(text) > 160 {
				return text[:160]
			}
			return text
		}
	}
	return "Draft reply"
}

func draftApprovalSummary(workflow messaging.Workflow, draft messaging.Draft) string {
	target := strings.TrimSpace(workflow.Sender.DisplayName)
	if target == "" {
		target = strings.TrimSpace(workflow.Sender.Address)
	}
	if target == "" {
		target = string(draft.ConversationID)
	}
	return fmt.Sprintf("Approve sending draft to %s", target)
}

func requestedBy(store interface {
	GetExposure(messaging.ExposureID) (messaging.Exposure, bool)
}, exposureID messaging.ExposureID) string {
	if exposureID == "" {
		return "broker"
	}
	exposure, ok := store.GetExposure(exposureID)
	if !ok {
		return "broker"
	}
	return exposure.SubjectID
}

func cloneDraft(draft messaging.Draft) messaging.Draft {
	draft.Parts = cloneMessageParts(draft.Parts)
	draft.Metadata = cloneStringMap(draft.Metadata)
	return draft
}

func cloneMessageParts(parts []messaging.MessagePart) []messaging.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]messaging.MessagePart, 0, len(parts))
	for _, part := range parts {
		cloned := part
		cloned.Metadata = cloneStringMap(part.Metadata)
		out = append(out, cloned)
	}
	return out
}

func cloneMailboxPrincipal(principal agentmailbox.Principal) agentmailbox.Principal {
	return principal
}

func cloneMailboxPrincipalPtr(principal *agentmailbox.Principal) *agentmailbox.Principal {
	if principal == nil {
		return nil
	}
	copy := *principal
	return &copy
}
