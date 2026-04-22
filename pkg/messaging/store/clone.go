package store

import (
	"slices"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := Snapshot{
		Connections:   make([]messaging.Connection, 0, len(snapshot.Connections)),
		Identities:    make([]messaging.Identity, 0, len(snapshot.Identities)),
		Conversations: make([]messaging.Conversation, 0, len(snapshot.Conversations)),
		Messages:      make([]messaging.Message, 0, len(snapshot.Messages)),
		Drafts:        make([]messaging.Draft, 0, len(snapshot.Drafts)),
		Policies:      make([]messaging.Policy, 0, len(snapshot.Policies)),
		Exposures:     make([]messaging.Exposure, 0, len(snapshot.Exposures)),
		Workflows:     make([]messaging.Workflow, 0, len(snapshot.Workflows)),
	}
	for _, connection := range snapshot.Connections {
		cloned.Connections = append(cloned.Connections, cloneConnection(connection))
	}
	for _, identity := range snapshot.Identities {
		cloned.Identities = append(cloned.Identities, cloneIdentity(identity))
	}
	for _, conversation := range snapshot.Conversations {
		cloned.Conversations = append(cloned.Conversations, cloneConversation(conversation))
	}
	for _, message := range snapshot.Messages {
		cloned.Messages = append(cloned.Messages, cloneMessage(message))
	}
	for _, draft := range snapshot.Drafts {
		cloned.Drafts = append(cloned.Drafts, cloneDraft(draft))
	}
	for _, policy := range snapshot.Policies {
		cloned.Policies = append(cloned.Policies, clonePolicy(policy))
	}
	for _, exposure := range snapshot.Exposures {
		cloned.Exposures = append(cloned.Exposures, cloneExposure(exposure))
	}
	for _, workflow := range snapshot.Workflows {
		cloned.Workflows = append(cloned.Workflows, cloneWorkflow(workflow))
	}
	if len(snapshot.ActivityEvents) > 0 {
		cloned.ActivityEvents = make(map[messaging.WorkflowID][]messaging.ActivityEvent, len(snapshot.ActivityEvents))
		for workflowID, events := range snapshot.ActivityEvents {
			cloned.ActivityEvents[workflowID] = cloneActivityEvents(events)
		}
	}
	if len(snapshot.Events) > 0 {
		cloned.Events = make(map[messaging.ConnectionID][]messaging.Event, len(snapshot.Events))
		for connectionID, events := range snapshot.Events {
			cloned.Events[connectionID] = cloneEvents(events)
		}
	}
	if len(snapshot.Checkpoints) > 0 {
		cloned.Checkpoints = make(map[messaging.ConnectionID]protocol.Checkpoint, len(snapshot.Checkpoints))
		for connectionID, checkpoint := range snapshot.Checkpoints {
			cloned.Checkpoints[connectionID] = cloneCheckpoint(checkpoint)
		}
	}
	return cloned
}

func cloneConnection(connection messaging.Connection) messaging.Connection {
	connection.Auth = cloneAuthInfo(connection.Auth)
	connection.Metadata = cloneStringMap(connection.Metadata)
	connection.ConnectedAt = connection.ConnectedAt.UTC()
	connection.UpdatedAt = connection.UpdatedAt.UTC()
	return connection
}

func cloneAuthInfo(auth messaging.AuthInfo) messaging.AuthInfo {
	auth.Scopes = slices.Clone(auth.Scopes)
	auth.ExpiresAt = cloneTimePtr(auth.ExpiresAt)
	return auth
}

func cloneIdentity(identity messaging.Identity) messaging.Identity {
	identity.Metadata = cloneStringMap(identity.Metadata)
	return identity
}

func cloneParticipant(participant messaging.Participant) messaging.Participant {
	participant.Metadata = cloneStringMap(participant.Metadata)
	return participant
}

func cloneParticipants(participants []messaging.Participant) []messaging.Participant {
	if len(participants) == 0 {
		return nil
	}
	out := make([]messaging.Participant, 0, len(participants))
	for _, participant := range participants {
		out = append(out, cloneParticipant(participant))
	}
	return out
}

func cloneConversation(conversation messaging.Conversation) messaging.Conversation {
	conversation.Participants = cloneParticipants(conversation.Participants)
	conversation.Metadata = cloneStringMap(conversation.Metadata)
	return conversation
}

func cloneMessagePart(part messaging.MessagePart) messaging.MessagePart {
	part.Metadata = cloneStringMap(part.Metadata)
	return part
}

func cloneMessageParts(parts []messaging.MessagePart) []messaging.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]messaging.MessagePart, 0, len(parts))
	for _, part := range parts {
		out = append(out, cloneMessagePart(part))
	}
	return out
}

func cloneMessage(message messaging.Message) messaging.Message {
	message.Sender = cloneParticipant(message.Sender)
	message.Parts = cloneMessageParts(message.Parts)
	message.EditedAt = cloneTimePtr(message.EditedAt)
	message.Metadata = cloneStringMap(message.Metadata)
	message.CreatedAt = message.CreatedAt.UTC()
	return message
}

func cloneDraft(draft messaging.Draft) messaging.Draft {
	draft.Parts = cloneMessageParts(draft.Parts)
	draft.Metadata = cloneStringMap(draft.Metadata)
	return draft
}

func clonePolicy(policy messaging.Policy) messaging.Policy {
	policy.Rules.AllowedIdentityIDs = slices.Clone(policy.Rules.AllowedIdentityIDs)
	policy.Metadata = cloneStringMap(policy.Metadata)
	return policy
}

func cloneExposure(exposure messaging.Exposure) messaging.Exposure {
	exposure.Metadata = cloneStringMap(exposure.Metadata)
	return exposure
}

func cloneWorkflow(workflow messaging.Workflow) messaging.Workflow {
	workflow.Sender = cloneParticipant(workflow.Sender)
	workflow.SourceCreatedAt = cloneTimePtr(workflow.SourceCreatedAt)
	workflow.RuleMatchedAt = cloneTimePtr(workflow.RuleMatchedAt)
	workflow.DraftCreatedAt = cloneTimePtr(workflow.DraftCreatedAt)
	workflow.OperatorNotifiedAt = cloneTimePtr(workflow.OperatorNotifiedAt)
	workflow.OperatorRespondedAt = cloneTimePtr(workflow.OperatorRespondedAt)
	workflow.ApprovedAt = cloneTimePtr(workflow.ApprovedAt)
	workflow.SendRequestedAt = cloneTimePtr(workflow.SendRequestedAt)
	workflow.SourceSentAt = cloneTimePtr(workflow.SourceSentAt)
	workflow.FulfilledAt = cloneTimePtr(workflow.FulfilledAt)
	workflow.Metadata = cloneStringMap(workflow.Metadata)
	workflow.BrokerReceivedAt = workflow.BrokerReceivedAt.UTC()
	workflow.LastActivityAt = workflow.LastActivityAt.UTC()
	return workflow
}

func cloneActivityEvent(event messaging.ActivityEvent) messaging.ActivityEvent {
	event.Metadata = cloneStringMap(event.Metadata)
	event.OccurredAt = event.OccurredAt.UTC()
	return event
}

func cloneActivityEvents(events []messaging.ActivityEvent) []messaging.ActivityEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]messaging.ActivityEvent, 0, len(events))
	for _, event := range events {
		out = append(out, cloneActivityEvent(event))
	}
	return out
}

func cloneEvent(event messaging.Event) messaging.Event {
	event.Metadata = cloneStringMap(event.Metadata)
	event.Timestamp = event.Timestamp.UTC()
	return event
}

func cloneEvents(events []messaging.Event) []messaging.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]messaging.Event, 0, len(events))
	for _, event := range events {
		out = append(out, cloneEvent(event))
	}
	return out
}

func cloneCheckpoint(checkpoint protocol.Checkpoint) protocol.Checkpoint {
	checkpoint.Metadata = cloneStringMap(checkpoint.Metadata)
	checkpoint.UpdatedAt = checkpoint.UpdatedAt.UTC()
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

func cloneTimePtr(ts *time.Time) *time.Time {
	if ts == nil {
		return nil
	}
	copy := ts.UTC()
	return &copy
}
