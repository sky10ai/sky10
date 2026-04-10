package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	metaDecisionID   = "decision_id"
	metaCompletionID = "completion_id"
)

// TaskRequestPayload is the structured request body for a claimable or
// addressed task.
type TaskRequestPayload struct {
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Summary string          `json:"summary,omitempty"`
}

func (p TaskRequestPayload) Validate() error {
	if strings.TrimSpace(p.Method) == "" {
		return fmt.Errorf("task method is required")
	}
	return nil
}

// ApprovalRequestPayload is the structured body for a human or agent approval
// request.
type ApprovalRequestPayload struct {
	Action  string          `json:"action"`
	Summary string          `json:"summary"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (p ApprovalRequestPayload) Validate() error {
	if strings.TrimSpace(p.Action) == "" {
		return fmt.Errorf("approval action is required")
	}
	if strings.TrimSpace(p.Summary) == "" {
		return fmt.Errorf("approval summary is required")
	}
	return nil
}

// PaymentRequiredPayload is the quote/control-plane message sent by the
// provider before work begins.
type PaymentRequiredPayload struct {
	Method  string `json:"method"`
	Amount  string `json:"amount"`
	Asset   string `json:"asset"`
	Chain   string `json:"chain"`
	Address string `json:"address"`
	Nonce   string `json:"nonce"`
}

func (p PaymentRequiredPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.Method) == "":
		return fmt.Errorf("payment method is required")
	case strings.TrimSpace(p.Amount) == "":
		return fmt.Errorf("payment amount is required")
	case strings.TrimSpace(p.Asset) == "":
		return fmt.Errorf("payment asset is required")
	case strings.TrimSpace(p.Chain) == "":
		return fmt.Errorf("payment chain is required")
	case strings.TrimSpace(p.Address) == "":
		return fmt.Errorf("payment address is required")
	case strings.TrimSpace(p.Nonce) == "":
		return fmt.Errorf("payment nonce is required")
	default:
		return nil
	}
}

// PaymentProofPayload carries the caller-signed payment bytes handed to the
// provider.
type PaymentProofPayload struct {
	SignedTx string `json:"signed_tx"`
	Chain    string `json:"chain"`
	Amount   string `json:"amount"`
	Nonce    string `json:"nonce"`
}

func (p PaymentProofPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.SignedTx) == "":
		return fmt.Errorf("payment proof signed_tx is required")
	case strings.TrimSpace(p.Chain) == "":
		return fmt.Errorf("payment proof chain is required")
	case strings.TrimSpace(p.Amount) == "":
		return fmt.Errorf("payment proof amount is required")
	case strings.TrimSpace(p.Nonce) == "":
		return fmt.Errorf("payment proof nonce is required")
	default:
		return nil
	}
}

// ReceiptPayload is the co-signed transaction/work receipt exchanged after
// result delivery.
type ReceiptPayload struct {
	TxHash            string `json:"tx_hash,omitempty"`
	Caller            string `json:"caller"`
	Provider          string `json:"provider"`
	Method            string `json:"method"`
	Amount            string `json:"amount"`
	Chain             string `json:"chain"`
	Nonce             string `json:"nonce"`
	CallerRating      int    `json:"caller_rating,omitempty"`
	ProviderRating    int    `json:"provider_rating,omitempty"`
	CallerSignature   string `json:"caller_signature,omitempty"`
	ProviderSignature string `json:"provider_signature,omitempty"`
}

func (p ReceiptPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.Caller) == "":
		return fmt.Errorf("receipt caller is required")
	case strings.TrimSpace(p.Provider) == "":
		return fmt.Errorf("receipt provider is required")
	case strings.TrimSpace(p.Method) == "":
		return fmt.Errorf("receipt method is required")
	case strings.TrimSpace(p.Amount) == "":
		return fmt.Errorf("receipt amount is required")
	case strings.TrimSpace(p.Chain) == "":
		return fmt.Errorf("receipt chain is required")
	case strings.TrimSpace(p.Nonce) == "":
		return fmt.Errorf("receipt nonce is required")
	default:
		return nil
	}
}

// ResultPayload is the provider's durable response, including a provider-signed
// receipt draft.
type ResultPayload struct {
	Data    json.RawMessage `json:"data,omitempty"`
	Receipt ReceiptPayload  `json:"receipt"`
}

func (p ResultPayload) Validate() error {
	if err := p.Receipt.Validate(); err != nil {
		return fmt.Errorf("result receipt: %w", err)
	}
	if strings.TrimSpace(p.Receipt.ProviderSignature) == "" {
		return fmt.Errorf("result receipt provider signature is required")
	}
	return nil
}

// CreateTaskRequest creates a durable task request with explicit request and
// idempotency keys.
func (s *Store) CreateTaskRequest(ctx context.Context, item Item, payload TaskRequestPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(item.RequestID) == "" {
		return Record{}, fmt.Errorf("task request_id is required")
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("task idempotency_key is required")
	}
	item.Kind = ItemKindTaskRequest
	return s.createWorkflowItem(ctx, item, payload)
}

// CompleteTaskRequest marks a task request as completed once, using
// completionID as the replay-safe idempotency token for the event.
func (s *Store) CompleteTaskRequest(ctx context.Context, itemID string, actor Principal, completionID string) (Record, error) {
	return s.applyTerminalEvent(ctx, itemID, actor, completionID, EventTypeCompleted, EventTypeCancelled)
}

// CreateApprovalRequest creates a durable approval request.
func (s *Store) CreateApprovalRequest(ctx context.Context, item Item, payload ApprovalRequestPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(item.RequestID) == "" {
		return Record{}, fmt.Errorf("approval request_id is required")
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("approval idempotency_key is required")
	}
	item.Kind = ItemKindApprovalRequest
	return s.createWorkflowItem(ctx, item, payload)
}

// Approve records an approval decision idempotently.
func (s *Store) Approve(ctx context.Context, itemID string, actor Principal, decisionID string) (Record, error) {
	return s.applyTerminalEvent(ctx, itemID, actor, decisionID, EventTypeApproved, EventTypeRejected)
}

// Reject records a rejection decision idempotently.
func (s *Store) Reject(ctx context.Context, itemID string, actor Principal, decisionID string) (Record, error) {
	return s.applyTerminalEvent(ctx, itemID, actor, decisionID, EventTypeRejected, EventTypeApproved)
}

// CreatePaymentRequired creates the provider's payment quote/control-plane
// message.
func (s *Store) CreatePaymentRequired(ctx context.Context, item Item, payload PaymentRequiredPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(item.RequestID) == "" {
		return Record{}, fmt.Errorf("payment request_id is required")
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("payment idempotency_key is required")
	}
	item.Kind = ItemKindPaymentRequired
	return s.createWorkflowItem(ctx, item, payload)
}

// CreatePaymentProof creates the caller's signed-payment response after
// validating it against the original payment_required message.
func (s *Store) CreatePaymentProof(ctx context.Context, item Item, payload PaymentProofPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	parent, requiredPayload, err := s.loadPaymentRequiredParent(item)
	if err != nil {
		return Record{}, err
	}
	if item.RequestID == "" {
		item.RequestID = parent.Item.RequestID
	}
	if item.RequestID != parent.Item.RequestID {
		return Record{}, fmt.Errorf("payment proof request_id %q does not match payment_required %q", item.RequestID, parent.Item.RequestID)
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("payment proof idempotency_key is required")
	}
	if item.To == nil {
		to := clonePrincipal(parent.Item.From)
		item.To = &to
	}
	if item.To.ID != parent.Item.From.ID {
		return Record{}, fmt.Errorf("payment proof recipient %q does not match provider %q", item.To.ID, parent.Item.From.ID)
	}
	if parent.Item.To != nil && item.From.ID != parent.Item.To.ID {
		return Record{}, fmt.Errorf("payment proof sender %q does not match caller %q", item.From.ID, parent.Item.To.ID)
	}
	if payload.Amount != requiredPayload.Amount || payload.Chain != requiredPayload.Chain || payload.Nonce != requiredPayload.Nonce {
		return Record{}, fmt.Errorf("payment proof does not match required amount/chain/nonce")
	}
	item.Kind = ItemKindPaymentProof
	return s.createWorkflowItem(ctx, item, payload)
}

// CreateResult creates the provider's result item and validates the embedded
// receipt draft against the payment flow.
func (s *Store) CreateResult(ctx context.Context, item Item, payload ResultPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	proofRecord, proofPayload, requiredRecord, requiredPayload, err := s.loadPaymentChainForResult(item)
	if err != nil {
		return Record{}, err
	}
	if item.RequestID == "" {
		item.RequestID = proofRecord.Item.RequestID
	}
	if item.RequestID != proofRecord.Item.RequestID {
		return Record{}, fmt.Errorf("result request_id %q does not match payment proof %q", item.RequestID, proofRecord.Item.RequestID)
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("result idempotency_key is required")
	}
	if item.To == nil && requiredRecord.Item.To != nil {
		to := clonePrincipal(*requiredRecord.Item.To)
		item.To = &to
	}
	if item.From.ID != requiredRecord.Item.From.ID {
		return Record{}, fmt.Errorf("result sender %q does not match provider %q", item.From.ID, requiredRecord.Item.From.ID)
	}
	if requiredRecord.Item.To != nil && (item.To == nil || item.To.ID != requiredRecord.Item.To.ID) {
		return Record{}, fmt.Errorf("result recipient does not match caller %q", requiredRecord.Item.To.ID)
	}
	if err := validateReceiptAgainstRequired(payload.Receipt, requiredRecord, requiredPayload); err != nil {
		return Record{}, fmt.Errorf("result receipt mismatch: %w", err)
	}
	if payload.Receipt.CallerSignature != "" {
		return Record{}, fmt.Errorf("result receipt must not include caller signature yet")
	}
	if proofPayload.Amount != requiredPayload.Amount || proofPayload.Chain != requiredPayload.Chain || proofPayload.Nonce != requiredPayload.Nonce {
		return Record{}, fmt.Errorf("payment proof does not match payment requirement")
	}
	item.Kind = ItemKindResult
	return s.createWorkflowItem(ctx, item, payload)
}

// CreateReceipt creates the caller's counter-signed receipt and finalizes the
// surrounding payment flow idempotently.
func (s *Store) CreateReceipt(ctx context.Context, item Item, payload ReceiptPayload) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	resultRecord, resultPayload, proofRecord, requiredRecord, requiredPayload, err := s.loadPaymentChainForReceipt(item)
	if err != nil {
		return Record{}, err
	}
	if item.RequestID == "" {
		item.RequestID = resultRecord.Item.RequestID
	}
	if item.RequestID != resultRecord.Item.RequestID {
		return Record{}, fmt.Errorf("receipt request_id %q does not match result %q", item.RequestID, resultRecord.Item.RequestID)
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, fmt.Errorf("receipt idempotency_key is required")
	}
	if item.To == nil {
		to := clonePrincipal(requiredRecord.Item.From)
		item.To = &to
	}
	if requiredRecord.Item.To != nil && item.From.ID != requiredRecord.Item.To.ID {
		return Record{}, fmt.Errorf("receipt sender %q does not match caller %q", item.From.ID, requiredRecord.Item.To.ID)
	}
	if item.To.ID != requiredRecord.Item.From.ID {
		return Record{}, fmt.Errorf("receipt recipient %q does not match provider %q", item.To.ID, requiredRecord.Item.From.ID)
	}
	if err := validateReceiptAgainstRequired(payload, requiredRecord, requiredPayload); err != nil {
		return Record{}, fmt.Errorf("receipt mismatch: %w", err)
	}
	if payload.ProviderSignature == "" || payload.CallerSignature == "" {
		return Record{}, fmt.Errorf("receipt must include both caller and provider signatures")
	}
	if !receiptMatchesDraft(resultPayload.Receipt, payload) {
		return Record{}, fmt.Errorf("receipt does not match the result receipt draft")
	}
	item.Kind = ItemKindReceipt
	record, err := s.createWorkflowItem(ctx, item, payload)
	if err != nil {
		return Record{}, err
	}
	if _, err := s.markCompletedOnce(ctx, proofRecord.Item.ID, clonePrincipal(requiredRecord.Item.From), item.IdempotencyKey); err != nil {
		return Record{}, err
	}
	if _, err := s.markCompletedOnce(ctx, requiredRecord.Item.ID, clonePrincipal(requiredRecord.Item.From), item.IdempotencyKey); err != nil {
		return Record{}, err
	}
	if _, err := s.markCompletedOnce(ctx, resultRecord.Item.ID, clonePrincipal(item.From), item.IdempotencyKey); err != nil {
		return Record{}, err
	}
	return s.markCompletedOnce(ctx, record.Item.ID, clonePrincipal(item.From), item.IdempotencyKey)
}

func (s *Store) createWorkflowItem(ctx context.Context, item Item, payload interface{}) (Record, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Record{}, fmt.Errorf("marshal workflow payload: %w", err)
	}
	if err := item.From.Validate(); err != nil {
		return Record{}, fmt.Errorf("workflow sender: %w", err)
	}
	item.PayloadInline = payloadJSON

	if existing, ok := s.findDuplicateWorkflowItem(item); ok {
		if !workflowItemEquivalent(existing.Item, item) {
			return Record{}, fmt.Errorf("idempotency key %q already used for a different %s item", item.IdempotencyKey, item.Kind)
		}
		if !bytes.Equal(existing.Item.PayloadInline, payloadJSON) {
			return Record{}, fmt.Errorf("idempotency key %q already used with a different payload", item.IdempotencyKey)
		}
		return existing, nil
	}
	return s.Create(ctx, item)
}

func (s *Store) findDuplicateWorkflowItem(item Item) (Record, bool) {
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return Record{}, false
	}
	return s.findFirstMatching(func(record Record) bool {
		return record.Item.Kind == item.Kind &&
			record.Item.RequestID == item.RequestID &&
			record.Item.IdempotencyKey == item.IdempotencyKey
	})
}

func workflowItemEquivalent(existing Item, candidate Item) bool {
	if existing.Kind != candidate.Kind ||
		existing.RequestID != candidate.RequestID ||
		existing.ReplyTo != candidate.ReplyTo ||
		existing.IdempotencyKey != candidate.IdempotencyKey ||
		existing.From.ID != candidate.From.ID {
		return false
	}
	if (existing.To == nil) != (candidate.To == nil) {
		return false
	}
	if existing.To != nil && candidate.To != nil && existing.To.ID != candidate.To.ID {
		return false
	}
	return true
}

func (s *Store) applyTerminalEvent(ctx context.Context, itemID string, actor Principal, token, eventType string, conflicting string) (Record, error) {
	if strings.TrimSpace(token) == "" {
		return Record{}, fmt.Errorf("workflow token is required")
	}
	record, ok := s.Get(itemID)
	if !ok {
		return Record{}, fmt.Errorf("mailbox item %s not found", itemID)
	}
	for _, event := range record.Events {
		if event.Type == eventType {
			return record, nil
		}
		if event.Type == conflicting {
			return Record{}, fmt.Errorf("mailbox item %s already has %s", itemID, conflicting)
		}
	}
	return s.AppendEvent(ctx, Event{
		ItemID: itemID,
		Type:   eventType,
		Actor:  clonePrincipal(actor),
		Meta: map[string]string{
			metaDecisionID: token,
		},
	})
}

func (s *Store) markCompletedOnce(ctx context.Context, itemID string, actor Principal, completionID string) (Record, error) {
	record, ok := s.Get(itemID)
	if !ok {
		return Record{}, fmt.Errorf("mailbox item %s not found", itemID)
	}
	for _, event := range record.Events {
		if event.Type == EventTypeCompleted {
			return record, nil
		}
	}
	return s.AppendEvent(ctx, Event{
		ItemID: itemID,
		Type:   EventTypeCompleted,
		Actor:  clonePrincipal(actor),
		Meta: map[string]string{
			metaCompletionID: completionID,
		},
	})
}

func (s *Store) loadPaymentRequiredParent(item Item) (Record, PaymentRequiredPayload, error) {
	replyTo := strings.TrimSpace(item.ReplyTo)
	if replyTo == "" {
		return Record{}, PaymentRequiredPayload{}, fmt.Errorf("payment proof reply_to is required")
	}
	parent, ok := s.Get(replyTo)
	if !ok {
		return Record{}, PaymentRequiredPayload{}, fmt.Errorf("payment_required %s not found", replyTo)
	}
	if parent.Item.Kind != ItemKindPaymentRequired {
		return Record{}, PaymentRequiredPayload{}, fmt.Errorf("reply_to %s is %s, want %s", replyTo, parent.Item.Kind, ItemKindPaymentRequired)
	}
	var payload PaymentRequiredPayload
	if err := json.Unmarshal(parent.Item.PayloadInline, &payload); err != nil {
		return Record{}, PaymentRequiredPayload{}, fmt.Errorf("parse payment_required payload: %w", err)
	}
	return parent, payload, nil
}

func (s *Store) loadPaymentChainForResult(item Item) (Record, PaymentProofPayload, Record, PaymentRequiredPayload, error) {
	replyTo := strings.TrimSpace(item.ReplyTo)
	if replyTo == "" {
		return Record{}, PaymentProofPayload{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("result reply_to is required")
	}
	proofRecord, ok := s.Get(replyTo)
	if !ok {
		return Record{}, PaymentProofPayload{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("payment_proof %s not found", replyTo)
	}
	if proofRecord.Item.Kind != ItemKindPaymentProof {
		return Record{}, PaymentProofPayload{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("reply_to %s is %s, want %s", replyTo, proofRecord.Item.Kind, ItemKindPaymentProof)
	}
	var proofPayload PaymentProofPayload
	if err := json.Unmarshal(proofRecord.Item.PayloadInline, &proofPayload); err != nil {
		return Record{}, PaymentProofPayload{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("parse payment_proof payload: %w", err)
	}
	requiredRecord, requiredPayload, err := s.loadPaymentRequiredParent(proofRecord.Item)
	if err != nil {
		return Record{}, PaymentProofPayload{}, Record{}, PaymentRequiredPayload{}, err
	}
	return proofRecord, proofPayload, requiredRecord, requiredPayload, nil
}

func (s *Store) loadPaymentChainForReceipt(item Item) (Record, ResultPayload, Record, Record, PaymentRequiredPayload, error) {
	replyTo := strings.TrimSpace(item.ReplyTo)
	if replyTo == "" {
		return Record{}, ResultPayload{}, Record{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("receipt reply_to is required")
	}
	resultRecord, ok := s.Get(replyTo)
	if !ok {
		return Record{}, ResultPayload{}, Record{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("result %s not found", replyTo)
	}
	if resultRecord.Item.Kind != ItemKindResult {
		return Record{}, ResultPayload{}, Record{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("reply_to %s is %s, want %s", replyTo, resultRecord.Item.Kind, ItemKindResult)
	}
	var resultPayload ResultPayload
	if err := json.Unmarshal(resultRecord.Item.PayloadInline, &resultPayload); err != nil {
		return Record{}, ResultPayload{}, Record{}, Record{}, PaymentRequiredPayload{}, fmt.Errorf("parse result payload: %w", err)
	}
	proofRecord, _, requiredRecord, requiredPayload, err := s.loadPaymentChainForResult(resultRecord.Item)
	if err != nil {
		return Record{}, ResultPayload{}, Record{}, Record{}, PaymentRequiredPayload{}, err
	}
	return resultRecord, resultPayload, proofRecord, requiredRecord, requiredPayload, nil
}

func validateReceiptAgainstRequired(receipt ReceiptPayload, requiredRecord Record, requiredPayload PaymentRequiredPayload) error {
	if receipt.Caller != requiredRecord.Item.RecipientID() {
		return fmt.Errorf("receipt caller %q does not match required caller %q", receipt.Caller, requiredRecord.Item.RecipientID())
	}
	if receipt.Provider != requiredRecord.Item.From.ID {
		return fmt.Errorf("receipt provider %q does not match required provider %q", receipt.Provider, requiredRecord.Item.From.ID)
	}
	if receipt.Method != requiredPayload.Method {
		return fmt.Errorf("receipt method %q does not match required %q", receipt.Method, requiredPayload.Method)
	}
	if receipt.Amount != requiredPayload.Amount {
		return fmt.Errorf("receipt amount %q does not match required %q", receipt.Amount, requiredPayload.Amount)
	}
	if receipt.Chain != requiredPayload.Chain {
		return fmt.Errorf("receipt chain %q does not match required %q", receipt.Chain, requiredPayload.Chain)
	}
	if receipt.Nonce != requiredPayload.Nonce {
		return fmt.Errorf("receipt nonce %q does not match required %q", receipt.Nonce, requiredPayload.Nonce)
	}
	return nil
}

func receiptMatchesDraft(draft, final ReceiptPayload) bool {
	return draft.TxHash == final.TxHash &&
		draft.Caller == final.Caller &&
		draft.Provider == final.Provider &&
		draft.Method == final.Method &&
		draft.Amount == final.Amount &&
		draft.Chain == final.Chain &&
		draft.Nonce == final.Nonce &&
		draft.ProviderRating == final.ProviderRating &&
		draft.ProviderSignature == final.ProviderSignature
}
