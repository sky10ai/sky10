package broker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

// Config configures one broker instance.
type Config struct {
	Store   *messagingstore.Store
	Manager *messagingruntime.Manager
	RootDir string
	Now     func() time.Time
	NewID   func() string
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

// PollResult reports the broker's persisted poll outcome.
type PollResult struct {
	Connection    messaging.Connection     `json:"connection"`
	Events        []messaging.Event        `json:"events,omitempty"`
	Checkpoint    *protocol.Checkpoint     `json:"checkpoint,omitempty"`
	Messages      []messaging.Message      `json:"messages,omitempty"`
	Conversations []messaging.Conversation `json:"conversations,omitempty"`
}

// Broker orchestrates messaging connections through supervised adapters.
type Broker struct {
	store   *messagingstore.Store
	manager *messagingruntime.Manager
	rootDir string
	now     func() time.Time
	newID   func() string

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
	return &Broker{
		store:   cfg.Store,
		manager: manager,
		rootDir: rootDir,
		now:     now,
		newID:   newID,
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
	if err := params.Connection.Validate(); err != nil {
		return err
	}
	if err := params.Process.Validate(); err != nil {
		return err
	}

	connection := cloneConnection(params.Connection)
	if strings.TrimSpace(string(connection.Status)) == "" {
		connection.Status = messaging.ConnectionStatusUnknown
	}
	if connection.UpdatedAt.IsZero() {
		connection.UpdatedAt = b.now()
	}

	if _, err := b.manager.Add(messagingruntime.ManagedAdapterSpec{
		Key:     string(connection.ID),
		Process: params.Process,
	}); err != nil {
		return err
	}
	if err := b.store.PutConnection(ctx, connection); err != nil {
		_ = b.manager.Remove(string(connection.ID))
		return err
	}
	return nil
}

// ConnectConnection waits for the adapter, computes runtime paths, and persists
// the adapter's initial connection + identity state.
func (b *Broker) ConnectConnection(ctx context.Context, connectionID messaging.ConnectionID) (ConnectResult, error) {
	connection, adapterClient, paths, describe, err := b.prepareAdapterCall(ctx, connectionID)
	if err != nil {
		return ConnectResult{}, err
	}

	result, err := adapterClient.Connect(ctx, protocol.ConnectParams{
		Connection: connection,
		Paths:      paths,
	})
	if err != nil {
		return ConnectResult{}, err
	}

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
	connection.UpdatedAt = now

	if err := b.store.PutConnection(ctx, connection); err != nil {
		return ConnectResult{}, err
	}
	if err := b.store.ReplaceConnectionIdentities(ctx, connection.ID, result.Identities); err != nil {
		return ConnectResult{}, err
	}
	if err := b.store.AppendEvent(ctx, messaging.Event{
		ID:           messaging.EventID(b.newID()),
		Type:         messaging.EventTypeConnectionUpdated,
		ConnectionID: connection.ID,
		Timestamp:    now,
	}); err != nil {
		return ConnectResult{}, err
	}
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

	persisted := make([]messaging.Event, 0, len(result.Events))
	conversations := make([]messaging.Conversation, 0)
	messages := make([]messaging.Message, 0)
	for _, rawEvent := range result.Events {
		event := cloneEvent(rawEvent)
		if strings.TrimSpace(string(event.ID)) == "" {
			event.ID = messaging.EventID(b.newID())
		}
		if strings.TrimSpace(string(event.ConnectionID)) == "" {
			event.ConnectionID = connectionID
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = b.now()
		}
		if err := b.store.AppendEvent(ctx, event); err != nil {
			return PollResult{}, err
		}
		persisted = append(persisted, event)

		conversation, message, err := b.hydrateEvent(ctx, adapterClient, connection, event)
		if err != nil {
			return PollResult{}, err
		}
		if conversation.ID != "" {
			conversations = appendIfConversationMissing(conversations, conversation)
		}
		if message.ID != "" {
			messages = appendIfMessageMissing(messages, message)
		}
	}

	var storedCheckpoint *protocol.Checkpoint
	if result.Checkpoint != nil {
		checkpointCopy := cloneCheckpoint(*result.Checkpoint)
		if checkpointCopy.UpdatedAt.IsZero() {
			checkpointCopy.UpdatedAt = b.now()
		}
		if err := b.store.PutCheckpoint(ctx, connectionID, checkpointCopy); err != nil {
			return PollResult{}, err
		}
		storedCheckpoint = &checkpointCopy
	}

	return PollResult{
		Connection:    connection,
		Events:        persisted,
		Checkpoint:    storedCheckpoint,
		Messages:      messages,
		Conversations: conversations,
	}, nil
}

func (b *Broker) prepareAdapterCall(ctx context.Context, connectionID messaging.ConnectionID) (messaging.Connection, *messagingruntime.AdapterClient, protocol.RuntimePaths, protocol.DescribeResult, error) {
	connection, ok := b.store.GetConnection(connectionID)
	if !ok {
		return messaging.Connection{}, nil, protocol.RuntimePaths{}, protocol.DescribeResult{}, fmt.Errorf("messaging connection %s not found", connectionID)
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
	return result.Message.Message, nil
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
