package agent

import (
	"strings"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

const (
	DeliveryPolicyLiveOnly      = "live_only"
	DeliveryPolicyMailboxBacked = "mailbox_backed"
	DeliveryScopeSandbox        = "sandbox"

	DeliveryTransportLocalRegistry = "local_registry"
	DeliveryTransportSkylink       = "skylink"
	DeliveryTransportSandboxProxy  = "sandbox_proxy"
)

// DeliveryMetadata explains how a caller-visible send or mailbox operation was
// handled: pure live delivery, durable queueing, or relay handoff.
type DeliveryMetadata struct {
	Policy           string `json:"policy"`
	Scope            string `json:"scope,omitempty"`
	Status           string `json:"status"`
	LiveTransport    string `json:"live_transport,omitempty"`
	DurableTransport string `json:"durable_transport,omitempty"`
	LastTransport    string `json:"last_transport,omitempty"`
	MailboxItemID    string `json:"mailbox_item_id,omitempty"`
	MailboxState     string `json:"mailbox_state,omitempty"`
	LastEvent        string `json:"last_event,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	LiveAttempted    bool   `json:"live_attempted"`
	DurableUsed      bool   `json:"durable_used"`
}

// DeliveryPolicyDescription documents the intended delivery contract for one
// user-visible operation.
type DeliveryPolicyDescription struct {
	Policy           string `json:"policy"`
	Scope            string `json:"scope,omitempty"`
	LiveTransport    string `json:"live_transport,omitempty"`
	DurableTransport string `json:"durable_transport,omitempty"`
	Description      string `json:"description"`
}

// SendResult is the response from agent.send.
type SendResult struct {
	ID            string           `json:"id"`
	Status        string           `json:"status"`
	MailboxItemID string           `json:"mailbox_item_id,omitempty"`
	Delivery      DeliveryMetadata `json:"delivery"`
}

func deliveryPolicies(mailboxConfigured bool) map[string]DeliveryPolicyDescription {
	agentSendPolicy := DeliveryPolicyDescription{
		Policy:        DeliveryPolicyLiveOnly,
		Scope:         agentmailbox.ScopePrivateNetwork,
		LiveTransport: DeliveryTransportLocalRegistry + "," + DeliveryTransportSkylink,
		Description:   "agent.send delivers directly to a local agent or a connected private-network device and fails fast when durable mailbox fallback is unavailable.",
	}
	if mailboxConfigured {
		agentSendPolicy.Policy = DeliveryPolicyMailboxBacked
		agentSendPolicy.DurableTransport = "private_mailbox"
		agentSendPolicy.Description = "agent.send delivers live first and degrades into a durable private-network mailbox when the recipient is offline or late."
	}

	return map[string]DeliveryPolicyDescription{
		"agent_send": agentSendPolicy,
		"mailbox_private_network": {
			Policy:           DeliveryPolicyMailboxBacked,
			Scope:            agentmailbox.ScopePrivateNetwork,
			LiveTransport:    DeliveryTransportLocalRegistry + "," + DeliveryTransportSkylink,
			DurableTransport: "private_mailbox",
			Description:      "private-network mailbox items persist first, then deliver live on local registration or private-network reconnect.",
		},
		"mailbox_sky10_network": {
			Policy:           DeliveryPolicyMailboxBacked,
			Scope:            agentmailbox.ScopeSky10Network,
			LiveTransport:    DeliveryTransportSkylink,
			DurableTransport: "nostr_dropbox",
			Description:      "sky10-network mailbox items try live skylink delivery first, then hand off through sealed Nostr dropbox relay when direct routing is unavailable.",
		},
		"mailbox_public_queue": {
			Policy:           DeliveryPolicyMailboxBacked,
			Scope:            agentmailbox.ScopeSky10Network,
			DurableTransport: "nostr_queue",
			Description:      "public queue offers persist locally and publish sealed queue advertisements until one worker claims them.",
		},
	}
}

func mailboxDeliveryMetadata(record agentmailbox.Record) DeliveryMetadata {
	meta := DeliveryMetadata{
		Policy:           DeliveryPolicyMailboxBacked,
		Scope:            record.Item.Scope(),
		Status:           "queued",
		DurableTransport: mailboxDurableTransport(record.Item),
		MailboxItemID:    record.Item.ID,
		MailboxState:     string(record.State),
		DurableUsed:      true,
	}

	event, ok := latestDeliveryEvent(record)
	if ok {
		meta.LastEvent = event.Type
		meta.LastError = strings.TrimSpace(event.Error)
		transport := strings.TrimSpace(event.Meta["transport"])
		if transport != "" {
			meta.LastTransport = transport
		}
		if isLiveDeliveryTransport(transport) && (event.Type == agentmailbox.EventTypeDeliveryAttempted || event.Type == agentmailbox.EventTypeDeliveryFailed || event.Type == agentmailbox.EventTypeDelivered) {
			meta.LiveAttempted = true
		}
	}

	if liveTransport := firstAttemptedTransport(record); liveTransport != "" {
		meta.LiveTransport = liveTransport
		meta.LiveAttempted = true
	}

	switch {
	case record.State == agentmailbox.StateDelivered:
		meta.Status = "delivered"
	case meta.LastEvent == agentmailbox.EventTypeDelivered:
		meta.Status = "delivered"
	case meta.MailboxState == string(agentmailbox.StateAssigned):
		meta.Status = "assigned"
	case meta.MailboxState == string(agentmailbox.StateClaimed):
		meta.Status = "claimed"
	case meta.MailboxState == string(agentmailbox.StateApproved):
		meta.Status = "approved"
	case meta.MailboxState == string(agentmailbox.StateCompleted):
		meta.Status = "completed"
	case meta.MailboxState == string(agentmailbox.StateRejected):
		meta.Status = "rejected"
	case meta.MailboxState == string(agentmailbox.StateCancelled):
		meta.Status = "cancelled"
	case meta.MailboxState == string(agentmailbox.StateExpired):
		meta.Status = "expired"
	case meta.MailboxState == string(agentmailbox.StateDeadLettered):
		meta.Status = "dead_lettered"
	case meta.LastEvent == agentmailbox.EventTypeHandedOff:
		meta.Status = "handed_off"
	default:
		meta.Status = "queued"
	}

	if meta.LastTransport == "" {
		if meta.Status == "queued" || meta.Status == "handed_off" {
			meta.LastTransport = meta.DurableTransport
		} else if meta.LiveTransport != "" {
			meta.LastTransport = meta.LiveTransport
		}
	}

	return meta
}

func mailboxDurableTransport(item agentmailbox.Item) string {
	switch item.Scope() {
	case agentmailbox.ScopeSky10Network:
		if item.QueueName() != "" {
			return "nostr_queue"
		}
		return "nostr_dropbox"
	default:
		return "private_mailbox"
	}
}

func latestDeliveryEvent(record agentmailbox.Record) (agentmailbox.Event, bool) {
	for i := len(record.Events) - 1; i >= 0; i-- {
		event := record.Events[i]
		switch event.Type {
		case agentmailbox.EventTypeHandedOff,
			agentmailbox.EventTypeDelivered,
			agentmailbox.EventTypeDeliveryFailed,
			agentmailbox.EventTypeDeliveryAttempted:
			return event, true
		}
	}
	return agentmailbox.Event{}, false
}

func firstAttemptedTransport(record agentmailbox.Record) string {
	for _, event := range record.Events {
		if event.Type != agentmailbox.EventTypeDeliveryAttempted {
			continue
		}
		if transport := strings.TrimSpace(event.Meta["transport"]); transport != "" {
			if isLiveDeliveryTransport(transport) {
				return transport
			}
		}
	}
	return ""
}

func isLiveDeliveryTransport(transport string) bool {
	switch strings.TrimSpace(transport) {
	case DeliveryTransportLocalRegistry, DeliveryTransportSkylink, DeliveryTransportSandboxProxy:
		return true
	default:
		return false
	}
}
