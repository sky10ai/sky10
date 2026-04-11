package link

import (
	"fmt"
	"sync"
	"time"
)

const maxRuntimeHealthEvents = 8

// HealthEvent is one recent network-health event exposed to operators.
type HealthEvent struct {
	Type   string    `json:"type"`
	Status string    `json:"status"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// RuntimeHealthSnapshot is the runtime-only network state tracked by the
// daemon outside durable discovery records.
type RuntimeHealthSnapshot struct {
	Reachability        string        `json:"reachability,omitempty"`
	LastPublishedAt     *time.Time    `json:"last_published_at,omitempty"`
	LastAddressChangeAt *time.Time    `json:"last_address_change_at,omitempty"`
	Events              []HealthEvent `json:"events,omitempty"`
}

// MailboxHealth summarizes durable async-delivery state without exposing the
// full mailbox record set in skylink.status.
type MailboxHealth struct {
	Queued              int        `json:"queued"`
	Failed              int        `json:"failed"`
	HandedOff           int        `json:"handed_off"`
	PendingPrivate      int        `json:"pending_private"`
	PendingSky10Network int        `json:"pending_sky10_network"`
	LastHandoffAt       *time.Time `json:"last_handoff_at,omitempty"`
	LastDeliveredAt     *time.Time `json:"last_delivered_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
}

// NetworkHealth is the operator-facing status snapshot returned by
// skylink.status.
type NetworkHealth struct {
	PreferredTransport      string         `json:"preferred_transport"`
	TransportDegradedReason string         `json:"transport_degraded_reason,omitempty"`
	DeliveryDegradedReason  string         `json:"delivery_degraded_reason,omitempty"`
	Reachability            string         `json:"reachability,omitempty"`
	PublicAddr              string         `json:"public_addr,omitempty"`
	MappingVariesByServer   bool           `json:"mapping_varies_by_server,omitempty"`
	ConnectedPrivatePeers   int            `json:"connected_private_peers"`
	LastPublishedAt         *time.Time     `json:"last_published_at,omitempty"`
	LastAddressChangeAt     *time.Time     `json:"last_address_change_at,omitempty"`
	Netcheck                NetcheckResult `json:"netcheck"`
	Mailbox                 MailboxHealth  `json:"mailbox"`
	Events                  []HealthEvent  `json:"events,omitempty"`
}

// RuntimeHealthTracker records recent operator-visible network events and
// state transitions.
type RuntimeHealthTracker struct {
	mu                  sync.RWMutex
	reachability        string
	lastPublishedAt     time.Time
	lastAddressChangeAt time.Time
	events              []HealthEvent
}

// NewRuntimeHealthTracker creates an in-memory tracker for network-health
// events.
func NewRuntimeHealthTracker() *RuntimeHealthTracker {
	return &RuntimeHealthTracker{}
}

// Snapshot returns the current runtime-health snapshot.
func (t *RuntimeHealthTracker) Snapshot() RuntimeHealthSnapshot {
	if t == nil {
		return RuntimeHealthSnapshot{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := RuntimeHealthSnapshot{
		Reachability:        t.reachability,
		LastPublishedAt:     timePtr(t.lastPublishedAt),
		LastAddressChangeAt: timePtr(t.lastAddressChangeAt),
	}
	if len(t.events) > 0 {
		out.Events = append([]HealthEvent(nil), t.events...)
	}
	return out
}

// RecordAddressUpdate records a local-address-change event.
func (t *RuntimeHealthTracker) RecordAddressUpdate(count int) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastAddressChangeAt = now
	t.recordLocked("addresses", "ok", fmt.Sprintf("%d current addrs", count), now)
}

// RecordReachability records a local reachability transition.
func (t *RuntimeHealthTracker) RecordReachability(reachability string) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reachability = reachability
	t.recordLocked("reachability", "ok", reachability, now)
}

// RecordPublish records one publish attempt to a discovery surface.
func (t *RuntimeHealthTracker) RecordPublish(target string, err error) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	status := "ok"
	detail := target
	if err != nil {
		status = "error"
		detail = fmt.Sprintf("%s: %v", target, err)
	} else {
		t.lastPublishedAt = now
	}
	t.recordLocked("publish", status, detail, now)
}

// RecordConnect records one explicit connect attempt.
func (t *RuntimeHealthTracker) RecordConnect(target string, err error) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	status := "ok"
	detail := target
	if err != nil {
		status = "error"
		detail = fmt.Sprintf("%s: %v", target, err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked("connect", status, detail, now)
}

// RecordMailbox records a mailbox-driven fallback event such as queueing,
// relay handoff, or reconnect delivery.
func (t *RuntimeHealthTracker) RecordMailbox(action, state, itemID string) {
	if t == nil {
		return
	}
	now := time.Now().UTC()
	detail := state
	if itemID != "" {
		detail = fmt.Sprintf("%s %s", itemID, state)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked("mailbox:"+action, "ok", detail, now)
}

func (t *RuntimeHealthTracker) recordLocked(eventType, status, detail string, at time.Time) {
	t.events = append([]HealthEvent{{
		Type:   eventType,
		Status: status,
		Detail: detail,
		At:     at,
	}}, t.events...)
	if len(t.events) > maxRuntimeHealthEvents {
		t.events = t.events[:maxRuntimeHealthEvents]
	}
}

func preferredTransportFromNetcheck(result NetcheckResult) string {
	if !result.UDP || result.MappingVariesByServer {
		return "tcp"
	}
	return "quic"
}

func transportDegradedReason(result NetcheckResult) string {
	switch {
	case !result.UDP:
		return "udp_unreachable"
	case result.MappingVariesByServer:
		return "udp_mapping_varies"
	default:
		return ""
	}
}

func deliveryDegradedReason(mailbox MailboxHealth) string {
	switch {
	case mailbox.Failed > 0:
		return "mailbox_failures_pending"
	case mailbox.HandedOff > 0:
		return "mailbox_handoff_pending"
	case mailbox.Queued > 0:
		return "mailbox_queue_pending"
	default:
		return ""
	}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	cp := t
	return &cp
}
