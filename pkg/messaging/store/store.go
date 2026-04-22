package store

import (
	"context"
	"fmt"
	"sync"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

// Store materializes durable messaging state into broker-friendly read views.
type Store struct {
	backend Backend

	mu    sync.RWMutex
	index *recordIndex
}

// NewStore creates a messaging store and rebuilds its in-memory index.
func NewStore(ctx context.Context, backend Backend) (*Store, error) {
	if backend == nil {
		return nil, fmt.Errorf("messaging backend is required")
	}
	s := &Store{backend: backend}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload rebuilds the materialized read index from persisted state.
func (s *Store) Reload(ctx context.Context) error {
	snapshot, err := s.backend.Load(ctx)
	if err != nil {
		return fmt.Errorf("load messaging snapshot: %w", err)
	}
	s.mu.Lock()
	s.index = newRecordIndex(snapshot)
	s.mu.Unlock()
	return nil
}

// Snapshot returns a cloned view of the current indexed state.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return Snapshot{}
	}
	return cloneSnapshot(s.index.snapshot())
}

// PutConnection persists one normalized connection.
func (s *Store) PutConnection(ctx context.Context, connection messaging.Connection) error {
	if err := connection.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutConnection(ctx, connection); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putConnection(connection)
	s.mu.Unlock()
	return nil
}

// PutIdentity persists one normalized identity.
func (s *Store) PutIdentity(ctx context.Context, identity messaging.Identity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutIdentity(ctx, identity); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putIdentity(identity)
	s.mu.Unlock()
	return nil
}

// ReplaceConnectionIdentities atomically replaces the known identity set for
// one connection.
func (s *Store) ReplaceConnectionIdentities(ctx context.Context, connectionID messaging.ConnectionID, identities []messaging.Identity) error {
	if string(connectionID) == "" {
		return fmt.Errorf("connection id is required")
	}
	for idx, identity := range identities {
		if err := identity.Validate(); err != nil {
			return fmt.Errorf("identities[%d]: %w", idx, err)
		}
		if identity.ConnectionID != connectionID {
			return fmt.Errorf("identities[%d] connection_id %q does not match %q", idx, identity.ConnectionID, connectionID)
		}
	}
	if err := s.backend.ReplaceConnectionIdentities(ctx, connectionID, identities); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.replaceConnectionIdentities(connectionID, identities)
	s.mu.Unlock()
	return nil
}

// PutConversation persists one normalized conversation.
func (s *Store) PutConversation(ctx context.Context, conversation messaging.Conversation) error {
	if err := conversation.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutConversation(ctx, conversation); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putConversation(conversation)
	s.mu.Unlock()
	return nil
}

// PutMessage persists one normalized message.
func (s *Store) PutMessage(ctx context.Context, message messaging.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutMessage(ctx, message); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putMessage(message)
	s.mu.Unlock()
	return nil
}

// PutDraft persists one normalized draft.
func (s *Store) PutDraft(ctx context.Context, draft messaging.Draft) error {
	if err := draft.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutDraft(ctx, draft); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putDraft(draft)
	s.mu.Unlock()
	return nil
}

// PutApproval persists one normalized approval request.
func (s *Store) PutApproval(ctx context.Context, approval messaging.Approval) error {
	if err := approval.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutApproval(ctx, approval); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putApproval(approval)
	s.mu.Unlock()
	return nil
}

// PutPolicy persists one named policy bundle.
func (s *Store) PutPolicy(ctx context.Context, policy messaging.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutPolicy(ctx, policy); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putPolicy(policy)
	s.mu.Unlock()
	return nil
}

// PutExposure persists one exposure grant.
func (s *Store) PutExposure(ctx context.Context, exposure messaging.Exposure) error {
	if err := exposure.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutExposure(ctx, exposure); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putExposure(exposure)
	s.mu.Unlock()
	return nil
}

// PutWorkflow persists one human-facing workflow record.
func (s *Store) PutWorkflow(ctx context.Context, workflow messaging.Workflow) error {
	if err := workflow.Validate(); err != nil {
		return err
	}
	if err := s.backend.PutWorkflow(ctx, workflow); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putWorkflow(workflow)
	s.mu.Unlock()
	return nil
}

// AppendActivityEvent appends one durable workflow activity event.
func (s *Store) AppendActivityEvent(ctx context.Context, event messaging.ActivityEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if err := s.backend.AppendActivityEvent(ctx, event); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.appendActivityEvent(event.WorkflowID, event)
	s.mu.Unlock()
	return nil
}

// AppendEvent appends one durable connection event.
func (s *Store) AppendEvent(ctx context.Context, event messaging.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if err := s.backend.AppendEvent(ctx, event); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.appendEvent(event.ConnectionID, event)
	s.mu.Unlock()
	return nil
}

// PutCheckpoint persists polling progress for one connection.
func (s *Store) PutCheckpoint(ctx context.Context, connectionID messaging.ConnectionID, checkpoint protocol.Checkpoint) error {
	if string(connectionID) == "" {
		return fmt.Errorf("connection id is required")
	}
	if err := s.backend.PutCheckpoint(ctx, connectionID, checkpoint); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureIndexLocked()
	s.index.putCheckpoint(connectionID, checkpoint)
	s.mu.Unlock()
	return nil
}

// GetConnection returns one persisted connection.
func (s *Store) GetConnection(connectionID messaging.ConnectionID) (messaging.Connection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Connection{}, false
	}
	return s.index.getConnection(connectionID)
}

// ListConnections returns all connections in stable order.
func (s *Store) ListConnections() []messaging.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnections()
}

// GetIdentity returns one persisted identity.
func (s *Store) GetIdentity(identityID messaging.IdentityID) (messaging.Identity, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Identity{}, false
	}
	return s.index.getIdentity(identityID)
}

// ListConnectionIdentities returns identities scoped to one connection.
func (s *Store) ListConnectionIdentities(connectionID messaging.ConnectionID) []messaging.Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnectionIdentities(connectionID)
}

// GetConversation returns one persisted conversation.
func (s *Store) GetConversation(conversationID messaging.ConversationID) (messaging.Conversation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Conversation{}, false
	}
	return s.index.getConversation(conversationID)
}

// ListConnectionConversations returns all conversations for one connection.
func (s *Store) ListConnectionConversations(connectionID messaging.ConnectionID) []messaging.Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnectionConversations(connectionID)
}

// GetMessage returns one persisted message.
func (s *Store) GetMessage(messageID messaging.MessageID) (messaging.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Message{}, false
	}
	return s.index.getMessage(messageID)
}

// ListConversationMessages returns all messages for one conversation.
func (s *Store) ListConversationMessages(conversationID messaging.ConversationID) []messaging.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConversationMessages(conversationID)
}

// GetDraft returns one persisted draft.
func (s *Store) GetDraft(draftID messaging.DraftID) (messaging.Draft, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Draft{}, false
	}
	return s.index.getDraft(draftID)
}

// GetApproval returns one persisted approval request.
func (s *Store) GetApproval(approvalID messaging.ApprovalID) (messaging.Approval, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Approval{}, false
	}
	return s.index.getApproval(approvalID)
}

// ListConversationDrafts returns drafts for one conversation.
func (s *Store) ListConversationDrafts(conversationID messaging.ConversationID) []messaging.Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConversationDrafts(conversationID)
}

// ListDraftApprovals returns approvals associated with one draft.
func (s *Store) ListDraftApprovals(draftID messaging.DraftID) []messaging.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listDraftApprovals(draftID)
}

// ListConnectionApprovals returns approvals scoped to one connection.
func (s *Store) ListConnectionApprovals(connectionID messaging.ConnectionID) []messaging.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnectionApprovals(connectionID)
}

// GetPolicy returns one persisted policy.
func (s *Store) GetPolicy(policyID messaging.PolicyID) (messaging.Policy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Policy{}, false
	}
	return s.index.getPolicy(policyID)
}

// ListPolicies returns all known policies.
func (s *Store) ListPolicies() []messaging.Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listPolicies()
}

// GetExposure returns one persisted exposure.
func (s *Store) GetExposure(exposureID messaging.ExposureID) (messaging.Exposure, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Exposure{}, false
	}
	return s.index.getExposure(exposureID)
}

// ListConnectionExposures returns exposures scoped to one connection.
func (s *Store) ListConnectionExposures(connectionID messaging.ConnectionID) []messaging.Exposure {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnectionExposures(connectionID)
}

// GetWorkflow returns one persisted workflow.
func (s *Store) GetWorkflow(workflowID messaging.WorkflowID) (messaging.Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return messaging.Workflow{}, false
	}
	return s.index.getWorkflow(workflowID)
}

// ListWorkflows returns workflows in operator-friendly order.
func (s *Store) ListWorkflows() []messaging.Workflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listWorkflows()
}

// ListWorkflowActivity returns audit activity for one workflow.
func (s *Store) ListWorkflowActivity(workflowID messaging.WorkflowID) []messaging.ActivityEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listWorkflowActivity(workflowID)
}

// ListConnectionEvents returns broker events for one connection.
func (s *Store) ListConnectionEvents(connectionID messaging.ConnectionID) []messaging.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return nil
	}
	return s.index.listConnectionEvents(connectionID)
}

// GetCheckpoint returns the last stored polling checkpoint for one connection.
func (s *Store) GetCheckpoint(connectionID messaging.ConnectionID) (protocol.Checkpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return protocol.Checkpoint{}, false
	}
	return s.index.getCheckpoint(connectionID)
}

func (s *Store) ensureIndexLocked() {
	if s.index == nil {
		s.index = newRecordIndex(Snapshot{})
	}
}
