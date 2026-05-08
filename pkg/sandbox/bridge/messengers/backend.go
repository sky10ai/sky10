package messengers

import (
	"context"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
)

// Backend is the host-side surface the messenger bridge handlers delegate to.
// Implementations must scope every operation to AgentID, which is stamped by
// the bridge transport and never accepted from the payload.
type Backend interface {
	ListConnections(ctx context.Context, params ListConnectionsParams) ([]messaging.Connection, error)
	ListConversations(ctx context.Context, params ListConversationsParams) ([]messaging.Conversation, error)
	ListEvents(ctx context.Context, params ListEventsParams) ([]messaging.Event, error)
	GetMessages(ctx context.Context, params GetMessagesParams) ([]messaging.Message, error)
	CreateDraft(ctx context.Context, params CreateDraftParams) (messagingbroker.DraftMutationResult, error)
	RequestSend(ctx context.Context, params RequestSendParams) (messagingbroker.RequestSendDraftResult, error)
}

// ListConnectionsParams scopes connection discovery for one agent. AdapterID
// is optional; when set, only matching connections are returned.
type ListConnectionsParams struct {
	AgentID   string              `json:"-"`
	AdapterID messaging.AdapterID `json:"adapter_id,omitempty"`
}

// ListConversationsParams lists cached conversations for one exposed
// connection.
type ListConversationsParams struct {
	AgentID      string                 `json:"-"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
}

// ListEventsParams lists durable broker events for one exposed connection.
// AfterEventID is an optional cursor; Limit defaults in the backend when zero.
type ListEventsParams struct {
	AgentID      string                 `json:"-"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	AfterEventID messaging.EventID      `json:"after_event_id,omitempty"`
	Limit        int                    `json:"limit,omitempty"`
}

// GetMessagesParams reads cached messages in one exposed conversation.
type GetMessagesParams struct {
	AgentID        string                   `json:"-"`
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id"`
}

// CreateDraftParams creates a pending outbound draft under one exposure.
// DraftID and LocalIdentityID are optional conveniences: the host fills DraftID
// when absent, and derives LocalIdentityID from the conversation when possible.
type CreateDraftParams struct {
	AgentID          string                   `json:"-"`
	DraftID          messaging.DraftID        `json:"draft_id,omitempty"`
	ConnectionID     messaging.ConnectionID   `json:"connection_id"`
	ConversationID   messaging.ConversationID `json:"conversation_id"`
	LocalIdentityID  messaging.IdentityID     `json:"local_identity_id,omitempty"`
	ReplyToMessageID messaging.MessageID      `json:"reply_to_message_id,omitempty"`
	ReplyToRemoteID  string                   `json:"reply_to_remote_id,omitempty"`
	Parts            []messaging.MessagePart  `json:"parts"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
}

// RequestSendParams asks the broker to send or approval-gate one draft.
type RequestSendParams struct {
	AgentID         string            `json:"-"`
	DraftID         messaging.DraftID `json:"draft_id"`
	NewConversation bool              `json:"new_conversation,omitempty"`
}
