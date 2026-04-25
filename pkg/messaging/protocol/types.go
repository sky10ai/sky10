package protocol

import (
	"fmt"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
)

const (
	// Name is the stable adapter protocol namespace.
	Name = "sky10.messaging.adapter"
	// Version is the current adapter protocol version.
	Version = "v1alpha1"
)

// Method is one adapter RPC operation.
type Method string

const (
	MethodDescribe            Method = "messaging.adapter.describe"
	MethodValidateConfig      Method = "messaging.adapter.validateConfig"
	MethodConnect             Method = "messaging.adapter.connect"
	MethodRefresh             Method = "messaging.adapter.refresh"
	MethodListIdentities      Method = "messaging.adapter.listIdentities"
	MethodListConversations   Method = "messaging.adapter.listConversations"
	MethodListMessages        Method = "messaging.adapter.listMessages"
	MethodGetMessage          Method = "messaging.adapter.getMessage"
	MethodListContainers      Method = "messaging.adapter.listContainers"
	MethodResolveIdentity     Method = "messaging.adapter.resolveIdentity"
	MethodSearchIdentities    Method = "messaging.adapter.searchIdentities"
	MethodSearchConversations Method = "messaging.adapter.searchConversations"
	MethodSearchMessages      Method = "messaging.adapter.searchMessages"
	MethodCreateDraft         Method = "messaging.adapter.createDraft"
	MethodUpdateDraft         Method = "messaging.adapter.updateDraft"
	MethodDeleteDraft         Method = "messaging.adapter.deleteDraft"
	MethodSendMessage         Method = "messaging.adapter.sendMessage"
	MethodReplyMessage        Method = "messaging.adapter.replyMessage"
	MethodMoveMessages        Method = "messaging.adapter.moveMessages"
	MethodMoveConversation    Method = "messaging.adapter.moveConversation"
	MethodArchiveMessages     Method = "messaging.adapter.archiveMessages"
	MethodArchiveConversation Method = "messaging.adapter.archiveConversation"
	MethodApplyLabels         Method = "messaging.adapter.applyLabels"
	MethodMarkRead            Method = "messaging.adapter.markRead"
	MethodHandleWebhook       Method = "messaging.adapter.handleWebhook"
	MethodPoll                Method = "messaging.adapter.poll"
	MethodHealth              Method = "messaging.adapter.health"
)

// ProtocolInfo describes the adapter contract version the process speaks.
type ProtocolInfo struct {
	Name               string   `json:"name"`
	Version            string   `json:"version"`
	CompatibleVersions []string `json:"compatible_versions,omitempty"`
	Transport          string   `json:"transport,omitempty"`
}

// Validate reports whether protocol metadata is structurally valid.
func (p ProtocolInfo) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("protocol name is required")
	}
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("protocol version is required")
	}
	for idx, version := range p.CompatibleVersions {
		if strings.TrimSpace(version) == "" {
			return fmt.Errorf("protocol compatible_versions[%d] is required", idx)
		}
	}
	return nil
}

// CurrentProtocol returns the current protocol descriptor.
func CurrentProtocol() ProtocolInfo {
	return ProtocolInfo{
		Name:               Name,
		Version:            Version,
		CompatibleVersions: []string{Version},
		Transport:          "stdio-jsonrpc",
	}
}

// ValidationIssueSeverity is the severity of one config validation issue.
type ValidationIssueSeverity string

const (
	ValidationIssueError   ValidationIssueSeverity = "error"
	ValidationIssueWarning ValidationIssueSeverity = "warning"
	ValidationIssueInfo    ValidationIssueSeverity = "info"
)

// ValidationIssue is one config problem or warning returned by ValidateConfig.
type ValidationIssue struct {
	Severity ValidationIssueSeverity `json:"severity"`
	Field    string                  `json:"field,omitempty"`
	Code     string                  `json:"code,omitempty"`
	Message  string                  `json:"message"`
}

// RuntimePaths contains broker-owned directories made available to one adapter
// connection. Paths should already be OS-native and absolute when supplied.
type RuntimePaths struct {
	RootDir        string `json:"root_dir,omitempty"`
	StateDir       string `json:"state_dir,omitempty"`
	CacheDir       string `json:"cache_dir,omitempty"`
	RuntimeDir     string `json:"runtime_dir,omitempty"`
	SecretsDir     string `json:"secrets_dir,omitempty"`
	CheckpointsDir string `json:"checkpoints_dir,omitempty"`
	LogDir         string `json:"log_dir,omitempty"`
	BlobDir        string `json:"blob_dir,omitempty"`
	StagingDir     string `json:"staging_dir,omitempty"`
}

// BlobRef is a broker-owned binary payload reference exposed to an adapter.
type BlobRef struct {
	ID          string `json:"id,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// ResolvedCredential is broker-owned secret material staged for one adapter
// process. Durable messaging records should keep only `credential_ref`; the
// adapter receives a local file/blob reference for the current invocation.
type ResolvedCredential struct {
	Ref         string               `json:"ref,omitempty"`
	AuthMethod  messaging.AuthMethod `json:"auth_method,omitempty"`
	ContentType string               `json:"content_type,omitempty"`
	Blob        BlobRef              `json:"blob"`
	Metadata    map[string]string    `json:"metadata,omitempty"`
}

// Attachment describes one file/image payload passed alongside a message or
// draft record.
type Attachment struct {
	Name        string            `json:"name,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	SHA256      string            `json:"sha256,omitempty"`
	Blob        BlobRef           `json:"blob"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// MessageRecord carries a normalized message and any staged attachments.
type MessageRecord struct {
	Message     messaging.Message     `json:"message"`
	Attachments []Attachment          `json:"attachments,omitempty"`
	Placements  []messaging.Placement `json:"placements,omitempty"`
}

// DraftRecord carries a normalized draft and any staged attachments.
type DraftRecord struct {
	Draft       messaging.Draft `json:"draft"`
	Attachments []Attachment    `json:"attachments,omitempty"`
}

// Checkpoint is adapter-owned durable progress for polling/webhook replay.
type Checkpoint struct {
	Cursor    string            `json:"cursor,omitempty"`
	Sequence  string            `json:"sequence,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// WebhookRequest is the broker-owned inbound HTTP envelope forwarded to an
// adapter for parsing and verification.
type WebhookRequest struct {
	RequestID    string                 `json:"request_id,omitempty"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Method       string                 `json:"method"`
	URL          string                 `json:"url"`
	Headers      map[string][]string    `json:"headers,omitempty"`
	Query        map[string][]string    `json:"query,omitempty"`
	Body         string                 `json:"body,omitempty"`
	BodyBlob     BlobRef                `json:"body_blob,omitempty"`
	RemoteAddr   string                 `json:"remote_addr,omitempty"`
	ReceivedAt   time.Time              `json:"received_at,omitempty"`
}

// HealthStatus is the adapter's reported health for one connection or process.
type HealthStatus struct {
	OK        bool                       `json:"ok"`
	Status    messaging.ConnectionStatus `json:"status,omitempty"`
	Message   string                     `json:"message,omitempty"`
	CheckedAt time.Time                  `json:"checked_at,omitempty"`
	Metadata  map[string]string          `json:"metadata,omitempty"`
}

// ProtocolErrorCode is one structured adapter error class.
type ProtocolErrorCode string

const (
	ProtocolErrorNotSupported ProtocolErrorCode = "not_supported"
	ProtocolErrorInvalidInput ProtocolErrorCode = "invalid_input"
	ProtocolErrorAuthRequired ProtocolErrorCode = "auth_required"
	ProtocolErrorTemporary    ProtocolErrorCode = "temporary_failure"
	ProtocolErrorConflict     ProtocolErrorCode = "conflict"
	ProtocolErrorInternal     ProtocolErrorCode = "internal"
)

// Error is a structured adapter protocol error.
type Error struct {
	Code      ProtocolErrorCode `json:"code"`
	Message   string            `json:"message"`
	Method    Method            `json:"method,omitempty"`
	Retryable bool              `json:"retryable,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Method != "" {
		return fmt.Sprintf("%s (%s): %s", e.Code, e.Method, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NotSupported returns a standard "not supported" protocol error.
func NotSupported(method Method, detail string) *Error {
	message := "operation is not supported by this adapter"
	if strings.TrimSpace(detail) != "" {
		message = detail
	}
	return &Error{
		Code:    ProtocolErrorNotSupported,
		Method:  method,
		Message: message,
	}
}

// DescribeParams asks an adapter to return its manifest and protocol version.
type DescribeParams struct {
	BrokerProtocol ProtocolInfo `json:"broker_protocol,omitempty"`
}

// DescribeResult is the adapter's manifest and protocol descriptor.
type DescribeResult struct {
	Protocol ProtocolInfo      `json:"protocol"`
	Adapter  messaging.Adapter `json:"adapter"`
}

// ValidateConfigParams asks the adapter to validate a proposed connection.
type ValidateConfigParams struct {
	Connection messaging.Connection `json:"connection"`
	Paths      RuntimePaths         `json:"paths,omitempty"`
	Credential *ResolvedCredential  `json:"credential,omitempty"`
}

// ValidateConfigResult reports connection config issues.
type ValidateConfigResult struct {
	Issues []ValidationIssue `json:"issues,omitempty"`
}

// ConnectParams starts one connection inside the adapter.
type ConnectParams struct {
	Connection messaging.Connection `json:"connection"`
	Paths      RuntimePaths         `json:"paths"`
	Credential *ResolvedCredential  `json:"credential,omitempty"`
}

// ConnectResult reports the adapter's initial view of the connection.
type ConnectResult struct {
	Status     messaging.ConnectionStatus `json:"status,omitempty"`
	Auth       messaging.AuthInfo         `json:"auth,omitempty"`
	Identities []messaging.Identity       `json:"identities,omitempty"`
	Metadata   map[string]string          `json:"metadata,omitempty"`
}

// RefreshParams refreshes one connection's auth/session state.
type RefreshParams struct {
	Connection messaging.Connection `json:"connection"`
	Paths      RuntimePaths         `json:"paths,omitempty"`
	Credential *ResolvedCredential  `json:"credential,omitempty"`
}

// RefreshResult is identical to ConnectResult.
type RefreshResult = ConnectResult

// ListIdentitiesParams asks an adapter to return local identities for one
// connection.
type ListIdentitiesParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
}

// ListIdentitiesResult returns discovered local identities.
type ListIdentitiesResult struct {
	Identities []messaging.Identity `json:"identities,omitempty"`
}

// PageRequest is a common page/cursor envelope.
type PageRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ListConversationsParams asks an adapter to enumerate conversations.
type ListConversationsParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	PageRequest
}

// ListConversationsResult is one conversation page.
type ListConversationsResult struct {
	Conversations []messaging.Conversation `json:"conversations,omitempty"`
	NextCursor    string                   `json:"next_cursor,omitempty"`
}

// ListMessagesParams asks an adapter to enumerate messages in one conversation.
type ListMessagesParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id"`
	PageRequest
}

// ListMessagesResult is one page of normalized messages.
type ListMessagesResult struct {
	Messages   []MessageRecord `json:"messages,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// GetMessageParams asks an adapter for one specific message.
type GetMessageParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MessageID      messaging.MessageID      `json:"message_id,omitempty"`
	RemoteID       string                   `json:"remote_id,omitempty"`
}

// GetMessageResult returns one normalized message.
type GetMessageResult struct {
	Message MessageRecord `json:"message"`
}

// ListContainersParams asks an adapter to enumerate provider-side containers
// such as mailboxes, folders, or labels.
type ListContainersParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	PageRequest
}

// ListContainersResult is one container page.
type ListContainersResult struct {
	Containers []messaging.Container `json:"containers,omitempty"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// SearchSource identifies whether a search is served from broker-indexed
// normalized state or through a live platform adapter lookup.
type SearchSource string

const (
	SearchSourceIndexed SearchSource = "indexed"
	SearchSourceRemote  SearchSource = "remote"
)

// ResolveIdentityParams asks an adapter to resolve one platform identity from
// an exact address, handle, remote id, or provider-specific query.
type ResolveIdentityParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Address      string                 `json:"address,omitempty"`
	RemoteID     string                 `json:"remote_id,omitempty"`
	Query        string                 `json:"query,omitempty"`
}

// IdentitySearchHit is one local or remote person/account lookup hit.
type IdentitySearchHit struct {
	Participant    messaging.Participant    `json:"participant"`
	Identity       *messaging.Identity      `json:"identity,omitempty"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MatchedFields  []string                 `json:"matched_fields,omitempty"`
	Source         SearchSource             `json:"source,omitempty"`
	Metadata       map[string]string        `json:"metadata,omitempty"`
}

// ResolveIdentityResult is the resolved identity hit for one exact lookup.
type ResolveIdentityResult struct {
	Hit    IdentitySearchHit `json:"hit,omitempty"`
	Found  bool              `json:"found"`
	Source SearchSource      `json:"source,omitempty"`
}

// SearchIdentitiesParams asks for contact, username, email address, phone
// number, handle, or local-identity lookup results.
type SearchIdentitiesParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Query        string                 `json:"query"`
	Source       SearchSource           `json:"source,omitempty"`
	PageRequest
}

// SearchIdentitiesResult returns identity lookup hits.
type SearchIdentitiesResult struct {
	Hits       []IdentitySearchHit `json:"hits,omitempty"`
	Count      int                 `json:"count,omitempty"`
	Source     SearchSource        `json:"source,omitempty"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// ConversationSearchHit is one destination/thread lookup hit.
type ConversationSearchHit struct {
	Conversation  messaging.Conversation `json:"conversation"`
	MatchedFields []string               `json:"matched_fields,omitempty"`
	Source        SearchSource           `json:"source,omitempty"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
}

// SearchConversationsParams asks for channel, room, thread, mailbox/folder, or
// cached conversation lookup results.
type SearchConversationsParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Query        string                 `json:"query"`
	Source       SearchSource           `json:"source,omitempty"`
	PageRequest
}

// SearchConversationsResult returns destination/thread lookup hits.
type SearchConversationsResult struct {
	Hits       []ConversationSearchHit `json:"hits,omitempty"`
	Count      int                     `json:"count,omitempty"`
	Source     SearchSource            `json:"source,omitempty"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

// MessageSearchHit is one content lookup hit.
type MessageSearchHit struct {
	Message       MessageRecord           `json:"message"`
	Conversation  *messaging.Conversation `json:"conversation,omitempty"`
	MatchedFields []string                `json:"matched_fields,omitempty"`
	Source        SearchSource            `json:"source,omitempty"`
	Metadata      map[string]string       `json:"metadata,omitempty"`
}

// SearchMessagesParams asks for message/body/header search results.
type SearchMessagesParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	ContainerID    messaging.ContainerID    `json:"container_id,omitempty"`
	Query          string                   `json:"query"`
	Source         SearchSource             `json:"source,omitempty"`
	PageRequest
}

// SearchMessagesResult returns content lookup hits.
type SearchMessagesResult struct {
	Hits       []MessageSearchHit `json:"hits,omitempty"`
	Count      int                `json:"count,omitempty"`
	Source     SearchSource       `json:"source,omitempty"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// PlacementChange reports updated provider locators after a management
// operation such as move, archive, label mutation, or read-state update.
type PlacementChange struct {
	MessageID messaging.MessageID     `json:"message_id,omitempty"`
	Message   *messaging.Message      `json:"message,omitempty"`
	Placement *messaging.Placement    `json:"placement,omitempty"`
	Removed   []messaging.ContainerID `json:"removed,omitempty"`
	Added     []messaging.ContainerID `json:"added,omitempty"`
}

// MoveMessagesParams asks an adapter to move messages to another container.
type MoveMessagesParams struct {
	ConnectionID           messaging.ConnectionID `json:"connection_id"`
	MessageIDs             []messaging.MessageID  `json:"message_ids"`
	DestinationContainerID messaging.ContainerID  `json:"destination_container_id"`
}

// MoveConversationParams asks an adapter to move every supported message in a
// conversation to another container.
type MoveConversationParams struct {
	ConnectionID           messaging.ConnectionID   `json:"connection_id"`
	ConversationID         messaging.ConversationID `json:"conversation_id"`
	DestinationContainerID messaging.ContainerID    `json:"destination_container_id"`
}

// ArchiveMessagesParams asks an adapter to archive messages using the
// platform's configured archive behavior.
type ArchiveMessagesParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	MessageIDs   []messaging.MessageID  `json:"message_ids"`
	ContainerID  messaging.ContainerID  `json:"container_id,omitempty"`
}

// ArchiveConversationParams asks an adapter to archive every supported message
// in a conversation.
type ArchiveConversationParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id"`
	ContainerID    messaging.ContainerID    `json:"container_id,omitempty"`
}

// ApplyLabelsParams asks an adapter to add/remove label-like containers.
type ApplyLabelsParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MessageIDs     []messaging.MessageID    `json:"message_ids,omitempty"`
	Add            []messaging.ContainerID  `json:"add,omitempty"`
	Remove         []messaging.ContainerID  `json:"remove,omitempty"`
}

// MarkReadParams asks an adapter to set read state for messages or a whole
// conversation.
type MarkReadParams struct {
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MessageIDs     []messaging.MessageID    `json:"message_ids,omitempty"`
	Read           bool                     `json:"read"`
}

// ManageMessagesResult reports provider-side state changes after message
// management operations.
type ManageMessagesResult struct {
	Messages   []MessageRecord       `json:"messages,omitempty"`
	Placements []messaging.Placement `json:"placements,omitempty"`
	Changes    []PlacementChange     `json:"changes,omitempty"`
	Events     []messaging.Event     `json:"events,omitempty"`
}

// CreateDraftParams asks an adapter to validate and optionally create a native
// draft for one broker draft.
type CreateDraftParams struct {
	Draft DraftRecord `json:"draft"`
}

// CreateDraftResult returns the normalized draft state after adapter handling.
type CreateDraftResult struct {
	Draft DraftRecord `json:"draft"`
}

// UpdateDraftParams asks an adapter to update an existing broker/native draft.
type UpdateDraftParams struct {
	Draft DraftRecord `json:"draft"`
}

// UpdateDraftResult returns the normalized draft state after update.
type UpdateDraftResult struct {
	Draft DraftRecord `json:"draft"`
}

// DeleteDraftParams asks an adapter to delete a native draft when supported.
type DeleteDraftParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	DraftID      messaging.DraftID      `json:"draft_id"`
}

// DeleteDraftResult reports whether a delete became effective.
type DeleteDraftResult struct {
	Deleted bool `json:"deleted"`
}

// SendOptions are common outbound transport controls.
type SendOptions struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// SendMessageParams asks the adapter to send an outbound message or approved
// draft.
type SendMessageParams struct {
	Draft DraftRecord `json:"draft"`
	SendOptions
}

// ReplyMessageParams asks the adapter to reply within an existing conversation.
type ReplyMessageParams struct {
	Draft            DraftRecord         `json:"draft"`
	ReplyToMessageID messaging.MessageID `json:"reply_to_message_id,omitempty"`
	ReplyToRemoteID  string              `json:"reply_to_remote_id,omitempty"`
	SendOptions
}

// SendResult reports the adapter's normalized outbound result.
type SendResult struct {
	Message MessageRecord           `json:"message"`
	Status  messaging.MessageStatus `json:"status,omitempty"`
}

// HandleWebhookParams asks the adapter to parse and normalize one webhook hit.
type HandleWebhookParams struct {
	Request WebhookRequest `json:"request"`
}

// HandleWebhookResult returns normalized events plus an optional HTTP response.
type HandleWebhookResult struct {
	Events     []messaging.Event `json:"events,omitempty"`
	Checkpoint *Checkpoint       `json:"checkpoint,omitempty"`
	StatusCode int               `json:"status_code,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
}

// PollParams asks an adapter to poll for new events since the last checkpoint.
type PollParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Checkpoint   *Checkpoint            `json:"checkpoint,omitempty"`
	Limit        int                    `json:"limit,omitempty"`
}

// PollResult returns normalized events plus an updated checkpoint.
type PollResult struct {
	Events          []messaging.Event `json:"events,omitempty"`
	Checkpoint      *Checkpoint       `json:"checkpoint,omitempty"`
	PollAfterMillis int64             `json:"poll_after_millis,omitempty"`
}

// HealthParams asks an adapter to report process/connection health.
type HealthParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id,omitempty"`
}

// HealthResult returns the adapter's current health view.
type HealthResult struct {
	Health HealthStatus `json:"health"`
}
