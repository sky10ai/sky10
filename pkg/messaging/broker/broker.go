package broker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

const defaultInlineWebhookBodyLimit = 32 << 10

// Config configures one broker instance.
type Config struct {
	Store              *messagingstore.Store
	Manager            *messagingruntime.Manager
	RootDir            string
	CredentialResolver CredentialResolver
	ApprovalMailbox    ApprovalMailbox
	ApprovalFrom       agentmailbox.Principal
	ApprovalTo         *agentmailbox.Principal
	Now                func() time.Time
	NewID              func() string
}

// RegisterConnectionParams describes one connection plus its adapter process.
type RegisterConnectionParams struct {
	Connection messaging.Connection
	Process    messagingruntime.ProcessSpec
}

// ConnectResult reports the broker's updated view after connect.
type ConnectResult struct {
	Connection messaging.Connection  `json:"connection"`
	Adapter    messaging.Adapter     `json:"adapter"`
	Paths      protocol.RuntimePaths `json:"paths"`
}

// DeleteConnectionResult reports a completed broker-side connection deletion.
type DeleteConnectionResult struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Deleted      bool                   `json:"deleted"`
}

// PollResult reports the broker's persisted poll outcome.
type PollResult struct {
	Connection    messaging.Connection     `json:"connection"`
	Events        []messaging.Event        `json:"events,omitempty"`
	Checkpoint    *protocol.Checkpoint     `json:"checkpoint,omitempty"`
	Messages      []messaging.Message      `json:"messages,omitempty"`
	Conversations []messaging.Conversation `json:"conversations,omitempty"`
}

// WebhookRequest is the broker-owned webhook input before normalization into
// the adapter protocol envelope.
type WebhookRequest struct {
	RequestID  string              `json:"request_id,omitempty"`
	Method     string              `json:"method"`
	URL        string              `json:"url"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Query      map[string][]string `json:"query,omitempty"`
	Body       []byte              `json:"body,omitempty"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	ReceivedAt time.Time           `json:"received_at,omitempty"`
}

// WebhookResult reports the persisted outcome of one webhook adapter call plus
// the HTTP response details the adapter wants the broker to return.
type WebhookResult struct {
	Connection    messaging.Connection     `json:"connection"`
	Events        []messaging.Event        `json:"events,omitempty"`
	Checkpoint    *protocol.Checkpoint     `json:"checkpoint,omitempty"`
	Messages      []messaging.Message      `json:"messages,omitempty"`
	Conversations []messaging.Conversation `json:"conversations,omitempty"`
	StatusCode    int                      `json:"status_code,omitempty"`
	Headers       map[string]string        `json:"headers,omitempty"`
	Body          string                   `json:"body,omitempty"`
}

// Broker orchestrates messaging connections through supervised adapters.
type Broker struct {
	store              *messagingstore.Store
	manager            *messagingruntime.Manager
	rootDir            string
	credentialResolver CredentialResolver
	approvalMailbox    ApprovalMailbox
	approvalFrom       agentmailbox.Principal
	approvalTo         *agentmailbox.Principal
	now                func() time.Time
	newID              func() string

	mu sync.RWMutex
}

// New creates a broker over an existing messaging store and runtime manager.
func New(ctx context.Context, cfg Config) (*Broker, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("messaging broker store is required")
	}
	if strings.TrimSpace(cfg.RootDir) == "" {
		return nil, fmt.Errorf("messaging broker root_dir is required")
	}
	rootDir, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("messaging broker root_dir: %w", err)
	}
	manager := cfg.Manager
	if manager == nil {
		manager = messagingruntime.NewManager(ctx)
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newID := cfg.NewID
	if newID == nil {
		newID = func() string { return uuid.NewString() }
	}
	approvalFrom := cfg.ApprovalFrom
	if cfg.ApprovalMailbox != nil && strings.TrimSpace(approvalFrom.ID) == "" {
		approvalFrom = agentmailbox.Principal{
			ID:    "system:messaging",
			Kind:  agentmailbox.PrincipalKindLocalAgent,
			Scope: agentmailbox.ScopePrivateNetwork,
		}
	}
	var approvalTo *agentmailbox.Principal
	if cfg.ApprovalTo != nil {
		copy := *cfg.ApprovalTo
		approvalTo = &copy
	}
	return &Broker{
		store:              cfg.Store,
		manager:            manager,
		rootDir:            rootDir,
		credentialResolver: cfg.CredentialResolver,
		approvalMailbox:    cfg.ApprovalMailbox,
		approvalFrom:       approvalFrom,
		approvalTo:         approvalTo,
		now:                now,
		newID:              newID,
	}, nil
}

// Close stops all managed adapters owned by this broker.
func (b *Broker) Close() error {
	if b == nil || b.manager == nil {
		return nil
	}
	return b.manager.Close()
}

// RegisterConnection persists one connection and starts supervising its adapter
// process. The adapter process is described during startup by the runtime
// manager, but broker-level connect happens separately.
func (b *Broker) RegisterConnection(ctx context.Context, params RegisterConnectionParams) error {
	_, err := b.createManagedConnection(ctx, params, false)
	return err
}

// CreateConnection persists one new connection and starts supervising its
// adapter process without connecting it. Duplicate connection IDs are rejected.
func (b *Broker) CreateConnection(ctx context.Context, params RegisterConnectionParams) (messaging.Connection, error) {
	return b.createManagedConnection(ctx, params, false)
}

func (b *Broker) createManagedConnection(ctx context.Context, params RegisterConnectionParams, replace bool) (messaging.Connection, error) {
	if err := params.Connection.Validate(); err != nil {
		return messaging.Connection{}, err
	}
	if _, exists := b.store.GetConnection(params.Connection.ID); exists && !replace {
		return messaging.Connection{}, fmt.Errorf("messaging connection %s already exists", params.Connection.ID)
	}
	if err := params.Process.Validate(); err != nil {
		return messaging.Connection{}, err
	}

	connection := cloneConnection(params.Connection)
	if strings.TrimSpace(string(connection.Status)) == "" {
		connection.Status = messaging.ConnectionStatusUnknown
	}
	if connection.UpdatedAt.IsZero() {
		connection.UpdatedAt = b.now()
	}

	if _, ok := b.manager.Get(string(connection.ID)); ok {
		if !replace {
			return messaging.Connection{}, fmt.Errorf("managed adapter %q already exists", connection.ID)
		}
		if err := b.manager.Remove(string(connection.ID)); err != nil {
			return messaging.Connection{}, err
		}
	}

	if connection.Status != messaging.ConnectionStatusDisabled {
		if _, err := b.manager.Add(messagingruntime.ManagedAdapterSpec{
			Key:     string(connection.ID),
			Process: params.Process,
		}); err != nil {
			return messaging.Connection{}, err
		}
	}
	if err := b.store.PutConnection(ctx, connection); err != nil {
		_ = b.removeManagedAdapterIfPresent(string(connection.ID))
		return messaging.Connection{}, err
	}
	return connection, nil
}

// UpsertConnection persists one connection and ensures a supervised adapter
// process exists for it. Existing managed adapters for the same connection are
// replaced so updated process specs take effect immediately.
func (b *Broker) UpsertConnection(ctx context.Context, params RegisterConnectionParams) error {
	_, err := b.createManagedConnection(ctx, params, true)
	return err
}

// RefreshConnection refreshes adapter-owned auth/session state and persists
// any updated connection metadata and identities.
func (b *Broker) RefreshConnection(ctx context.Context, connectionID messaging.ConnectionID) (ConnectResult, error) {
	connection, adapterClient, paths, describe, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return ConnectResult{}, err
	}
	credential, err := b.resolveConnectionCredential(ctx, connection, paths)
	if err != nil {
		return ConnectResult{}, err
	}

	result, err := adapterClient.Refresh(ctx, protocol.RefreshParams{
		Connection: connection,
		Paths:      paths,
		Credential: credential,
	})
	if err != nil {
		return ConnectResult{}, err
	}
	return b.persistConnectionResult(ctx, connection, paths, describe, result, result.Identities != nil)
}

// DisableConnection stops broker supervision for one connection and persists a
// disabled status. Disabled connections are not restored or polled.
func (b *Broker) DisableConnection(ctx context.Context, connectionID messaging.ConnectionID) (messaging.Connection, error) {
	connection, ok := b.store.GetConnection(connectionID)
	if !ok {
		return messaging.Connection{}, fmt.Errorf("messaging connection %s not found", connectionID)
	}
	paths := b.runtimePathsForConnection(connection)
	if err := b.removeManagedAdapterIfPresent(string(connectionID)); err != nil {
		return messaging.Connection{}, err
	}
	now := b.now()
	connection.Status = messaging.ConnectionStatusDisabled
	connection.UpdatedAt = now
	if err := b.store.PutConnection(ctx, connection); err != nil {
		return messaging.Connection{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeConnectionUpdated,
		ConnectionID: connection.ID,
		Timestamp:    now,
		Metadata: map[string]string{
			"status": string(connection.Status),
		},
	}); err != nil {
		return messaging.Connection{}, err
	}
	if err := removeRuntimePaths(paths.SecretsDir); err != nil {
		return messaging.Connection{}, err
	}
	return connection, nil
}

// DeleteConnection stops broker supervision and removes the connection's live
// broker-owned state. Operator workflow summaries are retained by the store as
// audit history.
func (b *Broker) DeleteConnection(ctx context.Context, connectionID messaging.ConnectionID) (DeleteConnectionResult, error) {
	connection, ok := b.store.GetConnection(connectionID)
	if !ok {
		return DeleteConnectionResult{}, fmt.Errorf("messaging connection %s not found", connectionID)
	}
	paths := b.runtimePathsForConnection(connection)
	if err := b.removeManagedAdapterIfPresent(string(connectionID)); err != nil {
		return DeleteConnectionResult{}, err
	}
	if err := b.store.DeleteConnection(ctx, connectionID); err != nil {
		return DeleteConnectionResult{}, err
	}
	if err := removeRuntimePaths(paths.RootDir, paths.BlobDir, paths.StagingDir); err != nil {
		return DeleteConnectionResult{}, err
	}
	return DeleteConnectionResult{ConnectionID: connectionID, Deleted: true}, nil
}

func (b *Broker) removeManagedAdapterIfPresent(key string) error {
	if _, ok := b.manager.Get(key); !ok {
		return nil
	}
	return b.manager.Remove(key)
}

// ConnectConnection waits for the adapter, computes runtime paths, and persists
// the adapter's initial connection + identity state.
func (b *Broker) ConnectConnection(ctx context.Context, connectionID messaging.ConnectionID) (ConnectResult, error) {
	connection, adapterClient, paths, describe, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return ConnectResult{}, err
	}
	credential, err := b.resolveConnectionCredential(ctx, connection, paths)
	if err != nil {
		return ConnectResult{}, err
	}

	result, err := adapterClient.Connect(ctx, protocol.ConnectParams{
		Connection: connection,
		Paths:      paths,
		Credential: credential,
	})
	if err != nil {
		return ConnectResult{}, err
	}

	return b.persistConnectionResult(ctx, connection, paths, describe, result, true)
}

// ValidateConnectionConfig asks a supervised adapter to validate one persisted
// connection using the broker-owned runtime paths and staged credential.
func (b *Broker) ValidateConnectionConfig(ctx context.Context, connectionID messaging.ConnectionID) (protocol.ValidateConfigResult, error) {
	connection, adapterClient, paths, _, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return protocol.ValidateConfigResult{}, err
	}
	credential, err := b.resolveConnectionCredential(ctx, connection, paths)
	if err != nil {
		return protocol.ValidateConfigResult{}, err
	}
	return adapterClient.ValidateConfig(ctx, protocol.ValidateConfigParams{
		Connection: connection,
		Paths:      paths,
		Credential: credential,
	})
}

func (b *Broker) persistConnectionResult(ctx context.Context, connection messaging.Connection, paths protocol.RuntimePaths, describe protocol.DescribeResult, result protocol.ConnectResult, replaceIdentities bool) (ConnectResult, error) {
	now := b.now()
	if result.Auth.Configured() {
		connection.Auth = result.Auth
	}
	if result.Metadata != nil {
		connection.Metadata = cloneStringMap(result.Metadata)
	}
	if strings.TrimSpace(string(result.Status)) != "" {
		connection.Status = result.Status
	}
	if connection.Status == messaging.ConnectionStatusConnected && connection.ConnectedAt.IsZero() {
		connection.ConnectedAt = now
	}
	connection.UpdatedAt = now

	if err := b.store.PutConnection(ctx, connection); err != nil {
		return ConnectResult{}, err
	}
	if replaceIdentities {
		if err := b.store.ReplaceConnectionIdentities(ctx, connection.ID, result.Identities); err != nil {
			return ConnectResult{}, err
		}
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeConnectionUpdated,
		ConnectionID: connection.ID,
		Timestamp:    now,
		Metadata: map[string]string{
			"status": string(connection.Status),
		},
	}); err != nil {
		return ConnectResult{}, err
	}
	if replaceIdentities {
		for _, identity := range result.Identities {
			if err := b.store.AppendEvent(ctx, messaging.Event{
				ID:           messaging.EventID(b.newID()),
				Type:         messaging.EventTypeIdentityDiscovered,
				ConnectionID: connection.ID,
				Timestamp:    now,
				Metadata: map[string]string{
					"identity_id": string(identity.ID),
				},
			}); err != nil {
				return ConnectResult{}, err
			}
		}
	}

	return ConnectResult{
		Connection: connection,
		Adapter:    describe.Adapter,
		Paths:      paths,
	}, nil
}

// PollConnection polls one adapter, persists returned events and checkpoints,
// and hydrates referenced conversations/messages when the adapter supports it.
func (b *Broker) PollConnection(ctx context.Context, connectionID messaging.ConnectionID, limit int) (PollResult, error) {
	connection, adapterClient, _, _, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return PollResult{}, err
	}

	var checkpoint *protocol.Checkpoint
	if current, ok := b.store.GetCheckpoint(connectionID); ok {
		copy := current
		checkpoint = &copy
	}
	result, err := adapterClient.Poll(ctx, protocol.PollParams{
		ConnectionID: connectionID,
		Checkpoint:   checkpoint,
		Limit:        limit,
	})
	if err != nil {
		return PollResult{}, err
	}

	persisted, conversations, messages, storedCheckpoint, err := b.persistInboundResult(ctx, adapterClient, connection, result.Events, result.Checkpoint)
	if err != nil {
		return PollResult{}, err
	}

	return PollResult{
		Connection:    connection,
		Events:        persisted,
		Checkpoint:    storedCheckpoint,
		Messages:      messages,
		Conversations: conversations,
	}, nil
}

// HandleWebhookConnection forwards one broker-owned webhook envelope to the
// adapter, persists resulting events/checkpoints, and returns the adapter's
// desired HTTP response details.
func (b *Broker) HandleWebhookConnection(ctx context.Context, connectionID messaging.ConnectionID, request WebhookRequest) (WebhookResult, error) {
	connection, adapterClient, paths, _, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return WebhookResult{}, err
	}
	normalized, err := b.normalizeWebhookRequest(paths, connectionID, request)
	if err != nil {
		return WebhookResult{}, err
	}
	result, err := adapterClient.HandleWebhook(ctx, protocol.HandleWebhookParams{
		Request: normalized,
	})
	if err != nil {
		return WebhookResult{}, err
	}
	persisted, conversations, messages, storedCheckpoint, err := b.persistInboundResult(ctx, adapterClient, connection, result.Events, result.Checkpoint)
	if err != nil {
		return WebhookResult{}, err
	}

	statusCode := result.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}
	return WebhookResult{
		Connection:    connection,
		Events:        persisted,
		Checkpoint:    storedCheckpoint,
		Messages:      messages,
		Conversations: conversations,
		StatusCode:    statusCode,
		Headers:       cloneStringMap(result.Headers),
		Body:          result.Body,
	}, nil
}

func (b *Broker) prepareAdapterCall(ctx context.Context, connectionID messaging.ConnectionID) (messaging.Connection, *messagingruntime.AdapterClient, protocol.RuntimePaths, protocol.DescribeResult, error) {
	connection, ok := b.store.GetConnection(connectionID)
	if !ok {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, fmt.Errorf("messaging connection %s not found", connectionID)
	}
	if connection.Status == messaging.ConnectionStatusDisabled {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, fmt.Errorf("messaging connection %s is disabled", connectionID)
	}
	paths := b.runtimePathsForConnection(connection)
	if err := ensureRuntimePaths(paths); err != nil {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, err
	}

	managed, ok := b.manager.Get(string(connectionID))
	if !ok {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, fmt.Errorf("managed adapter %s not found", connectionID)
	}
	if err := managed.WaitReady(ctx); err != nil {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, err
	}
	adapterClient, err := managed.Current()
	if err != nil {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, err
	}
	describe, err := adapterClient.Describe(ctx)
	if err != nil {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, err
	}
	return connection, adapterClient, paths, describe, nil
}

func (b *Broker) persistInboundResult(ctx context.Context, adapterClient *messagingruntime.AdapterClient, connection messaging.Connection, rawEvents []messaging.Event, checkpoint *protocol.Checkpoint) ([]messaging.Event, []messaging.Conversation, []messaging.Message, *protocol.Checkpoint, error) {
	persisted := make([]messaging.Event, 0, len(rawEvents))
	conversations := make([]messaging.Conversation, 0)
	messages := make([]messaging.Message, 0)
	for _, rawEvent := range rawEvents {
		event := cloneEvent(rawEvent)
		if strings.TrimSpace(string(event.ID)) == "" {
			event.ID = stableEventID(connection.ID, event)
		}
		if strings.TrimSpace(string(event.ConnectionID)) == "" {
			event.ConnectionID = connection.ID
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = b.now()
		}
		if err := b.store.AppendEvent(ctx, event); err != nil {
			return nil, nil, nil, nil, err
		}
		persisted = append(persisted, event)

		conversation, message, err := b.hydrateEvent(ctx, adapterClient, connection, event)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if conversation.ID != "" {
			conversations = appendIfConversationMissing(conversations, conversation)
		}
		if message.ID != "" {
			messages = appendIfMessageMissing(messages, message)
		}
	}

	var storedCheckpoint *protocol.Checkpoint
	if checkpoint != nil {
		checkpointCopy := cloneCheckpoint(*checkpoint)
		if checkpointCopy.UpdatedAt.IsZero() {
			checkpointCopy.UpdatedAt = b.now()
		}
		if err := b.store.PutCheckpoint(ctx, connection.ID, checkpointCopy); err != nil {
			return nil, nil, nil, nil, err
		}
		storedCheckpoint = &checkpointCopy
	}
	return persisted, conversations, messages, storedCheckpoint, nil
}

func (b *Broker) hydrateEvent(ctx context.Context, adapterClient *messagingruntime.AdapterClient, connection messaging.Connection, event messaging.Event) (messaging.Conversation, messaging.Message, error) {
	var conversation messaging.Conversation
	var message messaging.Message

	if event.ConversationID != "" {
		var err error
		conversation, err = b.ensureConversation(ctx, adapterClient, connection, event.ConversationID)
		if err != nil {
			return messaging.Conversation{}, messaging.Message{}, err
		}
	}
	if event.MessageID != "" {
		var err error
		message, err = b.syncMessage(ctx, adapterClient, connection, event.ConversationID, event.MessageID)
		if err != nil {
			return messaging.Conversation{}, messaging.Message{}, err
		}
	}
	return conversation, message, nil
}

func (b *Broker) ensureConversation(ctx context.Context, adapterClient *messagingruntime.AdapterClient, connection messaging.Connection, conversationID messaging.ConversationID) (messaging.Conversation, error) {
	if existing, ok := b.store.GetConversation(conversationID); ok {
		return existing, nil
	}
	page := protocol.ListConversationsParams{
		ConnectionID: connection.ID,
		PageRequest: protocol.PageRequest{
			Limit: 100,
		},
	}
	for {
		result, err := adapterClient.ListConversations(ctx, page)
		if err != nil {
			var protocolErr *protocol.Error
			if errors.As(err, &protocolErr) && protocolErr.Code == protocol.ProtocolErrorNotSupported {
				return messaging.Conversation{}, nil
			}
			return messaging.Conversation{}, err
		}
		for _, conversation := range result.Conversations {
			if err := b.store.PutConversation(ctx, conversation); err != nil {
				return messaging.Conversation{}, err
			}
			if conversation.ID == conversationID {
				return conversation, nil
			}
		}
		if strings.TrimSpace(result.NextCursor) == "" {
			break
		}
		page.Cursor = result.NextCursor
	}
	return messaging.Conversation{}, nil
}

func (b *Broker) syncMessage(ctx context.Context, adapterClient *messagingruntime.AdapterClient, connection messaging.Connection, conversationID messaging.ConversationID, messageID messaging.MessageID) (messaging.Message, error) {
	result, err := adapterClient.GetMessage(ctx, protocol.GetMessageParams{
		ConnectionID:   connection.ID,
		ConversationID: conversationID,
		MessageID:      messageID,
	})
	if err != nil {
		var protocolErr *protocol.Error
		if errors.As(err, &protocolErr) && protocolErr.Code == protocol.ProtocolErrorNotSupported {
			return messaging.Message{}, nil
		}
		return messaging.Message{}, err
	}
	if err := b.store.PutMessage(ctx, result.Message.Message); err != nil {
		return messaging.Message{}, err
	}
	for _, placement := range result.Message.Placements {
		if placement.ConnectionID == "" {
			placement.ConnectionID = connection.ID
		}
		if placement.MessageID == "" {
			placement.MessageID = result.Message.Message.ID
		}
		if err := b.store.PutPlacement(ctx, placement); err != nil {
			return messaging.Message{}, err
		}
	}
	return result.Message.Message, nil
}

func (b *Broker) normalizeWebhookRequest(paths protocol.RuntimePaths, connectionID messaging.ConnectionID, request WebhookRequest) (protocol.WebhookRequest, error) {
	receivedAt := request.ReceivedAt.UTC()
	if request.ReceivedAt.IsZero() {
		receivedAt = b.now()
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = b.newID()
	}
	normalized := protocol.WebhookRequest{
		RequestID:    requestID,
		ConnectionID: connectionID,
		Method:       request.Method,
		URL:          request.URL,
		Headers:      cloneHeaderMap(request.Headers),
		Query:        cloneHeaderMap(request.Query),
		RemoteAddr:   request.RemoteAddr,
		ReceivedAt:   receivedAt,
	}
	if len(request.Body) == 0 {
		return normalized, nil
	}
	if utf8.Valid(request.Body) && len(request.Body) <= defaultInlineWebhookBodyLimit {
		normalized.Body = string(request.Body)
		return normalized, nil
	}
	blob, err := stageWebhookBody(paths, requestID, request.Body)
	if err != nil {
		return protocol.WebhookRequest{}, err
	}
	normalized.BodyBlob = blob
	return normalized, nil
}

func (b *Broker) runtimePathsForConnection(connection messaging.Connection) protocol.RuntimePaths {
	adapterSegment := safePathSegment(string(connection.AdapterID))
	connectionSegment := encodePathSegment(string(connection.ID))
	connectionRoot := filepath.Join(b.rootDir, "adapters", adapterSegment, connectionSegment)
	return protocol.RuntimePaths{
		RootDir:        connectionRoot,
		StateDir:       filepath.Join(connectionRoot, "state"),
		CacheDir:       filepath.Join(connectionRoot, "cache"),
		RuntimeDir:     filepath.Join(connectionRoot, "runtime"),
		SecretsDir:     filepath.Join(connectionRoot, "runtime", "secrets"),
		CheckpointsDir: filepath.Join(connectionRoot, "checkpoints"),
		LogDir:         filepath.Join(connectionRoot, "logs"),
		BlobDir:        filepath.Join(b.rootDir, "blobs", connectionSegment),
		StagingDir:     filepath.Join(b.rootDir, "staging", connectionSegment),
	}
}

func ensureRuntimePaths(paths protocol.RuntimePaths) error {
	for _, dir := range []string{
		paths.RootDir,
		paths.StateDir,
		paths.CacheDir,
		paths.RuntimeDir,
		paths.CheckpointsDir,
		paths.LogDir,
		paths.BlobDir,
		paths.StagingDir,
	} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create runtime path %s: %w", dir, err)
		}
	}
	if strings.TrimSpace(paths.SecretsDir) != "" {
		if err := os.MkdirAll(paths.SecretsDir, 0o700); err != nil {
			return fmt.Errorf("create runtime path %s: %w", paths.SecretsDir, err)
		}
	}
	return nil
}

func removeRuntimePaths(paths ...string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove runtime path %s: %w", path, err)
		}
	}
	return nil
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "", value == ".", value == "..", strings.ContainsRune(value, os.PathSeparator):
		return encodePathSegment(value)
	}
	if os.PathSeparator != '/' && strings.ContainsRune(value, '/') {
		return encodePathSegment(value)
	}
	if os.PathSeparator != '\\' && strings.ContainsRune(value, '\\') {
		return encodePathSegment(value)
	}
	return value
}

func encodePathSegment(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func stageWebhookBody(paths protocol.RuntimePaths, requestID string, body []byte) (protocol.BlobRef, error) {
	webhookDir := filepath.Join(paths.StagingDir, "webhooks")
	if err := os.MkdirAll(webhookDir, 0o755); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("create webhook staging dir: %w", err)
	}
	fileName := encodePathSegment(requestID) + ".bin"
	localPath := filepath.Join(webhookDir, fileName)
	if err := os.WriteFile(localPath, body, 0o600); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("write staged webhook body: %w", err)
	}
	digest := sha256.Sum256(body)
	return protocol.BlobRef{
		ID:        "webhook:" + requestID,
		LocalPath: localPath,
		SizeBytes: int64(len(body)),
		SHA256:    hex.EncodeToString(digest[:]),
	}, nil
}

func appendIfConversationMissing(items []messaging.Conversation, value messaging.Conversation) []messaging.Conversation {
	if value.ID == "" {
		return items
	}
	for _, existing := range items {
		if existing.ID == value.ID {
			return items
		}
	}
	return append(items, value)
}

func appendIfMessageMissing(items []messaging.Message, value messaging.Message) []messaging.Message {
	if value.ID == "" {
		return items
	}
	for _, existing := range items {
		if existing.ID == value.ID {
			return items
		}
	}
	return append(items, value)
}

func cloneConnection(connection messaging.Connection) messaging.Connection {
	connection.Metadata = cloneStringMap(connection.Metadata)
	connection.Auth = cloneAuthInfo(connection.Auth)
	return connection
}

func cloneAuthInfo(auth messaging.AuthInfo) messaging.AuthInfo {
	auth.Scopes = slicesClone(auth.Scopes)
	if auth.ExpiresAt != nil {
		copy := auth.ExpiresAt.UTC()
		auth.ExpiresAt = &copy
	}
	return auth
}

func cloneEvent(event messaging.Event) messaging.Event {
	event.Metadata = cloneStringMap(event.Metadata)
	return event
}

func cloneCheckpoint(checkpoint protocol.Checkpoint) protocol.Checkpoint {
	checkpoint.Metadata = cloneStringMap(checkpoint.Metadata)
	return checkpoint
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func slicesClone[T any](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	out := make([]T, len(items))
	copy(out, items)
	return out
}

func cloneHeaderMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string][]string, len(values))
	for key, items := range values {
		cloned[key] = slicesClone(items)
	}
	return cloned
}

func stableEventID(connectionID messaging.ConnectionID, event messaging.Event) messaging.EventID {
	hash := sha256.New()
	writeEventHashPart(hash, string(connectionID))
	writeEventHashPart(hash, string(event.Type))
	writeEventHashPart(hash, string(event.ConnectionID))
	writeEventHashPart(hash, string(event.ConversationID))
	writeEventHashPart(hash, string(event.MessageID))
	writeEventHashPart(hash, string(event.DraftID))
	writeEventHashPart(hash, string(event.ExposureID))
	if !event.Timestamp.IsZero() {
		writeEventHashPart(hash, event.Timestamp.UTC().Format(time.RFC3339Nano))
	}
	if len(event.Metadata) > 0 {
		keys := make([]string, 0, len(event.Metadata))
		for key := range event.Metadata {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			writeEventHashPart(hash, key)
			writeEventHashPart(hash, event.Metadata[key])
		}
	}
	return messaging.EventID("evt/" + hex.EncodeToString(hash.Sum(nil))[:24])
}

func writeEventHashPart(hash hash.Hash, value string) {
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte{0})
}
