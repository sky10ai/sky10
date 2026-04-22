package store

import (
	"context"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

// Backend persists normalized messaging state and rebuilds store snapshots.
type Backend interface {
	Load(ctx context.Context) (Snapshot, error)
	PutConnection(ctx context.Context, connection messaging.Connection) error
	PutIdentity(ctx context.Context, identity messaging.Identity) error
	ReplaceConnectionIdentities(ctx context.Context, connectionID messaging.ConnectionID, identities []messaging.Identity) error
	PutConversation(ctx context.Context, conversation messaging.Conversation) error
	PutMessage(ctx context.Context, message messaging.Message) error
	PutDraft(ctx context.Context, draft messaging.Draft) error
	PutApproval(ctx context.Context, approval messaging.Approval) error
	PutPolicy(ctx context.Context, policy messaging.Policy) error
	PutExposure(ctx context.Context, exposure messaging.Exposure) error
	PutWorkflow(ctx context.Context, workflow messaging.Workflow) error
	AppendActivityEvent(ctx context.Context, event messaging.ActivityEvent) error
	AppendEvent(ctx context.Context, event messaging.Event) error
	PutCheckpoint(ctx context.Context, connectionID messaging.ConnectionID, checkpoint protocol.Checkpoint) error
}

// Snapshot is the durable messaging state materialized by a backend.
type Snapshot struct {
	Connections    []messaging.Connection
	Identities     []messaging.Identity
	Conversations  []messaging.Conversation
	Messages       []messaging.Message
	Drafts         []messaging.Draft
	Approvals      []messaging.Approval
	Policies       []messaging.Policy
	Exposures      []messaging.Exposure
	Workflows      []messaging.Workflow
	ActivityEvents map[messaging.WorkflowID][]messaging.ActivityEvent
	Events         map[messaging.ConnectionID][]messaging.Event
	Checkpoints    map[messaging.ConnectionID]protocol.Checkpoint
}
