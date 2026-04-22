package store

import (
	"cmp"
	"sort"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

type recordIndex struct {
	connections             map[messaging.ConnectionID]messaging.Connection
	identities              map[messaging.IdentityID]messaging.Identity
	identitiesByConnection  map[messaging.ConnectionID][]messaging.IdentityID
	conversations           map[messaging.ConversationID]messaging.Conversation
	conversationsByConn     map[messaging.ConnectionID][]messaging.ConversationID
	messages                map[messaging.MessageID]messaging.Message
	messagesByConversation  map[messaging.ConversationID][]messaging.MessageID
	drafts                  map[messaging.DraftID]messaging.Draft
	draftsByConversation    map[messaging.ConversationID][]messaging.DraftID
	approvals               map[messaging.ApprovalID]messaging.Approval
	approvalsByDraft        map[messaging.DraftID][]messaging.ApprovalID
	approvalsByConnection   map[messaging.ConnectionID][]messaging.ApprovalID
	policies                map[messaging.PolicyID]messaging.Policy
	exposures               map[messaging.ExposureID]messaging.Exposure
	exposuresByConnection   map[messaging.ConnectionID][]messaging.ExposureID
	workflows               map[messaging.WorkflowID]messaging.Workflow
	activityByWorkflow      map[messaging.WorkflowID][]messaging.ActivityEvent
	eventsByConnection      map[messaging.ConnectionID][]messaging.Event
	checkpointsByConnection map[messaging.ConnectionID]protocol.Checkpoint
}

func newRecordIndex(snapshot Snapshot) *recordIndex {
	idx := &recordIndex{
		connections:             make(map[messaging.ConnectionID]messaging.Connection, len(snapshot.Connections)),
		identities:              make(map[messaging.IdentityID]messaging.Identity, len(snapshot.Identities)),
		identitiesByConnection:  make(map[messaging.ConnectionID][]messaging.IdentityID),
		conversations:           make(map[messaging.ConversationID]messaging.Conversation, len(snapshot.Conversations)),
		conversationsByConn:     make(map[messaging.ConnectionID][]messaging.ConversationID),
		messages:                make(map[messaging.MessageID]messaging.Message, len(snapshot.Messages)),
		messagesByConversation:  make(map[messaging.ConversationID][]messaging.MessageID),
		drafts:                  make(map[messaging.DraftID]messaging.Draft, len(snapshot.Drafts)),
		draftsByConversation:    make(map[messaging.ConversationID][]messaging.DraftID),
		approvals:               make(map[messaging.ApprovalID]messaging.Approval, len(snapshot.Approvals)),
		approvalsByDraft:        make(map[messaging.DraftID][]messaging.ApprovalID),
		approvalsByConnection:   make(map[messaging.ConnectionID][]messaging.ApprovalID),
		policies:                make(map[messaging.PolicyID]messaging.Policy, len(snapshot.Policies)),
		exposures:               make(map[messaging.ExposureID]messaging.Exposure, len(snapshot.Exposures)),
		exposuresByConnection:   make(map[messaging.ConnectionID][]messaging.ExposureID),
		workflows:               make(map[messaging.WorkflowID]messaging.Workflow, len(snapshot.Workflows)),
		activityByWorkflow:      make(map[messaging.WorkflowID][]messaging.ActivityEvent, len(snapshot.ActivityEvents)),
		eventsByConnection:      make(map[messaging.ConnectionID][]messaging.Event, len(snapshot.Events)),
		checkpointsByConnection: make(map[messaging.ConnectionID]protocol.Checkpoint, len(snapshot.Checkpoints)),
	}
	for _, connection := range snapshot.Connections {
		idx.putConnection(connection)
	}
	for _, identity := range snapshot.Identities {
		idx.putIdentity(identity)
	}
	for _, conversation := range snapshot.Conversations {
		idx.putConversation(conversation)
	}
	for _, message := range snapshot.Messages {
		idx.putMessage(message)
	}
	for _, draft := range snapshot.Drafts {
		idx.putDraft(draft)
	}
	for _, approval := range snapshot.Approvals {
		idx.putApproval(approval)
	}
	for _, policy := range snapshot.Policies {
		idx.putPolicy(policy)
	}
	for _, exposure := range snapshot.Exposures {
		idx.putExposure(exposure)
	}
	for _, workflow := range snapshot.Workflows {
		idx.putWorkflow(workflow)
	}
	for workflowID, events := range snapshot.ActivityEvents {
		for _, event := range events {
			idx.appendActivityEvent(workflowID, event)
		}
	}
	for connectionID, events := range snapshot.Events {
		for _, event := range events {
			idx.appendEvent(connectionID, event)
		}
	}
	for connectionID, checkpoint := range snapshot.Checkpoints {
		idx.putCheckpoint(connectionID, checkpoint)
	}
	return idx
}

func (idx *recordIndex) snapshot() Snapshot {
	snapshot := Snapshot{
		Connections:    make([]messaging.Connection, 0, len(idx.connections)),
		Identities:     make([]messaging.Identity, 0, len(idx.identities)),
		Conversations:  make([]messaging.Conversation, 0, len(idx.conversations)),
		Messages:       make([]messaging.Message, 0, len(idx.messages)),
		Drafts:         make([]messaging.Draft, 0, len(idx.drafts)),
		Approvals:      make([]messaging.Approval, 0, len(idx.approvals)),
		Policies:       make([]messaging.Policy, 0, len(idx.policies)),
		Exposures:      make([]messaging.Exposure, 0, len(idx.exposures)),
		Workflows:      make([]messaging.Workflow, 0, len(idx.workflows)),
		ActivityEvents: make(map[messaging.WorkflowID][]messaging.ActivityEvent, len(idx.activityByWorkflow)),
		Events:         make(map[messaging.ConnectionID][]messaging.Event, len(idx.eventsByConnection)),
		Checkpoints:    make(map[messaging.ConnectionID]protocol.Checkpoint, len(idx.checkpointsByConnection)),
	}
	for _, connection := range idx.connections {
		snapshot.Connections = append(snapshot.Connections, cloneConnection(connection))
	}
	sort.Slice(snapshot.Connections, func(i, j int) bool {
		return snapshot.Connections[i].ID < snapshot.Connections[j].ID
	})
	for _, identity := range idx.identities {
		snapshot.Identities = append(snapshot.Identities, cloneIdentity(identity))
	}
	sort.Slice(snapshot.Identities, func(i, j int) bool {
		return snapshot.Identities[i].ID < snapshot.Identities[j].ID
	})
	for _, conversation := range idx.conversations {
		snapshot.Conversations = append(snapshot.Conversations, cloneConversation(conversation))
	}
	sort.Slice(snapshot.Conversations, func(i, j int) bool {
		return snapshot.Conversations[i].ID < snapshot.Conversations[j].ID
	})
	for _, message := range idx.messages {
		snapshot.Messages = append(snapshot.Messages, cloneMessage(message))
	}
	sort.Slice(snapshot.Messages, func(i, j int) bool {
		if snapshot.Messages[i].CreatedAt.Equal(snapshot.Messages[j].CreatedAt) {
			return snapshot.Messages[i].ID < snapshot.Messages[j].ID
		}
		return snapshot.Messages[i].CreatedAt.Before(snapshot.Messages[j].CreatedAt)
	})
	for _, draft := range idx.drafts {
		snapshot.Drafts = append(snapshot.Drafts, cloneDraft(draft))
	}
	sort.Slice(snapshot.Drafts, func(i, j int) bool {
		return snapshot.Drafts[i].ID < snapshot.Drafts[j].ID
	})
	for _, approval := range idx.approvals {
		snapshot.Approvals = append(snapshot.Approvals, cloneApproval(approval))
	}
	sort.Slice(snapshot.Approvals, func(i, j int) bool {
		left := snapshot.Approvals[i]
		right := snapshot.Approvals[j]
		if left.RequestedAt.Equal(right.RequestedAt) {
			return left.ID < right.ID
		}
		return left.RequestedAt.Before(right.RequestedAt)
	})
	for _, policy := range idx.policies {
		snapshot.Policies = append(snapshot.Policies, clonePolicy(policy))
	}
	sort.Slice(snapshot.Policies, func(i, j int) bool {
		return snapshot.Policies[i].ID < snapshot.Policies[j].ID
	})
	for _, exposure := range idx.exposures {
		snapshot.Exposures = append(snapshot.Exposures, cloneExposure(exposure))
	}
	sort.Slice(snapshot.Exposures, func(i, j int) bool {
		return snapshot.Exposures[i].ID < snapshot.Exposures[j].ID
	})
	for _, workflow := range idx.workflows {
		snapshot.Workflows = append(snapshot.Workflows, cloneWorkflow(workflow))
	}
	sort.Slice(snapshot.Workflows, func(i, j int) bool {
		return snapshot.Workflows[i].ID < snapshot.Workflows[j].ID
	})
	for workflowID, events := range idx.activityByWorkflow {
		snapshot.ActivityEvents[workflowID] = cloneActivityEvents(events)
	}
	for connectionID, events := range idx.eventsByConnection {
		snapshot.Events[connectionID] = cloneEvents(events)
	}
	for connectionID, checkpoint := range idx.checkpointsByConnection {
		snapshot.Checkpoints[connectionID] = cloneCheckpoint(checkpoint)
	}
	return snapshot
}

func (idx *recordIndex) putConnection(connection messaging.Connection) {
	idx.connections[connection.ID] = cloneConnection(connection)
}

func (idx *recordIndex) putIdentity(identity messaging.Identity) {
	identity = cloneIdentity(identity)
	existing, exists := idx.identities[identity.ID]
	if exists && existing.ConnectionID != identity.ConnectionID {
		idx.removeIdentityFromConnection(existing.ConnectionID, identity.ID)
	}
	idx.identities[identity.ID] = identity
	idx.identitiesByConnection[identity.ConnectionID] = appendUnique(idx.identitiesByConnection[identity.ConnectionID], identity.ID)
}

func (idx *recordIndex) replaceConnectionIdentities(connectionID messaging.ConnectionID, identities []messaging.Identity) {
	existing := idx.identitiesByConnection[connectionID]
	keep := make(map[messaging.IdentityID]struct{}, len(identities))
	for _, identity := range identities {
		keep[identity.ID] = struct{}{}
	}
	for _, identityID := range existing {
		if _, ok := keep[identityID]; ok {
			continue
		}
		delete(idx.identities, identityID)
	}
	delete(idx.identitiesByConnection, connectionID)
	for _, identity := range identities {
		idx.putIdentity(identity)
	}
}

func (idx *recordIndex) putConversation(conversation messaging.Conversation) {
	conversation = cloneConversation(conversation)
	existing, exists := idx.conversations[conversation.ID]
	if exists && existing.ConnectionID != conversation.ConnectionID {
		idx.removeConversationFromConnection(existing.ConnectionID, conversation.ID)
	}
	idx.conversations[conversation.ID] = conversation
	idx.conversationsByConn[conversation.ConnectionID] = appendUnique(idx.conversationsByConn[conversation.ConnectionID], conversation.ID)
}

func (idx *recordIndex) putMessage(message messaging.Message) {
	message = cloneMessage(message)
	existing, exists := idx.messages[message.ID]
	if exists && existing.ConversationID != message.ConversationID {
		idx.removeMessageFromConversation(existing.ConversationID, message.ID)
	}
	idx.messages[message.ID] = message
	idx.messagesByConversation[message.ConversationID] = appendUnique(idx.messagesByConversation[message.ConversationID], message.ID)
}

func (idx *recordIndex) putDraft(draft messaging.Draft) {
	draft = cloneDraft(draft)
	existing, exists := idx.drafts[draft.ID]
	if exists && existing.ConversationID != draft.ConversationID {
		idx.removeDraftFromConversation(existing.ConversationID, draft.ID)
	}
	idx.drafts[draft.ID] = draft
	idx.draftsByConversation[draft.ConversationID] = appendUnique(idx.draftsByConversation[draft.ConversationID], draft.ID)
}

func (idx *recordIndex) putApproval(approval messaging.Approval) {
	approval = cloneApproval(approval)
	existing, exists := idx.approvals[approval.ID]
	if exists {
		if existing.DraftID != approval.DraftID {
			idx.removeApprovalFromDraft(existing.DraftID, approval.ID)
		}
		if existing.ConnectionID != approval.ConnectionID {
			idx.removeApprovalFromConnection(existing.ConnectionID, approval.ID)
		}
	}
	idx.approvals[approval.ID] = approval
	idx.approvalsByDraft[approval.DraftID] = appendUnique(idx.approvalsByDraft[approval.DraftID], approval.ID)
	idx.approvalsByConnection[approval.ConnectionID] = appendUnique(idx.approvalsByConnection[approval.ConnectionID], approval.ID)
}

func (idx *recordIndex) putPolicy(policy messaging.Policy) {
	idx.policies[policy.ID] = clonePolicy(policy)
}

func (idx *recordIndex) putExposure(exposure messaging.Exposure) {
	exposure = cloneExposure(exposure)
	existing, exists := idx.exposures[exposure.ID]
	if exists && existing.ConnectionID != exposure.ConnectionID {
		idx.removeExposureFromConnection(existing.ConnectionID, exposure.ID)
	}
	idx.exposures[exposure.ID] = exposure
	idx.exposuresByConnection[exposure.ConnectionID] = appendUnique(idx.exposuresByConnection[exposure.ConnectionID], exposure.ID)
}

func (idx *recordIndex) putWorkflow(workflow messaging.Workflow) {
	idx.workflows[workflow.ID] = cloneWorkflow(workflow)
}

func (idx *recordIndex) appendActivityEvent(workflowID messaging.WorkflowID, event messaging.ActivityEvent) {
	idx.activityByWorkflow[workflowID] = append(idx.activityByWorkflow[workflowID], cloneActivityEvent(event))
	sort.Slice(idx.activityByWorkflow[workflowID], func(i, j int) bool {
		left := idx.activityByWorkflow[workflowID][i]
		right := idx.activityByWorkflow[workflowID][j]
		if left.OccurredAt.Equal(right.OccurredAt) {
			return left.ID < right.ID
		}
		return left.OccurredAt.Before(right.OccurredAt)
	})
}

func (idx *recordIndex) appendEvent(connectionID messaging.ConnectionID, event messaging.Event) {
	idx.eventsByConnection[connectionID] = append(idx.eventsByConnection[connectionID], cloneEvent(event))
	sort.Slice(idx.eventsByConnection[connectionID], func(i, j int) bool {
		left := idx.eventsByConnection[connectionID][i]
		right := idx.eventsByConnection[connectionID][j]
		if left.Timestamp.Equal(right.Timestamp) {
			return left.ID < right.ID
		}
		return left.Timestamp.Before(right.Timestamp)
	})
}

func (idx *recordIndex) putCheckpoint(connectionID messaging.ConnectionID, checkpoint protocol.Checkpoint) {
	idx.checkpointsByConnection[connectionID] = cloneCheckpoint(checkpoint)
}

func (idx *recordIndex) getConnection(connectionID messaging.ConnectionID) (messaging.Connection, bool) {
	connection, ok := idx.connections[connectionID]
	if !ok {
		return messaging.Connection{}, false
	}
	return cloneConnection(connection), true
}

func (idx *recordIndex) listConnections() []messaging.Connection {
	connections := make([]messaging.Connection, 0, len(idx.connections))
	for _, connection := range idx.connections {
		connections = append(connections, cloneConnection(connection))
	}
	sort.Slice(connections, func(i, j int) bool {
		if cmp.Compare(connections[i].Label, connections[j].Label) != 0 {
			return connections[i].Label < connections[j].Label
		}
		return connections[i].ID < connections[j].ID
	})
	return connections
}

func (idx *recordIndex) getIdentity(identityID messaging.IdentityID) (messaging.Identity, bool) {
	identity, ok := idx.identities[identityID]
	if !ok {
		return messaging.Identity{}, false
	}
	return cloneIdentity(identity), true
}

func (idx *recordIndex) listConnectionIdentities(connectionID messaging.ConnectionID) []messaging.Identity {
	ids := idx.identitiesByConnection[connectionID]
	identities := make([]messaging.Identity, 0, len(ids))
	for _, identityID := range ids {
		identity, ok := idx.identities[identityID]
		if !ok {
			continue
		}
		identities = append(identities, cloneIdentity(identity))
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].IsDefault != identities[j].IsDefault {
			return identities[i].IsDefault
		}
		if cmp.Compare(identities[i].DisplayName, identities[j].DisplayName) != 0 {
			return identities[i].DisplayName < identities[j].DisplayName
		}
		if cmp.Compare(identities[i].Address, identities[j].Address) != 0 {
			return identities[i].Address < identities[j].Address
		}
		return identities[i].ID < identities[j].ID
	})
	return identities
}

func (idx *recordIndex) getConversation(conversationID messaging.ConversationID) (messaging.Conversation, bool) {
	conversation, ok := idx.conversations[conversationID]
	if !ok {
		return messaging.Conversation{}, false
	}
	return cloneConversation(conversation), true
}

func (idx *recordIndex) listConnectionConversations(connectionID messaging.ConnectionID) []messaging.Conversation {
	ids := idx.conversationsByConn[connectionID]
	conversations := make([]messaging.Conversation, 0, len(ids))
	for _, conversationID := range ids {
		conversation, ok := idx.conversations[conversationID]
		if !ok {
			continue
		}
		conversations = append(conversations, cloneConversation(conversation))
	}
	sort.Slice(conversations, func(i, j int) bool {
		if cmp.Compare(conversations[i].Title, conversations[j].Title) != 0 {
			return conversations[i].Title < conversations[j].Title
		}
		return conversations[i].ID < conversations[j].ID
	})
	return conversations
}

func (idx *recordIndex) getMessage(messageID messaging.MessageID) (messaging.Message, bool) {
	message, ok := idx.messages[messageID]
	if !ok {
		return messaging.Message{}, false
	}
	return cloneMessage(message), true
}

func (idx *recordIndex) listConversationMessages(conversationID messaging.ConversationID) []messaging.Message {
	ids := idx.messagesByConversation[conversationID]
	messages := make([]messaging.Message, 0, len(ids))
	for _, messageID := range ids {
		message, ok := idx.messages[messageID]
		if !ok {
			continue
		}
		messages = append(messages, cloneMessage(message))
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages
}

func (idx *recordIndex) getDraft(draftID messaging.DraftID) (messaging.Draft, bool) {
	draft, ok := idx.drafts[draftID]
	if !ok {
		return messaging.Draft{}, false
	}
	return cloneDraft(draft), true
}

func (idx *recordIndex) getApproval(approvalID messaging.ApprovalID) (messaging.Approval, bool) {
	approval, ok := idx.approvals[approvalID]
	if !ok {
		return messaging.Approval{}, false
	}
	return cloneApproval(approval), true
}

func (idx *recordIndex) listConversationDrafts(conversationID messaging.ConversationID) []messaging.Draft {
	ids := idx.draftsByConversation[conversationID]
	drafts := make([]messaging.Draft, 0, len(ids))
	for _, draftID := range ids {
		draft, ok := idx.drafts[draftID]
		if !ok {
			continue
		}
		drafts = append(drafts, cloneDraft(draft))
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].ID < drafts[j].ID
	})
	return drafts
}

func (idx *recordIndex) listDraftApprovals(draftID messaging.DraftID) []messaging.Approval {
	ids := idx.approvalsByDraft[draftID]
	approvals := make([]messaging.Approval, 0, len(ids))
	for _, approvalID := range ids {
		approval, ok := idx.approvals[approvalID]
		if !ok {
			continue
		}
		approvals = append(approvals, cloneApproval(approval))
	}
	sort.Slice(approvals, func(i, j int) bool {
		left := approvals[i]
		right := approvals[j]
		if left.RequestedAt.Equal(right.RequestedAt) {
			return left.ID < right.ID
		}
		return left.RequestedAt.Before(right.RequestedAt)
	})
	return approvals
}

func (idx *recordIndex) listConnectionApprovals(connectionID messaging.ConnectionID) []messaging.Approval {
	ids := idx.approvalsByConnection[connectionID]
	approvals := make([]messaging.Approval, 0, len(ids))
	for _, approvalID := range ids {
		approval, ok := idx.approvals[approvalID]
		if !ok {
			continue
		}
		approvals = append(approvals, cloneApproval(approval))
	}
	sort.Slice(approvals, func(i, j int) bool {
		left := approvals[i]
		right := approvals[j]
		if left.RequestedAt.Equal(right.RequestedAt) {
			return left.ID < right.ID
		}
		return left.RequestedAt.After(right.RequestedAt)
	})
	return approvals
}

func (idx *recordIndex) getPolicy(policyID messaging.PolicyID) (messaging.Policy, bool) {
	policy, ok := idx.policies[policyID]
	if !ok {
		return messaging.Policy{}, false
	}
	return clonePolicy(policy), true
}

func (idx *recordIndex) listPolicies() []messaging.Policy {
	policies := make([]messaging.Policy, 0, len(idx.policies))
	for _, policy := range idx.policies {
		policies = append(policies, clonePolicy(policy))
	}
	sort.Slice(policies, func(i, j int) bool {
		if cmp.Compare(policies[i].Name, policies[j].Name) != 0 {
			return policies[i].Name < policies[j].Name
		}
		return policies[i].ID < policies[j].ID
	})
	return policies
}

func (idx *recordIndex) getExposure(exposureID messaging.ExposureID) (messaging.Exposure, bool) {
	exposure, ok := idx.exposures[exposureID]
	if !ok {
		return messaging.Exposure{}, false
	}
	return cloneExposure(exposure), true
}

func (idx *recordIndex) listConnectionExposures(connectionID messaging.ConnectionID) []messaging.Exposure {
	ids := idx.exposuresByConnection[connectionID]
	exposures := make([]messaging.Exposure, 0, len(ids))
	for _, exposureID := range ids {
		exposure, ok := idx.exposures[exposureID]
		if !ok {
			continue
		}
		exposures = append(exposures, cloneExposure(exposure))
	}
	sort.Slice(exposures, func(i, j int) bool {
		if cmp.Compare(exposures[i].SubjectID, exposures[j].SubjectID) != 0 {
			return exposures[i].SubjectID < exposures[j].SubjectID
		}
		return exposures[i].ID < exposures[j].ID
	})
	return exposures
}

func (idx *recordIndex) getWorkflow(workflowID messaging.WorkflowID) (messaging.Workflow, bool) {
	workflow, ok := idx.workflows[workflowID]
	if !ok {
		return messaging.Workflow{}, false
	}
	return cloneWorkflow(workflow), true
}

func (idx *recordIndex) listWorkflows() []messaging.Workflow {
	workflows := make([]messaging.Workflow, 0, len(idx.workflows))
	for _, workflow := range idx.workflows {
		workflows = append(workflows, cloneWorkflow(workflow))
	}
	sort.Slice(workflows, func(i, j int) bool {
		if workflows[i].LastActivityAt.Equal(workflows[j].LastActivityAt) {
			return workflows[i].ID < workflows[j].ID
		}
		return workflows[i].LastActivityAt.After(workflows[j].LastActivityAt)
	})
	return workflows
}

func (idx *recordIndex) listWorkflowActivity(workflowID messaging.WorkflowID) []messaging.ActivityEvent {
	return cloneActivityEvents(idx.activityByWorkflow[workflowID])
}

func (idx *recordIndex) listConnectionEvents(connectionID messaging.ConnectionID) []messaging.Event {
	return cloneEvents(idx.eventsByConnection[connectionID])
}

func (idx *recordIndex) getCheckpoint(connectionID messaging.ConnectionID) (protocol.Checkpoint, bool) {
	checkpoint, ok := idx.checkpointsByConnection[connectionID]
	if !ok {
		return protocol.Checkpoint{}, false
	}
	return cloneCheckpoint(checkpoint), true
}

func (idx *recordIndex) removeIdentityFromConnection(connectionID messaging.ConnectionID, identityID messaging.IdentityID) {
	ids := idx.identitiesByConnection[connectionID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == identityID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.identitiesByConnection, connectionID)
		return
	}
	idx.identitiesByConnection[connectionID] = filtered
}

func (idx *recordIndex) removeConversationFromConnection(connectionID messaging.ConnectionID, conversationID messaging.ConversationID) {
	ids := idx.conversationsByConn[connectionID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == conversationID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.conversationsByConn, connectionID)
		return
	}
	idx.conversationsByConn[connectionID] = filtered
}

func (idx *recordIndex) removeMessageFromConversation(conversationID messaging.ConversationID, messageID messaging.MessageID) {
	ids := idx.messagesByConversation[conversationID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == messageID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.messagesByConversation, conversationID)
		return
	}
	idx.messagesByConversation[conversationID] = filtered
}

func (idx *recordIndex) removeDraftFromConversation(conversationID messaging.ConversationID, draftID messaging.DraftID) {
	ids := idx.draftsByConversation[conversationID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == draftID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.draftsByConversation, conversationID)
		return
	}
	idx.draftsByConversation[conversationID] = filtered
}

func (idx *recordIndex) removeApprovalFromDraft(draftID messaging.DraftID, approvalID messaging.ApprovalID) {
	ids := idx.approvalsByDraft[draftID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == approvalID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.approvalsByDraft, draftID)
		return
	}
	idx.approvalsByDraft[draftID] = filtered
}

func (idx *recordIndex) removeApprovalFromConnection(connectionID messaging.ConnectionID, approvalID messaging.ApprovalID) {
	ids := idx.approvalsByConnection[connectionID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == approvalID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.approvalsByConnection, connectionID)
		return
	}
	idx.approvalsByConnection[connectionID] = filtered
}

func (idx *recordIndex) removeExposureFromConnection(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) {
	ids := idx.exposuresByConnection[connectionID]
	filtered := ids[:0]
	for _, candidate := range ids {
		if candidate == exposureID {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		delete(idx.exposuresByConnection, connectionID)
		return
	}
	idx.exposuresByConnection[connectionID] = filtered
}

func appendUnique[T comparable](items []T, value T) []T {
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	return append(items, value)
}
