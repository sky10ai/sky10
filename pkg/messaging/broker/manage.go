package broker

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/pkg/messaging"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

// ListContainers asks an adapter for provider-side containers and persists the
// returned cache for later policy and UI use.
func (b *Broker) ListContainers(ctx context.Context, params protocol.ListContainersParams) (protocol.ListContainersResult, error) {
	if params.ConnectionID == "" {
		return protocol.ListContainersResult{}, fmt.Errorf("connection_id is required")
	}
	connection, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ListContainersResult{}, err
	}
	if !describe.Adapter.Capabilities.ListContainers {
		return protocol.ListContainersResult{}, fmt.Errorf("adapter %s does not support listing containers", describe.Adapter.ID)
	}
	result, err := adapterClient.ListContainers(ctx, params)
	if err != nil {
		return protocol.ListContainersResult{}, err
	}
	for idx := range result.Containers {
		if result.Containers[idx].ConnectionID == "" {
			result.Containers[idx].ConnectionID = connection.ID
		}
		if err := b.store.PutContainer(ctx, result.Containers[idx]); err != nil {
			return protocol.ListContainersResult{}, err
		}
	}
	return result, nil
}

// MoveMessages moves specific messages to a provider-side container.
func (b *Broker) MoveMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if len(params.MessageIDs) == 0 {
		return protocol.ManageMessagesResult{}, fmt.Errorf("message_ids are required")
	}
	if params.DestinationContainerID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("destination_container_id is required")
	}
	if err := b.authorizeManageMessages(params.ConnectionID, exposureID, messagingpolicy.ManageInput{
		DestinationContainerID: params.DestinationContainerID,
	}); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.MoveMessages {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support moving messages", describe.Adapter.ID)
	}
	result, err := adapterClient.MoveMessages(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

// MoveConversation moves every adapter-supported message in a conversation to a
// provider-side container.
func (b *Broker) MoveConversation(ctx context.Context, exposureID messaging.ExposureID, params protocol.MoveConversationParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if params.ConversationID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("conversation_id is required")
	}
	if params.DestinationContainerID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("destination_container_id is required")
	}
	if err := b.authorizeManageMessages(params.ConnectionID, exposureID, messagingpolicy.ManageInput{
		DestinationContainerID: params.DestinationContainerID,
	}); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.MoveConversations {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support moving conversations", describe.Adapter.ID)
	}
	result, err := adapterClient.MoveConversation(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

// ArchiveMessages archives specific messages using provider semantics.
func (b *Broker) ArchiveMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if len(params.MessageIDs) == 0 {
		return protocol.ManageMessagesResult{}, fmt.Errorf("message_ids are required")
	}
	if err := b.authorizeManageMessages(params.ConnectionID, exposureID, messagingpolicy.ManageInput{
		DestinationContainerID: params.ContainerID,
	}); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.ArchiveMessages {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support archiving messages", describe.Adapter.ID)
	}
	result, err := adapterClient.ArchiveMessages(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

// ArchiveConversation archives every adapter-supported message in a
// conversation using provider semantics.
func (b *Broker) ArchiveConversation(ctx context.Context, exposureID messaging.ExposureID, params protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if params.ConversationID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("conversation_id is required")
	}
	if err := b.authorizeManageMessages(params.ConnectionID, exposureID, messagingpolicy.ManageInput{
		DestinationContainerID: params.ContainerID,
	}); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.ArchiveConversations {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support archiving conversations", describe.Adapter.ID)
	}
	result, err := adapterClient.ArchiveConversation(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

// ApplyLabels mutates label-like provider containers on messages or a
// conversation.
func (b *Broker) ApplyLabels(ctx context.Context, exposureID messaging.ExposureID, params protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if params.ConversationID == "" && len(params.MessageIDs) == 0 {
		return protocol.ManageMessagesResult{}, fmt.Errorf("conversation_id or message_ids are required")
	}
	if len(params.Add) == 0 && len(params.Remove) == 0 {
		return protocol.ManageMessagesResult{}, fmt.Errorf("add or remove labels are required")
	}
	if err := b.authorizeManageMessages(params.ConnectionID, exposureID, messagingpolicy.ManageInput{
		AddContainerIDs:    params.Add,
		RemoveContainerIDs: params.Remove,
	}); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.ApplyLabels {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support applying labels", describe.Adapter.ID)
	}
	result, err := adapterClient.ApplyLabels(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

// MarkRead changes read state on messages or a conversation.
func (b *Broker) MarkRead(ctx context.Context, exposureID messaging.ExposureID, params protocol.MarkReadParams) (protocol.ManageMessagesResult, error) {
	if params.ConnectionID == "" {
		return protocol.ManageMessagesResult{}, fmt.Errorf("connection_id is required")
	}
	if params.ConversationID == "" && len(params.MessageIDs) == 0 {
		return protocol.ManageMessagesResult{}, fmt.Errorf("conversation_id or message_ids are required")
	}
	decision, err := b.EvaluateMarkRead(params.ConnectionID, exposureID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if !decision.Allowed() {
		return protocol.ManageMessagesResult{}, fmt.Errorf("mark read denied by policy: %s", decision.Reason)
	}
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	if params.Read && !describe.Adapter.Capabilities.MarkRead {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support marking messages read", describe.Adapter.ID)
	}
	if !params.Read && !describe.Adapter.Capabilities.MarkUnread {
		return protocol.ManageMessagesResult{}, fmt.Errorf("adapter %s does not support marking messages unread", describe.Adapter.ID)
	}
	result, err := adapterClient.MarkRead(ctx, params)
	if err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return b.persistManageMessagesResult(ctx, params.ConnectionID, result)
}

func (b *Broker) authorizeManageMessages(connectionID messaging.ConnectionID, exposureID messaging.ExposureID, input messagingpolicy.ManageInput) error {
	decision, err := b.EvaluateManageMessages(connectionID, exposureID, input)
	if err != nil {
		return err
	}
	if !decision.Allowed() {
		return fmt.Errorf("message management denied by policy: %s", decision.Reason)
	}
	return nil
}

func (b *Broker) persistManageMessagesResult(ctx context.Context, connectionID messaging.ConnectionID, result protocol.ManageMessagesResult) (protocol.ManageMessagesResult, error) {
	for idx := range result.Messages {
		message := result.Messages[idx].Message
		if message.ConnectionID == "" {
			message.ConnectionID = connectionID
			result.Messages[idx].Message = message
		}
		if err := b.store.PutMessage(ctx, message); err != nil {
			return protocol.ManageMessagesResult{}, err
		}
		for placementIdx := range result.Messages[idx].Placements {
			placement := result.Messages[idx].Placements[placementIdx]
			if placement.ConnectionID == "" {
				placement.ConnectionID = connectionID
			}
			if placement.MessageID == "" {
				placement.MessageID = message.ID
			}
			if err := b.store.PutPlacement(ctx, placement); err != nil {
				return protocol.ManageMessagesResult{}, err
			}
			result.Messages[idx].Placements[placementIdx] = placement
		}
	}
	for idx := range result.Placements {
		placement := result.Placements[idx]
		if placement.ConnectionID == "" {
			placement.ConnectionID = connectionID
		}
		if err := b.store.PutPlacement(ctx, placement); err != nil {
			return protocol.ManageMessagesResult{}, err
		}
		result.Placements[idx] = placement
	}
	for idx := range result.Changes {
		change, err := b.persistPlacementChange(ctx, connectionID, result.Changes[idx])
		if err != nil {
			return protocol.ManageMessagesResult{}, err
		}
		result.Changes[idx] = change
	}
	for idx := range result.Events {
		event := cloneEvent(result.Events[idx])
		if event.ConnectionID == "" {
			event.ConnectionID = connectionID
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = b.now()
		}
		if event.ID == "" {
			event.ID = stableEventID(connectionID, event)
		}
		if err := b.store.AppendEvent(ctx, event); err != nil {
			return protocol.ManageMessagesResult{}, err
		}
		result.Events[idx] = event
	}
	return result, nil
}

func (b *Broker) persistPlacementChange(ctx context.Context, connectionID messaging.ConnectionID, change protocol.PlacementChange) (protocol.PlacementChange, error) {
	messageID := change.MessageID
	if messageID == "" && change.Message != nil {
		messageID = change.Message.ID
	}
	if messageID == "" && change.Placement != nil {
		messageID = change.Placement.MessageID
	}
	if change.Message != nil {
		message := *change.Message
		if message.ID == "" {
			message.ID = messageID
		}
		if message.ConnectionID == "" {
			message.ConnectionID = connectionID
		}
		if err := b.store.PutMessage(ctx, message); err != nil {
			return protocol.PlacementChange{}, err
		}
		change.Message = &message
		messageID = message.ID
	}
	if messageID == "" && (len(change.Removed) > 0 || len(change.Added) > 0 || change.Placement != nil) {
		return protocol.PlacementChange{}, fmt.Errorf("placement change message_id is required")
	}
	change.MessageID = messageID
	for _, containerID := range change.Removed {
		if err := b.store.DeletePlacement(ctx, messageID, containerID); err != nil {
			return protocol.PlacementChange{}, err
		}
	}
	if change.Placement != nil {
		placement := *change.Placement
		if placement.ConnectionID == "" {
			placement.ConnectionID = connectionID
		}
		if placement.MessageID == "" {
			placement.MessageID = messageID
		}
		if err := b.store.PutPlacement(ctx, placement); err != nil {
			return protocol.PlacementChange{}, err
		}
		change.Placement = &placement
	}
	for _, containerID := range change.Added {
		if _, ok := b.store.GetPlacement(messageID, containerID); ok {
			continue
		}
		if err := b.store.PutPlacement(ctx, messaging.Placement{
			MessageID:    messageID,
			ConnectionID: connectionID,
			ContainerID:  containerID,
		}); err != nil {
			return protocol.PlacementChange{}, err
		}
	}
	return change, nil
}
