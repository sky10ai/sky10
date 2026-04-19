package messaging

import (
	"fmt"
	"strings"
	"time"
)

// Stable identifier types for the messaging domain.
type (
	AdapterID      string
	ConnectionID   string
	IdentityID     string
	ConversationID string
	MessageID      string
	DraftID        string
	PolicyID       string
	ExposureID     string
	EventID        string
)

// AuthMethod describes how a messaging connection authenticates.
type AuthMethod string

const (
	AuthMethodNone        AuthMethod = "none"
	AuthMethodOAuth2      AuthMethod = "oauth2"
	AuthMethodAppPassword AuthMethod = "app_password"
	AuthMethodBasic       AuthMethod = "basic"
	AuthMethodBotToken    AuthMethod = "bot_token"
	AuthMethodBearerToken AuthMethod = "bearer_token"
	AuthMethodAPIKey      AuthMethod = "api_key"
	AuthMethodSession     AuthMethod = "session"
)

// ConnectionStatus describes the broker's current view of a connection.
type ConnectionStatus string

const (
	ConnectionStatusUnknown      ConnectionStatus = "unknown"
	ConnectionStatusConnecting   ConnectionStatus = "connecting"
	ConnectionStatusConnected    ConnectionStatus = "connected"
	ConnectionStatusDegraded     ConnectionStatus = "degraded"
	ConnectionStatusAuthRequired ConnectionStatus = "auth_required"
	ConnectionStatusDisabled     ConnectionStatus = "disabled"
	ConnectionStatusError        ConnectionStatus = "error"
)

// IdentityKind describes the local persona exposed by a connection.
type IdentityKind string

const (
	IdentityKindEmail       IdentityKind = "email"
	IdentityKindPhoneNumber IdentityKind = "phone_number"
	IdentityKindBot         IdentityKind = "bot"
	IdentityKindPage        IdentityKind = "page"
	IdentityKindAccount     IdentityKind = "account"
	IdentityKindWebhook     IdentityKind = "webhook"
)

// ConversationKind describes the top-level shape of a conversation.
type ConversationKind string

const (
	ConversationKindDirect      ConversationKind = "direct"
	ConversationKindGroup       ConversationKind = "group"
	ConversationKindChannel     ConversationKind = "channel"
	ConversationKindThread      ConversationKind = "thread"
	ConversationKindEmailThread ConversationKind = "email_thread"
)

// ParticipantKind describes a participant in a conversation.
type ParticipantKind string

const (
	ParticipantKindUser    ParticipantKind = "user"
	ParticipantKindBot     ParticipantKind = "bot"
	ParticipantKindPage    ParticipantKind = "page"
	ParticipantKindAccount ParticipantKind = "account"
	ParticipantKindSystem  ParticipantKind = "system"
)

// MessageDirection describes whether a message is inbound or outbound from the
// perspective of the local connection identity.
type MessageDirection string

const (
	MessageDirectionInbound  MessageDirection = "inbound"
	MessageDirectionOutbound MessageDirection = "outbound"
)

// MessageStatus describes broker-visible lifecycle state for a normalized
// message record.
type MessageStatus string

const (
	MessageStatusUnknown   MessageStatus = "unknown"
	MessageStatusReceived  MessageStatus = "received"
	MessageStatusQueued    MessageStatus = "queued"
	MessageStatusSent      MessageStatus = "sent"
	MessageStatusDelivered MessageStatus = "delivered"
	MessageStatusRead      MessageStatus = "read"
	MessageStatusFailed    MessageStatus = "failed"
)

// DraftStatus describes lifecycle state for an unsent outbound draft.
type DraftStatus string

const (
	DraftStatusPending          DraftStatus = "pending"
	DraftStatusApprovalRequired DraftStatus = "approval_required"
	DraftStatusApproved         DraftStatus = "approved"
	DraftStatusSending          DraftStatus = "sending"
	DraftStatusSent             DraftStatus = "sent"
	DraftStatusFailed           DraftStatus = "failed"
	DraftStatusRejected         DraftStatus = "rejected"
)

// MessagePartKind describes one normalized content fragment inside a message
// or draft.
type MessagePartKind string

const (
	MessagePartKindText     MessagePartKind = "text"
	MessagePartKindMarkdown MessagePartKind = "markdown"
	MessagePartKindHTML     MessagePartKind = "html"
	MessagePartKindFile     MessagePartKind = "file"
	MessagePartKindImage    MessagePartKind = "image"
)

// ExposureSubjectKind identifies who receives an exposure grant.
type ExposureSubjectKind string

const (
	ExposureSubjectKindAgent   ExposureSubjectKind = "agent"
	ExposureSubjectKindRuntime ExposureSubjectKind = "runtime"
	ExposureSubjectKindUser    ExposureSubjectKind = "user"
	ExposureSubjectKindService ExposureSubjectKind = "service"
)

// EventType is the normalized class of a broker event.
type EventType string

const (
	EventTypeConnectionUpdated   EventType = "connection_updated"
	EventTypeIdentityDiscovered  EventType = "identity_discovered"
	EventTypeConversationUpdated EventType = "conversation_updated"
	EventTypeMessageReceived     EventType = "message_received"
	EventTypeMessageUpdated      EventType = "message_updated"
	EventTypeDraftUpdated        EventType = "draft_updated"
	EventTypeDraftSendRequested  EventType = "draft_send_requested"
	EventTypeDeliveryUpdated     EventType = "delivery_updated"
	EventTypeApprovalRequired    EventType = "approval_required"
	EventTypeApprovalResolved    EventType = "approval_resolved"
	EventTypeAuthExpired         EventType = "auth_expired"
)

// Adapter describes one external platform integration implementation.
type Adapter struct {
	ID           AdapterID    `json:"id"`
	DisplayName  string       `json:"display_name"`
	Version      string       `json:"version,omitempty"`
	Description  string       `json:"description,omitempty"`
	AuthMethods  []AuthMethod `json:"auth_methods,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

// Validate checks whether an adapter definition is structurally valid.
func (a Adapter) Validate() error {
	if err := requireID(string(a.ID), "adapter id"); err != nil {
		return err
	}
	if err := requireText(a.DisplayName, "adapter display_name"); err != nil {
		return err
	}
	for idx, method := range a.AuthMethods {
		if strings.TrimSpace(string(method)) == "" {
			return fmt.Errorf("adapter auth_methods[%d] is required", idx)
		}
	}
	return nil
}

// AuthInfo describes how a connection authenticates. Secret material should be
// referenced indirectly through CredentialRef rather than embedded directly.
type AuthInfo struct {
	Method          AuthMethod `json:"method,omitempty"`
	CredentialRef   string     `json:"credential_ref,omitempty"`
	ExternalAccount string     `json:"external_account,omitempty"`
	TenantID        string     `json:"tenant_id,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Scopes          []string   `json:"scopes,omitempty"`
}

// Configured reports whether any auth metadata is present.
func (a AuthInfo) Configured() bool {
	return strings.TrimSpace(string(a.Method)) != "" ||
		strings.TrimSpace(a.CredentialRef) != "" ||
		strings.TrimSpace(a.ExternalAccount) != "" ||
		strings.TrimSpace(a.TenantID) != "" ||
		a.ExpiresAt != nil ||
		len(a.Scopes) > 0
}

// Validate checks whether auth metadata is structurally valid.
func (a AuthInfo) Validate() error {
	if !a.Configured() {
		return nil
	}
	if strings.TrimSpace(string(a.Method)) == "" {
		return fmt.Errorf("auth method is required")
	}
	if a.Method != AuthMethodNone && strings.TrimSpace(a.CredentialRef) == "" {
		return fmt.Errorf("auth credential_ref is required")
	}
	for idx, scope := range a.Scopes {
		if strings.TrimSpace(scope) == "" {
			return fmt.Errorf("auth scopes[%d] is required", idx)
		}
	}
	return nil
}

// Connection is one configured instance of an adapter with auth and policy
// attachment.
type Connection struct {
	ID              ConnectionID      `json:"id"`
	AdapterID       AdapterID         `json:"adapter_id"`
	Label           string            `json:"label"`
	Status          ConnectionStatus  `json:"status"`
	Auth            AuthInfo          `json:"auth,omitempty"`
	DefaultPolicyID PolicyID          `json:"default_policy_id,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ConnectedAt     time.Time         `json:"connected_at,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at,omitempty"`
}

// Validate checks whether a connection is structurally valid.
func (c Connection) Validate() error {
	if err := requireID(string(c.ID), "connection id"); err != nil {
		return err
	}
	if err := requireID(string(c.AdapterID), "connection adapter_id"); err != nil {
		return err
	}
	if err := requireText(c.Label, "connection label"); err != nil {
		return err
	}
	if err := c.Auth.Validate(); err != nil {
		return fmt.Errorf("connection auth: %w", err)
	}
	return nil
}

// Identity is one local messaging persona available through a connection.
type Identity struct {
	ID           IdentityID        `json:"id"`
	ConnectionID ConnectionID      `json:"connection_id"`
	Kind         IdentityKind      `json:"kind"`
	RemoteID     string            `json:"remote_id,omitempty"`
	Address      string            `json:"address,omitempty"`
	DisplayName  string            `json:"display_name,omitempty"`
	CanReceive   bool              `json:"can_receive"`
	CanSend      bool              `json:"can_send"`
	IsDefault    bool              `json:"is_default"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether an identity is structurally valid.
func (i Identity) Validate() error {
	if err := requireID(string(i.ID), "identity id"); err != nil {
		return err
	}
	if err := requireID(string(i.ConnectionID), "identity connection_id"); err != nil {
		return err
	}
	if strings.TrimSpace(string(i.Kind)) == "" {
		return fmt.Errorf("identity kind is required")
	}
	if strings.TrimSpace(i.RemoteID) == "" && strings.TrimSpace(i.Address) == "" {
		return fmt.Errorf("identity remote_id or address is required")
	}
	return nil
}

// Participant is a normalized member of a conversation or sender of a message.
type Participant struct {
	ID          string            `json:"id,omitempty"`
	Kind        ParticipantKind   `json:"kind"`
	RemoteID    string            `json:"remote_id,omitempty"`
	Address     string            `json:"address,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	IdentityID  IdentityID        `json:"identity_id,omitempty"`
	IsLocal     bool              `json:"is_local,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a participant is structurally valid.
func (p Participant) Validate() error {
	if strings.TrimSpace(string(p.Kind)) == "" {
		return fmt.Errorf("participant kind is required")
	}
	if strings.TrimSpace(p.ID) == "" &&
		strings.TrimSpace(p.RemoteID) == "" &&
		strings.TrimSpace(p.Address) == "" &&
		strings.TrimSpace(p.DisplayName) == "" &&
		strings.TrimSpace(string(p.IdentityID)) == "" {
		return fmt.Errorf("participant identity is required")
	}
	return nil
}

// Conversation is one normalized thread, DM, room, or email thread.
type Conversation struct {
	ID              ConversationID    `json:"id"`
	ConnectionID    ConnectionID      `json:"connection_id"`
	LocalIdentityID IdentityID        `json:"local_identity_id"`
	Kind            ConversationKind  `json:"kind"`
	RemoteID        string            `json:"remote_id,omitempty"`
	Title           string            `json:"title,omitempty"`
	Participants    []Participant     `json:"participants,omitempty"`
	ParentID        ConversationID    `json:"parent_id,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a conversation is structurally valid.
func (c Conversation) Validate() error {
	if err := requireID(string(c.ID), "conversation id"); err != nil {
		return err
	}
	if err := requireID(string(c.ConnectionID), "conversation connection_id"); err != nil {
		return err
	}
	if err := requireID(string(c.LocalIdentityID), "conversation local_identity_id"); err != nil {
		return err
	}
	if strings.TrimSpace(string(c.Kind)) == "" {
		return fmt.Errorf("conversation kind is required")
	}
	for idx, participant := range c.Participants {
		if err := participant.Validate(); err != nil {
			return fmt.Errorf("conversation participants[%d]: %w", idx, err)
		}
	}
	return nil
}

// MessagePart is one normalized content fragment inside a message or draft.
type MessagePart struct {
	Kind        MessagePartKind   `json:"kind"`
	ContentType string            `json:"content_type,omitempty"`
	Text        string            `json:"text,omitempty"`
	FileName    string            `json:"file_name,omitempty"`
	Ref         string            `json:"ref,omitempty"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a message part is structurally valid.
func (p MessagePart) Validate() error {
	switch p.Kind {
	case MessagePartKindText, MessagePartKindMarkdown, MessagePartKindHTML:
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("message part text is required")
		}
	case MessagePartKindFile, MessagePartKindImage:
		if strings.TrimSpace(p.Ref) == "" && strings.TrimSpace(p.FileName) == "" {
			return fmt.Errorf("message part ref or file_name is required")
		}
	default:
		return fmt.Errorf("message part kind is required")
	}
	if p.SizeBytes < 0 {
		return fmt.Errorf("message part size_bytes must be >= 0")
	}
	return nil
}

// Message is one normalized inbound or outbound message.
type Message struct {
	ID              MessageID         `json:"id"`
	ConnectionID    ConnectionID      `json:"connection_id"`
	ConversationID  ConversationID    `json:"conversation_id"`
	LocalIdentityID IdentityID        `json:"local_identity_id"`
	RemoteID        string            `json:"remote_id,omitempty"`
	Direction       MessageDirection  `json:"direction"`
	Sender          Participant       `json:"sender"`
	Parts           []MessagePart     `json:"parts"`
	CreatedAt       time.Time         `json:"created_at"`
	EditedAt        *time.Time        `json:"edited_at,omitempty"`
	ReplyToRemoteID string            `json:"reply_to_remote_id,omitempty"`
	Status          MessageStatus     `json:"status"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a message is structurally valid.
func (m Message) Validate() error {
	if err := requireID(string(m.ID), "message id"); err != nil {
		return err
	}
	if err := requireID(string(m.ConnectionID), "message connection_id"); err != nil {
		return err
	}
	if err := requireID(string(m.ConversationID), "message conversation_id"); err != nil {
		return err
	}
	if err := requireID(string(m.LocalIdentityID), "message local_identity_id"); err != nil {
		return err
	}
	if strings.TrimSpace(string(m.Direction)) == "" {
		return fmt.Errorf("message direction is required")
	}
	if err := m.Sender.Validate(); err != nil {
		return fmt.Errorf("message sender: %w", err)
	}
	if len(m.Parts) == 0 {
		return fmt.Errorf("message parts are required")
	}
	for idx, part := range m.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("message parts[%d]: %w", idx, err)
		}
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("message created_at is required")
	}
	return nil
}

// Draft is one unsent outbound message proposal.
type Draft struct {
	ID              DraftID           `json:"id"`
	ConnectionID    ConnectionID      `json:"connection_id"`
	ConversationID  ConversationID    `json:"conversation_id"`
	LocalIdentityID IdentityID        `json:"local_identity_id"`
	ReplyToRemoteID string            `json:"reply_to_remote_id,omitempty"`
	Parts           []MessagePart     `json:"parts"`
	Status          DraftStatus       `json:"status"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a draft is structurally valid.
func (d Draft) Validate() error {
	if err := requireID(string(d.ID), "draft id"); err != nil {
		return err
	}
	if err := requireID(string(d.ConnectionID), "draft connection_id"); err != nil {
		return err
	}
	if err := requireID(string(d.ConversationID), "draft conversation_id"); err != nil {
		return err
	}
	if err := requireID(string(d.LocalIdentityID), "draft local_identity_id"); err != nil {
		return err
	}
	if len(d.Parts) == 0 {
		return fmt.Errorf("draft parts are required")
	}
	for idx, part := range d.Parts {
		if err := part.Validate(); err != nil {
			return fmt.Errorf("draft parts[%d]: %w", idx, err)
		}
	}
	if strings.TrimSpace(string(d.Status)) == "" {
		return fmt.Errorf("draft status is required")
	}
	return nil
}

// PolicyRules is the broker-enforced allow/deny surface attached to a
// connection or exposure.
type PolicyRules struct {
	ReadInbound           bool `json:"read_inbound"`
	CreateDrafts          bool `json:"create_drafts"`
	SendMessages          bool `json:"send_messages"`
	RequireApproval       bool `json:"require_approval"`
	ReplyOnly             bool `json:"reply_only"`
	AllowNewConversations bool `json:"allow_new_conversations"`
	AllowAttachments      bool `json:"allow_attachments"`
	MarkRead              bool `json:"mark_read"`
}

// Policy is a named bundle of messaging permission rules.
type Policy struct {
	ID       PolicyID          `json:"id"`
	Name     string            `json:"name"`
	Rules    PolicyRules       `json:"rules"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether a policy is structurally valid.
func (p Policy) Validate() error {
	if err := requireID(string(p.ID), "policy id"); err != nil {
		return err
	}
	if err := requireText(p.Name, "policy name"); err != nil {
		return err
	}
	return nil
}

// Exposure grants a subject access to a connection under a policy.
type Exposure struct {
	ID           ExposureID          `json:"id"`
	ConnectionID ConnectionID        `json:"connection_id"`
	SubjectID    string              `json:"subject_id"`
	SubjectKind  ExposureSubjectKind `json:"subject_kind"`
	PolicyID     PolicyID            `json:"policy_id,omitempty"`
	Enabled      bool                `json:"enabled"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
}

// Validate checks whether an exposure is structurally valid.
func (e Exposure) Validate() error {
	if err := requireID(string(e.ID), "exposure id"); err != nil {
		return err
	}
	if err := requireID(string(e.ConnectionID), "exposure connection_id"); err != nil {
		return err
	}
	if err := requireText(e.SubjectID, "exposure subject_id"); err != nil {
		return err
	}
	if strings.TrimSpace(string(e.SubjectKind)) == "" {
		return fmt.Errorf("exposure subject_kind is required")
	}
	return nil
}

// Event is a normalized broker event emitted by adapters, approvals, or
// outbound delivery changes.
type Event struct {
	ID             EventID           `json:"id"`
	Type           EventType         `json:"type"`
	ConnectionID   ConnectionID      `json:"connection_id"`
	ConversationID ConversationID    `json:"conversation_id,omitempty"`
	MessageID      MessageID         `json:"message_id,omitempty"`
	DraftID        DraftID           `json:"draft_id,omitempty"`
	ExposureID     ExposureID        `json:"exposure_id,omitempty"`
	Timestamp      time.Time         `json:"timestamp"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// Validate checks whether an event is structurally valid.
func (e Event) Validate() error {
	if err := requireID(string(e.ID), "event id"); err != nil {
		return err
	}
	if strings.TrimSpace(string(e.Type)) == "" {
		return fmt.Errorf("event type is required")
	}
	if err := requireID(string(e.ConnectionID), "event connection_id"); err != nil {
		return err
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("event timestamp is required")
	}
	return nil
}

// Capabilities declares the operations a platform adapter supports.
type Capabilities struct {
	ReceiveMessages   bool `json:"receive_messages"`
	SendMessages      bool `json:"send_messages"`
	CreateDrafts      bool `json:"create_drafts"`
	UpdateDrafts      bool `json:"update_drafts"`
	DeleteDrafts      bool `json:"delete_drafts"`
	ListConversations bool `json:"list_conversations"`
	ListMessages      bool `json:"list_messages"`
	Threading         bool `json:"threading"`
	Attachments       bool `json:"attachments"`
	Webhooks          bool `json:"webhooks"`
	Polling           bool `json:"polling"`
	MarkRead          bool `json:"mark_read"`
	TypingIndicators  bool `json:"typing_indicators"`
	DeliveryStatus    bool `json:"delivery_status"`
	Reactions         bool `json:"reactions"`
	Edits             bool `json:"edits"`
	Deletes           bool `json:"deletes"`
}

func requireID(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func requireText(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}
