package broker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
	"github.com/sky10/sky10/pkg/messaging/protocol"
)

const (
	defaultSearchLimit = 25
	maxSearchLimit     = 100
)

// SearchIdentities searches people/accounts known to the broker index or, when
// Source is remote, delegates to the live platform adapter.
func (b *Broker) SearchIdentities(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error) {
	effective, query, source, err := b.authorizeSearch(params.ConnectionID, exposureID, messagingpolicy.SearchScopeIdentities, params.Query, params.Source)
	if err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	params.Query = query
	params.Source = source
	if source == protocol.SearchSourceRemote {
		return b.searchRemoteIdentities(ctx, effective, params)
	}

	hits := b.searchIndexedIdentities(effective.Policy, params.ConnectionID, query)
	page, nextCursor, err := paginateSearch(hits, params.PageRequest)
	if err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	return protocol.SearchIdentitiesResult{
		Hits:       page,
		Count:      len(page),
		Source:     protocol.SearchSourceIndexed,
		NextCursor: nextCursor,
	}, nil
}

// SearchConversations searches destination/thread metadata known to the broker
// index or delegates to the live platform adapter for remote lookup.
func (b *Broker) SearchConversations(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error) {
	effective, query, source, err := b.authorizeSearch(params.ConnectionID, exposureID, messagingpolicy.SearchScopeConversations, params.Query, params.Source)
	if err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	params.Query = query
	params.Source = source
	if source == protocol.SearchSourceRemote {
		return b.searchRemoteConversations(ctx, effective, params)
	}

	hits := b.searchIndexedConversations(effective.Policy, params.ConnectionID, query)
	page, nextCursor, err := paginateSearch(hits, params.PageRequest)
	if err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	return protocol.SearchConversationsResult{
		Hits:       page,
		Count:      len(page),
		Source:     protocol.SearchSourceIndexed,
		NextCursor: nextCursor,
	}, nil
}

// SearchMessages searches cached normalized message content or delegates to a
// live adapter when Source is remote.
func (b *Broker) SearchMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	effective, query, source, err := b.authorizeSearch(params.ConnectionID, exposureID, messagingpolicy.SearchScopeMessages, params.Query, params.Source)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	params.Query = query
	params.Source = source
	if source == protocol.SearchSourceRemote {
		return b.searchRemoteMessages(ctx, effective, params)
	}

	hits := b.searchIndexedMessages(effective.Policy, params, query)
	page, nextCursor, err := paginateSearch(hits, params.PageRequest)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	return protocol.SearchMessagesResult{
		Hits:       page,
		Count:      len(page),
		Source:     protocol.SearchSourceIndexed,
		NextCursor: nextCursor,
	}, nil
}

func (b *Broker) authorizeSearch(connectionID messaging.ConnectionID, exposureID messaging.ExposureID, scope messagingpolicy.SearchScope, rawQuery string, rawSource protocol.SearchSource) (EffectivePolicy, string, protocol.SearchSource, error) {
	if strings.TrimSpace(string(connectionID)) == "" {
		return EffectivePolicy{}, "", "", fmt.Errorf("connection_id is required")
	}
	query := strings.TrimSpace(rawQuery)
	if query == "" {
		return EffectivePolicy{}, "", "", fmt.Errorf("query is required")
	}
	source := rawSource
	if source == "" {
		source = protocol.SearchSourceIndexed
	}
	switch source {
	case protocol.SearchSourceIndexed, protocol.SearchSourceRemote:
	default:
		return EffectivePolicy{}, "", "", fmt.Errorf("unsupported search source %q", source)
	}
	effective, err := b.ResolvePolicy(connectionID, exposureID)
	if err != nil {
		return EffectivePolicy{}, "", "", err
	}
	decision := messagingpolicy.Search(effective.Policy, scope)
	if !decision.Allowed() {
		return EffectivePolicy{}, "", "", fmt.Errorf("search denied by policy: %s", decision.Reason)
	}
	return effective, query, source, nil
}

func (b *Broker) searchRemoteIdentities(ctx context.Context, effective EffectivePolicy, params protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error) {
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	if !describe.Adapter.Capabilities.SearchIdentities {
		return protocol.SearchIdentitiesResult{}, fmt.Errorf("adapter %s does not support identity search", describe.Adapter.ID)
	}
	result, err := adapterClient.SearchIdentities(ctx, params)
	if err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	filtered := make([]protocol.IdentitySearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		hit.Source = protocol.SearchSourceRemote
		if !b.identitySearchHitAllowed(hit, effective.Policy, params.ConnectionID) {
			continue
		}
		filtered = append(filtered, hit)
	}
	result.Hits = filtered
	result.Count = len(filtered)
	result.Source = protocol.SearchSourceRemote
	return result, nil
}

func (b *Broker) searchRemoteConversations(ctx context.Context, effective EffectivePolicy, params protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error) {
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	if !describe.Adapter.Capabilities.SearchConversations {
		return protocol.SearchConversationsResult{}, fmt.Errorf("adapter %s does not support conversation search", describe.Adapter.ID)
	}
	result, err := adapterClient.SearchConversations(ctx, params)
	if err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	filtered := make([]protocol.ConversationSearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		if hit.Conversation.ConnectionID == "" {
			hit.Conversation.ConnectionID = params.ConnectionID
		}
		hit.Source = protocol.SearchSourceRemote
		if !conversationAllowed(effective.Policy, hit.Conversation) || hit.Conversation.ConnectionID != params.ConnectionID {
			continue
		}
		if err := b.store.PutConversation(ctx, hit.Conversation); err != nil {
			return protocol.SearchConversationsResult{}, err
		}
		filtered = append(filtered, hit)
	}
	result.Hits = filtered
	result.Count = len(filtered)
	result.Source = protocol.SearchSourceRemote
	return result, nil
}

func (b *Broker) searchRemoteMessages(ctx context.Context, effective EffectivePolicy, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	_, adapterClient, _, describe, err := b.prepareAdapterCall(ctx, params.ConnectionID)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	if !describe.Adapter.Capabilities.SearchMessages {
		return protocol.SearchMessagesResult{}, fmt.Errorf("adapter %s does not support message search", describe.Adapter.ID)
	}
	result, err := adapterClient.SearchMessages(ctx, params)
	if err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	filtered := make([]protocol.MessageSearchHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		message := hit.Message.Message
		if message.ConnectionID == "" {
			message.ConnectionID = params.ConnectionID
			hit.Message.Message = message
		}
		if params.ConversationID != "" && message.ConversationID != params.ConversationID {
			continue
		}
		if message.ConnectionID != params.ConnectionID || !identityAllowed(effective.Policy, message.LocalIdentityID) {
			continue
		}
		if params.ContainerID != "" && len(hit.Message.Placements) > 0 && !messageRecordHasContainer(hit.Message, params.ContainerID) {
			continue
		}
		hit.Source = protocol.SearchSourceRemote
		if hit.Conversation != nil {
			conversation := *hit.Conversation
			if conversation.ConnectionID == "" {
				conversation.ConnectionID = params.ConnectionID
			}
			if conversation.ConnectionID == params.ConnectionID && conversationAllowed(effective.Policy, conversation) {
				if err := b.store.PutConversation(ctx, conversation); err != nil {
					return protocol.SearchMessagesResult{}, err
				}
				hit.Conversation = &conversation
			} else {
				hit.Conversation = nil
			}
		}
		if err := b.store.PutMessage(ctx, hit.Message.Message); err != nil {
			return protocol.SearchMessagesResult{}, err
		}
		for idx := range hit.Message.Placements {
			placement := hit.Message.Placements[idx]
			if placement.ConnectionID == "" {
				placement.ConnectionID = params.ConnectionID
			}
			if placement.MessageID == "" {
				placement.MessageID = hit.Message.Message.ID
			}
			if err := b.store.PutPlacement(ctx, placement); err != nil {
				return protocol.SearchMessagesResult{}, err
			}
			hit.Message.Placements[idx] = placement
		}
		filtered = append(filtered, hit)
	}
	result.Hits = filtered
	result.Count = len(filtered)
	result.Source = protocol.SearchSourceRemote
	return result, nil
}

func (b *Broker) searchIndexedIdentities(policy messaging.Policy, connectionID messaging.ConnectionID, query string) []protocol.IdentitySearchHit {
	hits := make([]protocol.IdentitySearchHit, 0)
	seen := make(map[string]struct{})
	for _, identity := range b.store.ListConnectionIdentities(connectionID) {
		if !identityAllowed(policy, identity.ID) {
			continue
		}
		matched, ok := matchedSearchFields(query,
			searchField{name: "identity_id", value: string(identity.ID)},
			searchField{name: "kind", value: string(identity.Kind)},
			searchField{name: "remote_id", value: identity.RemoteID},
			searchField{name: "address", value: identity.Address},
			searchField{name: "display_name", value: identity.DisplayName},
		)
		if !ok {
			continue
		}
		identityCopy := identity
		participant := participantForIdentity(identity)
		key := identitySearchKey(participant, &identityCopy)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		hits = append(hits, protocol.IdentitySearchHit{
			Participant:   participant,
			Identity:      &identityCopy,
			MatchedFields: matched,
			Source:        protocol.SearchSourceIndexed,
		})
	}
	for _, conversation := range b.store.ListConnectionConversations(connectionID) {
		if !conversationAllowed(policy, conversation) {
			continue
		}
		for _, participant := range conversation.Participants {
			matched, ok := matchedSearchFields(query,
				searchField{name: "participant_id", value: participant.ID},
				searchField{name: "kind", value: string(participant.Kind)},
				searchField{name: "remote_id", value: participant.RemoteID},
				searchField{name: "address", value: participant.Address},
				searchField{name: "display_name", value: participant.DisplayName},
				searchField{name: "identity_id", value: string(participant.IdentityID)},
			)
			if !ok {
				continue
			}
			key := identitySearchKey(participant, nil)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			hits = append(hits, protocol.IdentitySearchHit{
				Participant:    participant,
				ConversationID: conversation.ID,
				MatchedFields:  matched,
				Source:         protocol.SearchSourceIndexed,
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		left := identitySearchSortKey(hits[i])
		right := identitySearchSortKey(hits[j])
		if left != right {
			return left < right
		}
		return hits[i].ConversationID < hits[j].ConversationID
	})
	return hits
}

func (b *Broker) searchIndexedConversations(policy messaging.Policy, connectionID messaging.ConnectionID, query string) []protocol.ConversationSearchHit {
	hits := make([]protocol.ConversationSearchHit, 0)
	for _, conversation := range b.store.ListConnectionConversations(connectionID) {
		if !conversationAllowed(policy, conversation) {
			continue
		}
		fields := []searchField{
			{name: "conversation_id", value: string(conversation.ID)},
			{name: "kind", value: string(conversation.Kind)},
			{name: "remote_id", value: conversation.RemoteID},
			{name: "title", value: conversation.Title},
			{name: "parent_id", value: string(conversation.ParentID)},
		}
		fields = append(fields, participantSearchFields(conversation.Participants)...)
		fields = append(fields, metadataSearchFields(conversation.Metadata)...)
		matched, ok := matchedSearchFields(query, fields...)
		if !ok {
			continue
		}
		hits = append(hits, protocol.ConversationSearchHit{
			Conversation:  conversation,
			MatchedFields: matched,
			Source:        protocol.SearchSourceIndexed,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		left := strings.ToLower(hits[i].Conversation.Title)
		right := strings.ToLower(hits[j].Conversation.Title)
		if left != right {
			return left < right
		}
		return hits[i].Conversation.ID < hits[j].Conversation.ID
	})
	return hits
}

func (b *Broker) searchIndexedMessages(policy messaging.Policy, params protocol.SearchMessagesParams, query string) []protocol.MessageSearchHit {
	conversations := b.searchableConversations(policy, params.ConnectionID, params.ConversationID)
	hits := make([]protocol.MessageSearchHit, 0)
	for _, conversation := range conversations {
		for _, message := range b.store.ListConversationMessages(conversation.ID) {
			if !identityAllowed(policy, message.LocalIdentityID) {
				continue
			}
			if params.ContainerID != "" && !b.messageInContainer(message.ID, params.ContainerID) {
				continue
			}
			fields := []searchField{
				{name: "message_id", value: string(message.ID)},
				{name: "remote_id", value: message.RemoteID},
				{name: "direction", value: string(message.Direction)},
				{name: "sender_id", value: message.Sender.ID},
				{name: "sender_remote_id", value: message.Sender.RemoteID},
				{name: "sender_address", value: message.Sender.Address},
				{name: "sender_display_name", value: message.Sender.DisplayName},
				{name: "status", value: string(message.Status)},
				{name: "reply_to_remote_id", value: message.ReplyToRemoteID},
			}
			fields = append(fields, messagePartSearchFields(message.Parts)...)
			fields = append(fields, metadataSearchFields(message.Metadata)...)
			matched, ok := matchedSearchFields(query, fields...)
			if !ok {
				continue
			}
			conversationCopy := conversation
			hits = append(hits, protocol.MessageSearchHit{
				Message:       protocol.MessageRecord{Message: message, Placements: b.store.ListMessagePlacements(message.ID)},
				Conversation:  &conversationCopy,
				MatchedFields: matched,
				Source:        protocol.SearchSourceIndexed,
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		left := hits[i].Message.Message
		right := hits[j].Message.Message
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
	return hits
}

func (b *Broker) searchableConversations(policy messaging.Policy, connectionID messaging.ConnectionID, conversationID messaging.ConversationID) []messaging.Conversation {
	if conversationID != "" {
		conversation, ok := b.store.GetConversation(conversationID)
		if !ok || conversation.ConnectionID != connectionID || !conversationAllowed(policy, conversation) {
			return nil
		}
		return []messaging.Conversation{conversation}
	}
	conversations := b.store.ListConnectionConversations(connectionID)
	filtered := make([]messaging.Conversation, 0, len(conversations))
	for _, conversation := range conversations {
		if conversationAllowed(policy, conversation) {
			filtered = append(filtered, conversation)
		}
	}
	return filtered
}

func (b *Broker) messageInContainer(messageID messaging.MessageID, containerID messaging.ContainerID) bool {
	for _, placement := range b.store.ListMessagePlacements(messageID) {
		if placement.ContainerID == containerID {
			return true
		}
	}
	return false
}

func (b *Broker) identitySearchHitAllowed(hit protocol.IdentitySearchHit, policy messaging.Policy, connectionID messaging.ConnectionID) bool {
	if hit.Identity != nil {
		return hit.Identity.ConnectionID == connectionID && identityAllowed(policy, hit.Identity.ID)
	}
	if hit.ConversationID != "" {
		conversation, ok := b.store.GetConversation(hit.ConversationID)
		return ok && conversation.ConnectionID == connectionID && conversationAllowed(policy, conversation)
	}
	return true
}

func identityAllowed(policy messaging.Policy, identityID messaging.IdentityID) bool {
	if len(policy.Rules.AllowedIdentityIDs) == 0 {
		return true
	}
	for _, allowed := range policy.Rules.AllowedIdentityIDs {
		if allowed == identityID {
			return true
		}
	}
	return false
}

func conversationAllowed(policy messaging.Policy, conversation messaging.Conversation) bool {
	return identityAllowed(policy, conversation.LocalIdentityID)
}

func messageRecordHasContainer(record protocol.MessageRecord, containerID messaging.ContainerID) bool {
	if containerID == "" {
		return true
	}
	for _, placement := range record.Placements {
		if placement.ContainerID == containerID {
			return true
		}
	}
	return false
}

func participantForIdentity(identity messaging.Identity) messaging.Participant {
	kind := messaging.ParticipantKindAccount
	switch identity.Kind {
	case messaging.IdentityKindBot, messaging.IdentityKindWebhook:
		kind = messaging.ParticipantKindBot
	case messaging.IdentityKindPage:
		kind = messaging.ParticipantKindPage
	}
	return messaging.Participant{
		Kind:        kind,
		RemoteID:    identity.RemoteID,
		Address:     identity.Address,
		DisplayName: identity.DisplayName,
		IdentityID:  identity.ID,
		IsLocal:     true,
		Metadata:    cloneStringMap(identity.Metadata),
	}
}

func identitySearchKey(participant messaging.Participant, identity *messaging.Identity) string {
	if identity != nil && identity.ID != "" {
		return "identity:" + strings.ToLower(string(identity.ID))
	}
	switch {
	case strings.TrimSpace(participant.Address) != "":
		return "address:" + strings.ToLower(strings.TrimSpace(participant.Address))
	case strings.TrimSpace(participant.RemoteID) != "":
		return "remote:" + strings.ToLower(strings.TrimSpace(participant.RemoteID))
	case strings.TrimSpace(participant.ID) != "":
		return "id:" + strings.ToLower(strings.TrimSpace(participant.ID))
	default:
		return "display:" + strings.ToLower(strings.TrimSpace(participant.DisplayName)) + ":" + strings.ToLower(string(participant.Kind))
	}
}

func identitySearchSortKey(hit protocol.IdentitySearchHit) string {
	switch {
	case strings.TrimSpace(hit.Participant.DisplayName) != "":
		return strings.ToLower(hit.Participant.DisplayName)
	case strings.TrimSpace(hit.Participant.Address) != "":
		return strings.ToLower(hit.Participant.Address)
	case strings.TrimSpace(hit.Participant.RemoteID) != "":
		return strings.ToLower(hit.Participant.RemoteID)
	default:
		return strings.ToLower(string(hit.Participant.IdentityID))
	}
}

type searchField struct {
	name  string
	value string
}

func matchedSearchFields(query string, fields ...searchField) ([]string, bool) {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return nil, false
	}
	matched := make(map[string]struct{})
	for _, token := range tokens {
		tokenMatched := false
		for _, field := range fields {
			value := strings.ToLower(strings.TrimSpace(field.value))
			if value == "" || !strings.Contains(value, token) {
				continue
			}
			matched[field.name] = struct{}{}
			tokenMatched = true
		}
		if !tokenMatched {
			return nil, false
		}
	}
	names := make([]string, 0, len(matched))
	for name := range matched {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

func participantSearchFields(participants []messaging.Participant) []searchField {
	fields := make([]searchField, 0, len(participants)*5)
	for _, participant := range participants {
		fields = append(fields,
			searchField{name: "participant_id", value: participant.ID},
			searchField{name: "participant_kind", value: string(participant.Kind)},
			searchField{name: "participant_remote_id", value: participant.RemoteID},
			searchField{name: "participant_address", value: participant.Address},
			searchField{name: "participant_display_name", value: participant.DisplayName},
			searchField{name: "participant_identity_id", value: string(participant.IdentityID)},
		)
	}
	return fields
}

func messagePartSearchFields(parts []messaging.MessagePart) []searchField {
	fields := make([]searchField, 0, len(parts)*3)
	for _, part := range parts {
		fields = append(fields,
			searchField{name: "part_kind", value: string(part.Kind)},
			searchField{name: "part_content_type", value: part.ContentType},
			searchField{name: "part_text", value: part.Text},
			searchField{name: "part_file_name", value: part.FileName},
		)
	}
	return fields
}

func metadataSearchFields(metadata map[string]string) []searchField {
	if len(metadata) == 0 {
		return nil
	}
	fields := make([]searchField, 0, len(metadata)*2)
	for key, value := range metadata {
		fields = append(fields,
			searchField{name: "metadata_key", value: key},
			searchField{name: "metadata." + key, value: value},
		)
	}
	return fields
}

func paginateSearch[T any](items []T, page protocol.PageRequest) ([]T, string, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	offset := 0
	if strings.TrimSpace(page.Cursor) != "" {
		parsed, err := strconv.Atoi(page.Cursor)
		if err != nil || parsed < 0 {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		offset = parsed
	}
	if offset >= len(items) {
		return nil, "", nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	nextCursor := ""
	if end < len(items) {
		nextCursor = strconv.Itoa(end)
	}
	return items[offset:end], nextCursor, nil
}
