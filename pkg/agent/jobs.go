package agent

import "encoding/json"

const (
	JobWorkReceived      = "received"
	JobWorkAccepted      = "accepted"
	JobWorkQueued        = "queued"
	JobWorkRunning       = "running"
	JobWorkInputRequired = "input_required"
	JobWorkCompleted     = "completed"
	JobWorkFailed        = "failed"
	JobWorkCanceled      = "canceled"
	JobWorkExpired       = "expired"

	JobPaymentNone       = "none"
	JobPaymentRequired   = "required"
	JobPaymentAuthorized = "authorized"
	JobPaymentSettled    = "settled"
	JobPaymentFailed     = "failed"
	JobPaymentRefunded   = "refunded"

	AgentCallAccepted        = "accepted"
	AgentCallResult          = "result"
	AgentCallPaymentRequired = "payment_required"
	AgentCallInputRequired   = "input_required"
	AgentCallError           = "error"
)

type AgentPayloadRef struct {
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	URI      string `json:"uri,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

type AgentCallBudget struct {
	MaxAmount             string              `json:"max_amount,omitempty"`
	AcceptedPaymentAssets []AgentPaymentAsset `json:"accepted_payment_assets,omitempty"`
}

type AgentCallParams struct {
	Agent          string            `json:"agent,omitempty"`
	Tool           string            `json:"tool"`
	Input          json.RawMessage   `json:"input,omitempty"`
	PayloadRef     *AgentPayloadRef  `json:"payload_ref,omitempty"`
	PayloadRefs    []AgentPayloadRef `json:"payload_refs,omitempty"`
	Budget         *AgentCallBudget  `json:"budget,omitempty"`
	BidID          string            `json:"bid_id,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
}

type AgentCancelParams struct {
	JobID  string `json:"job_id"`
	Reason string `json:"reason,omitempty"`
}

type AgentJobGetParams struct {
	JobID string `json:"job_id"`
}

type AgentJobStatusParams struct {
	JobID     string   `json:"job_id"`
	WorkState string   `json:"work_state"`
	Message   string   `json:"message,omitempty"`
	Progress  *float64 `json:"progress,omitempty"`
}

type AgentJobCompleteParams struct {
	JobID        string            `json:"job_id"`
	Output       json.RawMessage   `json:"output,omitempty"`
	PayloadRef   *AgentPayloadRef  `json:"payload_ref,omitempty"`
	PayloadRefs  []AgentPayloadRef `json:"payload_refs,omitempty"`
	ResultDigest string            `json:"result_digest,omitempty"`
	Message      string            `json:"message,omitempty"`
}

type AgentJobFailParams struct {
	JobID   string `json:"job_id"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type AgentJobListParams struct {
	Role         string `json:"role,omitempty"`
	WorkState    string `json:"work_state,omitempty"`
	PaymentState string `json:"payment_state,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type AgentJob struct {
	JobID          string            `json:"job_id"`
	Buyer          string            `json:"buyer"`
	Seller         string            `json:"seller"`
	AgentID        string            `json:"agent_id,omitempty"`
	AgentName      string            `json:"agent_name,omitempty"`
	Tool           string            `json:"tool"`
	Capability     string            `json:"capability,omitempty"`
	BidID          string            `json:"bid_id,omitempty"`
	WorkState      string            `json:"work_state"`
	PaymentState   string            `json:"payment_state"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	StatusMessage  string            `json:"status_message,omitempty"`
	Progress       *float64          `json:"progress,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	InputDigest    string            `json:"input_digest,omitempty"`
	ResultDigest   string            `json:"result_digest,omitempty"`
	PayloadRefs    []AgentPayloadRef `json:"payload_refs,omitempty"`
	ResultRefs     []AgentPayloadRef `json:"result_refs,omitempty"`
	MessageID      string            `json:"message_id,omitempty"`
	CancelReason   string            `json:"cancel_reason,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	LastError      string            `json:"last_error,omitempty"`
	Delivery       *DeliveryMetadata `json:"delivery,omitempty"`
}

type AgentCallResultEnvelope struct {
	Type     string            `json:"type"`
	JobID    string            `json:"job_id,omitempty"`
	Job      *AgentJob         `json:"job,omitempty"`
	Output   any               `json:"output,omitempty"`
	Error    *CallError        `json:"error,omitempty"`
	Delivery *DeliveryMetadata `json:"delivery,omitempty"`
}

type CallError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AgentJobResult struct {
	Job AgentJob `json:"job"`
}

type AgentJobListResult struct {
	Jobs  []AgentJob `json:"jobs"`
	Count int        `json:"count"`
}

type AgentJobContext struct {
	JobID         string   `json:"job_id"`
	UpdateMethods []string `json:"update_methods,omitempty"`
}

type AgentToolCallMessage struct {
	JobID          string            `json:"job_id"`
	Tool           string            `json:"tool"`
	Capability     string            `json:"capability,omitempty"`
	Input          json.RawMessage   `json:"input,omitempty"`
	PayloadRef     *AgentPayloadRef  `json:"payload_ref,omitempty"`
	PayloadRefs    []AgentPayloadRef `json:"payload_refs,omitempty"`
	Budget         *AgentCallBudget  `json:"budget,omitempty"`
	BidID          string            `json:"bid_id,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	JobContext     AgentJobContext   `json:"job_context"`
}
