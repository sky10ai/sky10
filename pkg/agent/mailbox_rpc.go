package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

type mailboxListParams struct {
	PrincipalID string `json:"principal_id,omitempty"`
	Queue       string `json:"queue,omitempty"`
}

type mailboxGetParams struct {
	ItemID string `json:"item_id"`
}

type mailboxActionParams struct {
	ItemID     string `json:"item_id"`
	ActorID    string `json:"actor_id,omitempty"`
	ActorKind  string `json:"actor_kind,omitempty"`
	Token      string `json:"token,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type mailboxRetryParams struct {
	ItemID string `json:"item_id"`
}

type mailboxPrincipalParams struct {
	ID         string `json:"id"`
	Kind       string `json:"kind,omitempty"`
	Scope      string `json:"scope,omitempty"`
	DeviceHint string `json:"device_hint,omitempty"`
}

type mailboxSendParams struct {
	Kind           string                  `json:"kind"`
	From           *mailboxPrincipalParams `json:"from,omitempty"`
	To             *mailboxPrincipalParams `json:"to,omitempty"`
	TargetSkill    string                  `json:"target_skill,omitempty"`
	SessionID      string                  `json:"session_id,omitempty"`
	RequestID      string                  `json:"request_id,omitempty"`
	ReplyTo        string                  `json:"reply_to,omitempty"`
	IdempotencyKey string                  `json:"idempotency_key,omitempty"`
	Priority       string                  `json:"priority,omitempty"`
	ExpiresAt      time.Time               `json:"expires_at,omitempty"`
	Payload        json.RawMessage         `json:"payload,omitempty"`
}

func (h *RPCHandler) rpcMailboxSend(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}

	var p mailboxSendParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(p.Kind) == "" {
		return nil, fmt.Errorf("kind is required")
	}

	item := agentmailbox.Item{
		Kind:           p.Kind,
		From:           h.mailboxPrincipal(p.From),
		To:             h.mailboxRecipient(p.To),
		TargetSkill:    strings.TrimSpace(p.TargetSkill),
		SessionID:      p.SessionID,
		RequestID:      strings.TrimSpace(p.RequestID),
		ReplyTo:        strings.TrimSpace(p.ReplyTo),
		IdempotencyKey: strings.TrimSpace(p.IdempotencyKey),
		Priority:       strings.TrimSpace(p.Priority),
		ExpiresAt:      p.ExpiresAt,
		PayloadInline:  append(json.RawMessage(nil), p.Payload...),
	}
	if item.RequestID == "" {
		item.RequestID = item.IdempotencyKey
	}

	record, err := h.createMailboxRecord(ctx, store, item, p.Payload)
	if err != nil {
		return nil, err
	}
	h.emitMailboxEvent("agent.mailbox.updated", "send", record)
	return mailboxRecordResult(record), nil
}

func (h *RPCHandler) rpcMailboxListInbox(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	p, err := parseMailboxListParams(params)
	if err != nil {
		return nil, err
	}
	items := store.ListInbox(strings.TrimSpace(p.PrincipalID))
	return mailboxListResult(items), nil
}

func (h *RPCHandler) rpcMailboxListOutbox(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	p, err := parseMailboxListParams(params)
	if err != nil {
		return nil, err
	}
	items := store.ListOutbox(strings.TrimSpace(p.PrincipalID))
	return mailboxListResult(items), nil
}

func (h *RPCHandler) rpcMailboxListQueue(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	p, err := parseMailboxListParams(params)
	if err != nil {
		return nil, err
	}
	items := store.ListQueue(strings.TrimSpace(p.Queue))
	return mailboxListResult(items), nil
}

func (h *RPCHandler) rpcMailboxListFailed(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	p, err := parseMailboxListParams(params)
	if err != nil {
		return nil, err
	}
	items := store.ListFailed(strings.TrimSpace(p.PrincipalID))
	return mailboxListResult(items), nil
}

func (h *RPCHandler) rpcMailboxListSent(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	p, err := parseMailboxListParams(params)
	if err != nil {
		return nil, err
	}
	items := store.ListSent(strings.TrimSpace(p.PrincipalID))
	return mailboxListResult(items), nil
}

func (h *RPCHandler) rpcMailboxGet(_ context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(p.ItemID) == "" {
		return nil, fmt.Errorf("item_id is required")
	}
	record, ok := store.Get(strings.TrimSpace(p.ItemID))
	return map[string]interface{}{
		"item":  record,
		"found": ok,
	}, nil
}

func (h *RPCHandler) rpcMailboxClaim(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	ttl := time.Duration(p.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	record, ok, err := store.Claim(ctx, strings.TrimSpace(p.ItemID), h.actionActor(p.ActorID, p.ActorKind), ttl)
	if err != nil {
		return nil, err
	}
	if ok {
		h.emitMailboxEvent("agent.mailbox.claimed", "claim", record)
		h.emitMailboxEvent("agent.mailbox.updated", "claim", record)
	}
	return map[string]interface{}{
		"item":     record,
		"claimed":  ok,
		"released": false,
	}, nil
}

func (h *RPCHandler) rpcMailboxRelease(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	holder := strings.TrimSpace(p.ActorID)
	if holder == "" {
		holder = h.defaultActorID()
	}
	record, ok, err := store.Release(ctx, strings.TrimSpace(p.ItemID), holder, strings.TrimSpace(p.Token))
	if err != nil {
		return nil, err
	}
	if ok {
		h.emitMailboxEvent("agent.mailbox.updated", "release", record)
	}
	return map[string]interface{}{
		"item":     record,
		"claimed":  false,
		"released": ok,
	}, nil
}

func (h *RPCHandler) rpcMailboxAck(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	record, err := store.AppendEvent(ctx, agentmailbox.Event{
		ItemID: strings.TrimSpace(p.ItemID),
		Type:   agentmailbox.EventTypeSeen,
		Actor:  h.actionActor(p.ActorID, p.ActorKind),
	})
	if err != nil {
		return nil, err
	}
	h.emitMailboxEvent("agent.mailbox.updated", "ack", record)
	return mailboxRecordResult(record), nil
}

func (h *RPCHandler) rpcMailboxApprove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	decisionID := strings.TrimSpace(p.DecisionID)
	if decisionID == "" {
		decisionID = uuid.NewString()
	}
	record, err := store.Approve(ctx, strings.TrimSpace(p.ItemID), h.actionActor(p.ActorID, p.ActorKind), decisionID)
	if err != nil {
		return nil, err
	}
	h.emitMailboxEvent("agent.mailbox.updated", "approve", record)
	return mailboxRecordResult(record), nil
}

func (h *RPCHandler) rpcMailboxReject(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	decisionID := strings.TrimSpace(p.DecisionID)
	if decisionID == "" {
		decisionID = uuid.NewString()
	}
	record, err := store.Reject(ctx, strings.TrimSpace(p.ItemID), h.actionActor(p.ActorID, p.ActorKind), decisionID)
	if err != nil {
		return nil, err
	}
	h.emitMailboxEvent("agent.mailbox.updated", "reject", record)
	return mailboxRecordResult(record), nil
}

func (h *RPCHandler) rpcMailboxComplete(ctx context.Context, params json.RawMessage) (interface{}, error) {
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	completionID := strings.TrimSpace(p.DecisionID)
	if completionID == "" {
		completionID = uuid.NewString()
	}
	record, err := store.CompleteTaskRequest(ctx, strings.TrimSpace(p.ItemID), h.actionActor(p.ActorID, p.ActorKind), completionID)
	if err != nil {
		return nil, err
	}
	h.emitMailboxEvent("agent.mailbox.completed", "complete", record)
	h.emitMailboxEvent("agent.mailbox.updated", "complete", record)
	return mailboxRecordResult(record), nil
}

func (h *RPCHandler) rpcMailboxRetry(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.router == nil {
		return nil, fmt.Errorf("router not configured")
	}
	store, err := h.requireMailbox()
	if err != nil {
		return nil, err
	}
	var p mailboxRetryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	record, ok := store.Get(strings.TrimSpace(p.ItemID))
	if !ok {
		return nil, fmt.Errorf("mailbox item %s not found", strings.TrimSpace(p.ItemID))
	}

	if record.Item.From.ID == h.registry.DeviceID() && record.Item.To != nil && record.Item.To.DeviceHint != "" {
		if err := h.router.DrainOutbox(ctx, record.Item.To.DeviceHint); err != nil {
			return nil, err
		}
	} else if record.Item.To != nil {
		if err := h.router.DrainLocalPending(ctx, record.Item.To.ID); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("mailbox item %s is not retryable", record.Item.ID)
	}

	updated, _ := store.Get(record.Item.ID)
	return mailboxRecordResult(updated), nil
}

func (h *RPCHandler) requireMailbox() (*agentmailbox.Store, error) {
	if h.mailbox == nil {
		return nil, fmt.Errorf("mailbox not configured")
	}
	return h.mailbox, nil
}

func parseMailboxListParams(params json.RawMessage) (mailboxListParams, error) {
	if len(params) == 0 || string(params) == "null" {
		return mailboxListParams{}, nil
	}
	var p mailboxListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mailboxListParams{}, fmt.Errorf("invalid params: %w", err)
	}
	return p, nil
}

func mailboxListResult(items []agentmailbox.Record) map[string]interface{} {
	return map[string]interface{}{
		"items": items,
		"count": len(items),
	}
}

func mailboxRecordResult(record agentmailbox.Record) map[string]interface{} {
	return map[string]interface{}{
		"item": record,
	}
}

func (h *RPCHandler) mailboxPrincipal(p *mailboxPrincipalParams) agentmailbox.Principal {
	if p == nil {
		return agentmailbox.Principal{
			ID:         h.defaultActorID(),
			Kind:       agentmailbox.PrincipalKindHuman,
			Scope:      agentmailbox.ScopePrivateNetwork,
			DeviceHint: h.registry.DeviceID(),
		}
	}
	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = agentmailbox.PrincipalKindHuman
	}
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = agentmailbox.ScopePrivateNetwork
	}
	return agentmailbox.Principal{
		ID:         strings.TrimSpace(p.ID),
		Kind:       kind,
		Scope:      scope,
		DeviceHint: strings.TrimSpace(p.DeviceHint),
	}
}

func (h *RPCHandler) mailboxRecipient(p *mailboxPrincipalParams) *agentmailbox.Principal {
	if p == nil {
		return nil
	}
	recipient := h.mailboxPrincipal(p)
	return &recipient
}

func (h *RPCHandler) actionActor(id, kind string) agentmailbox.Principal {
	actorID := strings.TrimSpace(id)
	if actorID == "" {
		actorID = h.defaultActorID()
	}
	actorKind := strings.TrimSpace(kind)
	if actorKind == "" {
		actorKind = agentmailbox.PrincipalKindHuman
	}
	return agentmailbox.Principal{
		ID:         actorID,
		Kind:       actorKind,
		Scope:      agentmailbox.ScopePrivateNetwork,
		DeviceHint: h.registry.DeviceID(),
	}
}

func (h *RPCHandler) defaultActorID() string {
	if h.owner != nil {
		return h.owner.Address()
	}
	return h.registry.DeviceID()
}

func (h *RPCHandler) emitMailboxEvent(event, action string, record agentmailbox.Record) {
	if h.emit == nil {
		return
	}
	h.emit(event, map[string]interface{}{
		"action": action,
		"item":   record,
	})
}

func (h *RPCHandler) createMailboxRecord(ctx context.Context, store *agentmailbox.Store, item agentmailbox.Item, payload json.RawMessage) (agentmailbox.Record, error) {
	switch item.Kind {
	case agentmailbox.ItemKindTaskRequest:
		var taskPayload agentmailbox.TaskRequestPayload
		if err := json.Unmarshal(payload, &taskPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid task payload: %w", err)
		}
		return store.CreateTaskRequest(ctx, item, taskPayload)
	case agentmailbox.ItemKindApprovalRequest:
		var approvalPayload agentmailbox.ApprovalRequestPayload
		if err := json.Unmarshal(payload, &approvalPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid approval payload: %w", err)
		}
		return store.CreateApprovalRequest(ctx, item, approvalPayload)
	case agentmailbox.ItemKindPaymentRequired:
		var paymentPayload agentmailbox.PaymentRequiredPayload
		if err := json.Unmarshal(payload, &paymentPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid payment_required payload: %w", err)
		}
		return store.CreatePaymentRequired(ctx, item, paymentPayload)
	case agentmailbox.ItemKindPaymentProof:
		var proofPayload agentmailbox.PaymentProofPayload
		if err := json.Unmarshal(payload, &proofPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid payment_proof payload: %w", err)
		}
		return store.CreatePaymentProof(ctx, item, proofPayload)
	case agentmailbox.ItemKindResult:
		var resultPayload agentmailbox.ResultPayload
		if err := json.Unmarshal(payload, &resultPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid result payload: %w", err)
		}
		return store.CreateResult(ctx, item, resultPayload)
	case agentmailbox.ItemKindReceipt:
		var receiptPayload agentmailbox.ReceiptPayload
		if err := json.Unmarshal(payload, &receiptPayload); err != nil {
			return agentmailbox.Record{}, fmt.Errorf("invalid receipt payload: %w", err)
		}
		return store.CreateReceipt(ctx, item, receiptPayload)
	default:
		return store.Create(ctx, item)
	}
}
