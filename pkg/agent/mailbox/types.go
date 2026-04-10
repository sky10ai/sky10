package mailbox

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv/collections"
)

const (
	ScopePrivateNetwork = "private_network"
	ScopeSky10Network   = "sky10_network"
)

const (
	PrincipalKindHuman           = "human"
	PrincipalKindLocalAgent      = "local_agent"
	PrincipalKindNetworkAgent    = "network_agent"
	PrincipalKindCapabilityQueue = "capability_queue"
)

const (
	ItemKindMessage         = "message"
	ItemKindTaskRequest     = "task_request"
	ItemKindApprovalRequest = "approval_request"
	ItemKindPaymentRequired = "payment_required"
	ItemKindPaymentProof    = "payment_proof"
	ItemKindResult          = "result"
	ItemKindReceipt         = "receipt"
	ItemKindError           = "error"
)

const (
	EventTypeCreated           = "created"
	EventTypeDeliveryAttempted = "delivery_attempted"
	EventTypeDelivered         = "delivered"
	EventTypeDeliveryFailed    = "delivery_failed"
	EventTypeSeen              = "seen"
	EventTypeClaimed           = "claimed"
	EventTypeLeaseExpired      = "lease_expired"
	EventTypeApproved          = "approved"
	EventTypeRejected          = "rejected"
	EventTypeCompleted         = "completed"
	EventTypeCancelled         = "cancelled"
	EventTypeExpired           = "expired"
	EventTypeDeadLettered      = "dead_lettered"
)

// PayloadRef points to payload bytes stored outside the inline mailbox
// envelope.
type PayloadRef = collections.PayloadRef

// Principal identifies a durable mailbox owner or queue.
type Principal struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Scope      string `json:"scope"`
	DeviceHint string `json:"device_hint,omitempty"`
	RouteHint  string `json:"route_hint,omitempty"`
}

// Validate checks whether a principal is structurally valid.
func (p Principal) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("principal id is required")
	}
	return nil
}

// ScopeOrDefault returns the principal scope with a private-network default.
func (p Principal) ScopeOrDefault() string {
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		return ScopePrivateNetwork
	}
	return scope
}

// RouteAddress returns the sky10 address used to reach this principal over the
// public network, when available.
func (p Principal) RouteAddress() string {
	if p.ScopeOrDefault() != ScopeSky10Network {
		return ""
	}
	hint := strings.TrimSpace(p.RouteHint)
	if hint != "" {
		if _, err := skykey.ParseAddress(hint); err == nil {
			return hint
		}
	}
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return ""
	}
	if _, err := skykey.ParseAddress(id); err == nil {
		return id
	}
	return ""
}

// Item is the durable mailbox envelope for a unit of work or protocol step.
type Item struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	From           Principal       `json:"from"`
	To             *Principal      `json:"to,omitempty"`
	TargetSkill    string          `json:"target_skill,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	PayloadRef     *PayloadRef     `json:"payload_ref,omitempty"`
	PayloadInline  json.RawMessage `json:"payload_inline,omitempty"`
	Priority       string          `json:"priority,omitempty"`
	ExpiresAt      time.Time       `json:"expires_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ValidateForCreate checks whether an item is valid for durable creation.
func (i Item) ValidateForCreate() error {
	if strings.TrimSpace(i.Kind) == "" {
		return fmt.Errorf("item kind is required")
	}
	if err := i.From.Validate(); err != nil {
		return fmt.Errorf("item from: %w", err)
	}
	if i.To != nil {
		if err := i.To.Validate(); err != nil {
			return fmt.Errorf("item to: %w", err)
		}
	}
	if i.To == nil && strings.TrimSpace(i.TargetSkill) == "" {
		return fmt.Errorf("item recipient or target skill is required")
	}
	if i.PayloadRef != nil {
		if err := i.PayloadRef.Validate(); err != nil {
			return fmt.Errorf("item payload ref: %w", err)
		}
	}
	return nil
}

// Scope returns the mailbox transport scope for this item.
func (i Item) Scope() string {
	if i.To != nil {
		return i.To.ScopeOrDefault()
	}
	return i.From.ScopeOrDefault()
}

// QueueName returns the claimable queue identifier for an item, if any.
func (i Item) QueueName() string {
	if i.To != nil && i.To.Kind == PrincipalKindCapabilityQueue {
		return strings.TrimSpace(i.To.ID)
	}
	if skill := strings.TrimSpace(i.TargetSkill); skill != "" {
		return "skill:" + skill
	}
	return ""
}

// RecipientID returns the durable recipient principal ID, if any.
func (i Item) RecipientID() string {
	if i.To == nil {
		return ""
	}
	return strings.TrimSpace(i.To.ID)
}

// Event is an immutable timeline transition for an item.
type Event struct {
	ItemID    string            `json:"item_id"`
	EventID   string            `json:"event_id,omitempty"`
	Type      string            `json:"type"`
	Actor     Principal         `json:"actor"`
	LeaseID   string            `json:"lease_id,omitempty"`
	Error     string            `json:"error,omitempty"`
	Timestamp time.Time         `json:"timestamp,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// Validate checks whether an event is structurally valid.
func (e Event) Validate() error {
	if strings.TrimSpace(e.ItemID) == "" {
		return fmt.Errorf("event item_id is required")
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("event type is required")
	}
	return nil
}

// Claim is the active lease-backed owner of a queue item.
type Claim struct {
	Queue      string    `json:"queue"`
	ItemID     string    `json:"item_id"`
	Holder     string    `json:"holder"`
	Token      string    `json:"token"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// State is the current derived state of a mailbox item.
type State string

const (
	StateQueued       State = "queued"
	StateDelivered    State = "delivered"
	StateClaimed      State = "claimed"
	StateApproved     State = "approved"
	StateCompleted    State = "completed"
	StateRejected     State = "rejected"
	StateCancelled    State = "cancelled"
	StateExpired      State = "expired"
	StateFailed       State = "failed"
	StateDeadLettered State = "dead_lettered"
)

// Record is the fully materialized mailbox view for one item.
type Record struct {
	Item   Item    `json:"item"`
	Events []Event `json:"events"`
	Claim  *Claim  `json:"claim,omitempty"`
	State  State   `json:"state"`
}

// LatestEvent returns the most recent event in the record timeline.
func (r Record) LatestEvent() (Event, bool) {
	if len(r.Events) == 0 {
		return Event{}, false
	}
	return cloneEvent(r.Events[len(r.Events)-1]), true
}

// Terminal reports whether the current state is terminal.
func (r Record) Terminal() bool {
	return stateTerminal(r.State)
}

// Failed reports whether the current state represents a failure or manual
// intervention condition.
func (r Record) Failed() bool {
	return stateFailed(r.State)
}

func stateTerminal(state State) bool {
	switch state {
	case StateCompleted, StateRejected, StateCancelled, StateExpired, StateDeadLettered:
		return true
	default:
		return false
	}
}

func stateFailed(state State) bool {
	switch state {
	case StateFailed, StateCancelled, StateExpired, StateDeadLettered:
		return true
	default:
		return false
	}
}

func clonePrincipal(p Principal) Principal {
	return p
}

func clonePayloadRef(ref *PayloadRef) *PayloadRef {
	if ref == nil {
		return nil
	}
	cp := *ref
	return &cp
}

func cloneItem(item Item) Item {
	cp := item
	cp.From = clonePrincipal(item.From)
	if item.To != nil {
		to := clonePrincipal(*item.To)
		cp.To = &to
	}
	cp.PayloadRef = clonePayloadRef(item.PayloadRef)
	if item.PayloadInline != nil {
		cp.PayloadInline = append(json.RawMessage(nil), item.PayloadInline...)
	}
	return cp
}

func cloneEvent(event Event) Event {
	cp := event
	cp.Actor = clonePrincipal(event.Actor)
	if event.Meta != nil {
		cp.Meta = make(map[string]string, len(event.Meta))
		for k, v := range event.Meta {
			cp.Meta[k] = v
		}
	}
	return cp
}

func cloneClaim(claim *Claim) *Claim {
	if claim == nil {
		return nil
	}
	cp := *claim
	return &cp
}

func cloneRecord(record Record) Record {
	out := Record{
		Item:  cloneItem(record.Item),
		Claim: cloneClaim(record.Claim),
		State: record.State,
	}
	out.Events = make([]Event, len(record.Events))
	for i, event := range record.Events {
		out.Events[i] = cloneEvent(event)
	}
	return out
}
