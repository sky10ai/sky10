package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/kv/collections"
	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

const defaultRootPrefix = "messaging"

// KVBackend persists messaging records under a shared KV prefix.
type KVBackend struct {
	store collections.KVStore
	root  string
}

// NewKVBackend creates a KV-backed messaging backend rooted at root.
func NewKVBackend(store collections.KVStore, root string) *KVBackend {
	root = strings.TrimSuffix(strings.TrimSpace(root), "/")
	if root == "" {
		root = defaultRootPrefix
	}
	return &KVBackend{store: store, root: root}
}

// Load rebuilds a messaging snapshot from KV state.
func (b *KVBackend) Load(_ context.Context) (Snapshot, error) {
	if b == nil || b.store == nil {
		return Snapshot{}, fmt.Errorf("messaging backend store is required")
	}

	connections, err := b.listConnections()
	if err != nil {
		return Snapshot{}, err
	}
	identities, err := b.listIdentities()
	if err != nil {
		return Snapshot{}, err
	}
	conversations, err := b.listConversations()
	if err != nil {
		return Snapshot{}, err
	}
	messages, err := b.listMessages()
	if err != nil {
		return Snapshot{}, err
	}
	drafts, err := b.listDrafts()
	if err != nil {
		return Snapshot{}, err
	}
	approvals, err := b.listApprovals()
	if err != nil {
		return Snapshot{}, err
	}
	policies, err := b.listPolicies()
	if err != nil {
		return Snapshot{}, err
	}
	exposures, err := b.listExposures()
	if err != nil {
		return Snapshot{}, err
	}
	workflows, err := b.listWorkflows()
	if err != nil {
		return Snapshot{}, err
	}

	activity := make(map[messaging.WorkflowID][]messaging.ActivityEvent, len(workflows))
	for _, workflow := range workflows {
		events, err := b.listActivityEvents(workflow.ID)
		if err != nil {
			return Snapshot{}, err
		}
		if len(events) > 0 {
			activity[workflow.ID] = events
		}
	}

	events := make(map[messaging.ConnectionID][]messaging.Event, len(connections))
	checkpoints := make(map[messaging.ConnectionID]protocol.Checkpoint, len(connections))
	for _, connection := range connections {
		connectionEvents, err := b.listEvents(connection.ID)
		if err != nil {
			return Snapshot{}, err
		}
		if len(connectionEvents) > 0 {
			events[connection.ID] = connectionEvents
		}
		checkpoint, ok, err := b.getCheckpoint(connection.ID)
		if err != nil {
			return Snapshot{}, err
		}
		if ok {
			checkpoints[connection.ID] = checkpoint
		}
	}

	return Snapshot{
		Connections:    connections,
		Identities:     identities,
		Conversations:  conversations,
		Messages:       messages,
		Drafts:         drafts,
		Approvals:      approvals,
		Policies:       policies,
		Exposures:      exposures,
		Workflows:      workflows,
		ActivityEvents: activity,
		Events:         events,
		Checkpoints:    checkpoints,
	}, nil
}

// PutConnection persists one normalized connection.
func (b *KVBackend) PutConnection(ctx context.Context, connection messaging.Connection) error {
	return b.putJSON(ctx, b.connectionKey(connection.ID), cloneConnection(connection))
}

// PutIdentity persists one normalized identity.
func (b *KVBackend) PutIdentity(ctx context.Context, identity messaging.Identity) error {
	return b.putJSON(ctx, b.identityKey(identity.ID), cloneIdentity(identity))
}

// ReplaceConnectionIdentities atomically replaces the known identities for one
// connection.
func (b *KVBackend) ReplaceConnectionIdentities(ctx context.Context, connectionID messaging.ConnectionID, identities []messaging.Identity) error {
	existing, err := b.listIdentities()
	if err != nil {
		return err
	}
	keep := make(map[messaging.IdentityID]struct{}, len(identities))
	for _, identity := range identities {
		if identity.ConnectionID != connectionID {
			return fmt.Errorf("identity %q has connection_id %q, want %q", identity.ID, identity.ConnectionID, connectionID)
		}
		keep[identity.ID] = struct{}{}
		if err := b.PutIdentity(ctx, identity); err != nil {
			return err
		}
	}
	for _, identity := range existing {
		if identity.ConnectionID != connectionID {
			continue
		}
		if _, ok := keep[identity.ID]; ok {
			continue
		}
		if err := b.store.Delete(ctx, b.identityKey(identity.ID)); err != nil {
			return fmt.Errorf("delete identity %s: %w", identity.ID, err)
		}
	}
	return nil
}

// PutConversation persists one normalized conversation.
func (b *KVBackend) PutConversation(ctx context.Context, conversation messaging.Conversation) error {
	return b.putJSON(ctx, b.conversationKey(conversation.ID), cloneConversation(conversation))
}

// PutMessage persists one normalized message.
func (b *KVBackend) PutMessage(ctx context.Context, message messaging.Message) error {
	return b.putJSON(ctx, b.messageKey(message.ID), cloneMessage(message))
}

// PutDraft persists one normalized draft.
func (b *KVBackend) PutDraft(ctx context.Context, draft messaging.Draft) error {
	return b.putJSON(ctx, b.draftKey(draft.ID), cloneDraft(draft))
}

// PutApproval persists one approval request.
func (b *KVBackend) PutApproval(ctx context.Context, approval messaging.Approval) error {
	return b.putJSON(ctx, b.approvalKey(approval.ID), cloneApproval(approval))
}

// PutPolicy persists one policy bundle.
func (b *KVBackend) PutPolicy(ctx context.Context, policy messaging.Policy) error {
	return b.putJSON(ctx, b.policyKey(policy.ID), clonePolicy(policy))
}

// PutExposure persists one exposure grant.
func (b *KVBackend) PutExposure(ctx context.Context, exposure messaging.Exposure) error {
	return b.putJSON(ctx, b.exposureKey(exposure.ID), cloneExposure(exposure))
}

// PutWorkflow persists one workflow summary.
func (b *KVBackend) PutWorkflow(ctx context.Context, workflow messaging.Workflow) error {
	return b.putJSON(ctx, b.workflowKey(workflow.ID), cloneWorkflow(workflow))
}

// AppendActivityEvent appends one workflow activity event to its durable log.
func (b *KVBackend) AppendActivityEvent(ctx context.Context, event messaging.ActivityEvent) error {
	body, err := json.Marshal(cloneActivityEvent(event))
	if err != nil {
		return fmt.Errorf("marshal activity event %s: %w", event.ID, err)
	}
	if _, err := collections.NewAppendLog(b.store, b.activityLogPrefix(event.WorkflowID)).Append(ctx, body); err != nil {
		return fmt.Errorf("append activity event %s: %w", event.ID, err)
	}
	return nil
}

// AppendEvent appends one connection event to its durable log.
func (b *KVBackend) AppendEvent(ctx context.Context, event messaging.Event) error {
	body, err := json.Marshal(cloneEvent(event))
	if err != nil {
		return fmt.Errorf("marshal event %s: %w", event.ID, err)
	}
	if _, err := collections.NewAppendLog(b.store, b.eventLogPrefix(event.ConnectionID)).Append(ctx, body); err != nil {
		return fmt.Errorf("append event %s: %w", event.ID, err)
	}
	return nil
}

// PutCheckpoint persists polling progress for one connection.
func (b *KVBackend) PutCheckpoint(ctx context.Context, connectionID messaging.ConnectionID, checkpoint protocol.Checkpoint) error {
	return b.putJSON(ctx, b.checkpointKey(connectionID), cloneCheckpoint(checkpoint))
}

func (b *KVBackend) listConnections() ([]messaging.Connection, error) {
	return listKVCollection(b.store, b.connectionsPrefix(), "connection", func(raw []byte) (messaging.Connection, error) {
		var value messaging.Connection
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Connection{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Connection{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listIdentities() ([]messaging.Identity, error) {
	return listKVCollection(b.store, b.identitiesPrefix(), "identity", func(raw []byte) (messaging.Identity, error) {
		var value messaging.Identity
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Identity{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Identity{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listConversations() ([]messaging.Conversation, error) {
	return listKVCollection(b.store, b.conversationsPrefix(), "conversation", func(raw []byte) (messaging.Conversation, error) {
		var value messaging.Conversation
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Conversation{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Conversation{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listMessages() ([]messaging.Message, error) {
	return listKVCollection(b.store, b.messagesPrefix(), "message", func(raw []byte) (messaging.Message, error) {
		var value messaging.Message
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Message{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Message{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listDrafts() ([]messaging.Draft, error) {
	return listKVCollection(b.store, b.draftsPrefix(), "draft", func(raw []byte) (messaging.Draft, error) {
		var value messaging.Draft
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Draft{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Draft{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listApprovals() ([]messaging.Approval, error) {
	return listKVCollection(b.store, b.approvalsPrefix(), "approval", func(raw []byte) (messaging.Approval, error) {
		var value messaging.Approval
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Approval{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Approval{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listPolicies() ([]messaging.Policy, error) {
	return listKVCollection(b.store, b.policiesPrefix(), "policy", func(raw []byte) (messaging.Policy, error) {
		var value messaging.Policy
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Policy{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Policy{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listExposures() ([]messaging.Exposure, error) {
	return listKVCollection(b.store, b.exposuresPrefix(), "exposure", func(raw []byte) (messaging.Exposure, error) {
		var value messaging.Exposure
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Exposure{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Exposure{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listWorkflows() ([]messaging.Workflow, error) {
	return listKVCollection(b.store, b.workflowsPrefix(), "workflow", func(raw []byte) (messaging.Workflow, error) {
		var value messaging.Workflow
		if err := json.Unmarshal(raw, &value); err != nil {
			return messaging.Workflow{}, err
		}
		if err := value.Validate(); err != nil {
			return messaging.Workflow{}, err
		}
		return value, nil
	})
}

func (b *KVBackend) listActivityEvents(workflowID messaging.WorkflowID) ([]messaging.ActivityEvent, error) {
	entries, err := collections.NewAppendLog(b.store, b.activityLogPrefix(workflowID)).List()
	if err != nil {
		return nil, err
	}
	events := make([]messaging.ActivityEvent, 0, len(entries))
	for _, entry := range entries {
		var event messaging.ActivityEvent
		if err := json.Unmarshal(entry.Value, &event); err != nil {
			return nil, fmt.Errorf("parse activity event %s for workflow %s: %w", entry.ID, workflowID, err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("validate activity event %s for workflow %s: %w", event.ID, workflowID, err)
		}
		events = append(events, cloneActivityEvent(event))
	}
	return events, nil
}

func (b *KVBackend) listEvents(connectionID messaging.ConnectionID) ([]messaging.Event, error) {
	entries, err := collections.NewAppendLog(b.store, b.eventLogPrefix(connectionID)).List()
	if err != nil {
		return nil, err
	}
	events := make([]messaging.Event, 0, len(entries))
	for _, entry := range entries {
		var event messaging.Event
		if err := json.Unmarshal(entry.Value, &event); err != nil {
			return nil, fmt.Errorf("parse event %s for connection %s: %w", entry.ID, connectionID, err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("validate event %s for connection %s: %w", event.ID, connectionID, err)
		}
		events = append(events, cloneEvent(event))
	}
	return events, nil
}

func (b *KVBackend) getCheckpoint(connectionID messaging.ConnectionID) (protocol.Checkpoint, bool, error) {
	raw, ok := b.store.Get(b.checkpointKey(connectionID))
	if !ok {
		return protocol.Checkpoint{}, false, nil
	}
	var checkpoint protocol.Checkpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return protocol.Checkpoint{}, false, fmt.Errorf("parse checkpoint for %s: %w", connectionID, err)
	}
	return cloneCheckpoint(checkpoint), true, nil
}

func (b *KVBackend) putJSON(ctx context.Context, key string, value any) error {
	if b == nil || b.store == nil {
		return fmt.Errorf("messaging backend store is required")
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return b.store.Set(ctx, key, body)
}

func listKVCollection[T any](store collections.KVStore, prefix, label string, decode func([]byte) (T, error)) ([]T, error) {
	if store == nil {
		return nil, fmt.Errorf("messaging backend store is required")
	}
	keys := store.List(prefix + "/")
	values := make([]T, 0, len(keys))
	for _, key := range keys {
		raw, ok := store.Get(key)
		if !ok {
			continue
		}
		value, err := decode(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s %q: %w", label, key, err)
		}
		values = append(values, value)
	}
	return values, nil
}

func (b *KVBackend) connectionsPrefix() string {
	return b.root + "/connections"
}

func (b *KVBackend) connectionKey(connectionID messaging.ConnectionID) string {
	return b.connectionsPrefix() + "/" + encodeKeyPart(string(connectionID))
}

func (b *KVBackend) identitiesPrefix() string {
	return b.root + "/identities"
}

func (b *KVBackend) identityKey(identityID messaging.IdentityID) string {
	return b.identitiesPrefix() + "/" + encodeKeyPart(string(identityID))
}

func (b *KVBackend) conversationsPrefix() string {
	return b.root + "/conversations"
}

func (b *KVBackend) conversationKey(conversationID messaging.ConversationID) string {
	return b.conversationsPrefix() + "/" + encodeKeyPart(string(conversationID))
}

func (b *KVBackend) messagesPrefix() string {
	return b.root + "/messages"
}

func (b *KVBackend) messageKey(messageID messaging.MessageID) string {
	return b.messagesPrefix() + "/" + encodeKeyPart(string(messageID))
}

func (b *KVBackend) draftsPrefix() string {
	return b.root + "/drafts"
}

func (b *KVBackend) draftKey(draftID messaging.DraftID) string {
	return b.draftsPrefix() + "/" + encodeKeyPart(string(draftID))
}

func (b *KVBackend) policiesPrefix() string {
	return b.root + "/policies"
}

func (b *KVBackend) approvalsPrefix() string {
	return b.root + "/approvals"
}

func (b *KVBackend) approvalKey(approvalID messaging.ApprovalID) string {
	return b.approvalsPrefix() + "/" + encodeKeyPart(string(approvalID))
}

func (b *KVBackend) policyKey(policyID messaging.PolicyID) string {
	return b.policiesPrefix() + "/" + encodeKeyPart(string(policyID))
}

func (b *KVBackend) exposuresPrefix() string {
	return b.root + "/exposures"
}

func (b *KVBackend) exposureKey(exposureID messaging.ExposureID) string {
	return b.exposuresPrefix() + "/" + encodeKeyPart(string(exposureID))
}

func (b *KVBackend) workflowsPrefix() string {
	return b.root + "/workflows"
}

func (b *KVBackend) workflowKey(workflowID messaging.WorkflowID) string {
	return b.workflowsPrefix() + "/" + encodeKeyPart(string(workflowID))
}

func (b *KVBackend) activityLogPrefix(workflowID messaging.WorkflowID) string {
	return b.root + "/activity/" + encodeKeyPart(string(workflowID))
}

func (b *KVBackend) eventLogPrefix(connectionID messaging.ConnectionID) string {
	return b.root + "/events/" + encodeKeyPart(string(connectionID))
}

func (b *KVBackend) checkpointsPrefix() string {
	return b.root + "/checkpoints"
}

func (b *KVBackend) checkpointKey(connectionID messaging.ConnectionID) string {
	return b.checkpointsPrefix() + "/" + encodeKeyPart(string(connectionID))
}

func encodeKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
