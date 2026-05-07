package shim

import (
	"context"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingpolicy "github.com/sky10/sky10/pkg/messaging/policy"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

// Method is one stable host shim operation.
type Method string

const (
	MethodListConnections     Method = "messaging.shim.listConnections"
	MethodListIdentities      Method = "messaging.shim.listIdentities"
	MethodListConversations   Method = "messaging.shim.listConversations"
	MethodGetConversation     Method = "messaging.shim.getConversation"
	MethodGetMessages         Method = "messaging.shim.getMessages"
	MethodListContainers      Method = "messaging.shim.listContainers"
	MethodSearchIdentities    Method = "messaging.shim.searchIdentities"
	MethodSearchConversations Method = "messaging.shim.searchConversations"
	MethodSearchMessages      Method = "messaging.shim.searchMessages"
	MethodCreateDraft         Method = "messaging.shim.createDraft"
	MethodUpdateDraft         Method = "messaging.shim.updateDraft"
	MethodRequestSend         Method = "messaging.shim.requestSend"
	MethodMoveMessages        Method = "messaging.shim.moveMessages"
	MethodMoveConversation    Method = "messaging.shim.moveConversation"
	MethodArchiveMessages     Method = "messaging.shim.archiveMessages"
	MethodArchiveConversation Method = "messaging.shim.archiveConversation"
	MethodApplyLabels         Method = "messaging.shim.applyLabels"
	MethodMarkRead            Method = "messaging.shim.markRead"
	MethodSubscribeEvents     Method = "messaging.shim.subscribeEvents"
)

// Broker is the broker surface the shim needs. The concrete broker satisfies
// this interface; tests can also provide narrow fakes when needed.
type Broker interface {
	ResolvePolicy(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) (messagingbroker.EffectivePolicy, error)
	EvaluateReadInbound(connectionID messaging.ConnectionID, exposureID messaging.ExposureID) (messagingpolicy.Decision, error)
	ListContainers(ctx context.Context, params protocol.ListContainersParams) (protocol.ListContainersResult, error)
	SearchIdentities(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error)
	SearchConversations(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error)
	SearchMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error)
	CreateDraft(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft) (messagingbroker.DraftMutationResult, error)
	UpdateDraft(ctx context.Context, exposureID messaging.ExposureID, draft messaging.Draft) (messagingbroker.DraftMutationResult, error)
	RequestSendDraft(ctx context.Context, exposureID messaging.ExposureID, draftID messaging.DraftID, newConversation bool) (messagingbroker.RequestSendDraftResult, error)
	MoveMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error)
	MoveConversation(ctx context.Context, exposureID messaging.ExposureID, params protocol.MoveConversationParams) (protocol.ManageMessagesResult, error)
	ArchiveMessages(ctx context.Context, exposureID messaging.ExposureID, params protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error)
	ArchiveConversation(ctx context.Context, exposureID messaging.ExposureID, params protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error)
	ApplyLabels(ctx context.Context, exposureID messaging.ExposureID, params protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error)
	MarkRead(ctx context.Context, exposureID messaging.ExposureID, params protocol.MarkReadParams) (protocol.ManageMessagesResult, error)
}

// Config configures one exposure-bound shim surface.
type Config struct {
	Broker     Broker
	Store      *messagingstore.Store
	ExposureID messaging.ExposureID
}

// Service is an exposure-bound host shim. Every method is scoped to the
// configured exposure and delegates mutations through the broker.
type Service struct {
	broker     Broker
	store      *messagingstore.Store
	exposureID messaging.ExposureID
}

// New creates an exposure-bound shim service.
func New(cfg Config) (*Service, error) {
	if cfg.Broker == nil {
		return nil, fmt.Errorf("messaging shim broker is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("messaging shim store is required")
	}
	if strings.TrimSpace(string(cfg.ExposureID)) == "" {
		return nil, fmt.Errorf("messaging shim exposure_id is required")
	}
	return &Service{
		broker:     cfg.Broker,
		store:      cfg.Store,
		exposureID: cfg.ExposureID,
	}, nil
}

// ListConnections returns the single connection exposed to this shim. Raw auth
// metadata and sensitive credential-like connection metadata are stripped.
func (s *Service) ListConnections(ctx context.Context) ([]messaging.Connection, error) {
	_ = ctx
	exposure, err := s.exposure()
	if err != nil {
		return nil, err
	}
	connection, ok := s.store.GetConnection(exposure.ConnectionID)
	if !ok {
		return nil, fmt.Errorf("messaging connection %s not found", exposure.ConnectionID)
	}
	if _, err := s.authorizeConnection(connection.ID); err != nil {
		return nil, err
	}
	return []messaging.Connection{sanitizeConnection(connection)}, nil
}

// ListIdentities lists local identities on the exposed connection, narrowed by
// any policy identity allow-list.
func (s *Service) ListIdentities(ctx context.Context, connectionID messaging.ConnectionID) ([]messaging.Identity, error) {
	_ = ctx
	policy, err := s.authorizeConnection(connectionID)
	if err != nil {
		return nil, err
	}
	return filterIdentities(s.store.ListConnectionIdentities(connectionID), policy), nil
}

// ListConversations lists cached conversations for the exposed connection.
func (s *Service) ListConversations(ctx context.Context, connectionID messaging.ConnectionID) ([]messaging.Conversation, error) {
	policy, err := s.authorizeRead(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	return filterConversations(s.store.ListConnectionConversations(connectionID), policy), nil
}

// GetConversation returns one cached conversation when it belongs to the
// exposed connection and passes policy narrowing.
func (s *Service) GetConversation(ctx context.Context, connectionID messaging.ConnectionID, conversationID messaging.ConversationID) (messaging.Conversation, bool, error) {
	policy, err := s.authorizeRead(ctx, connectionID)
	if err != nil {
		return messaging.Conversation{}, false, err
	}
	conversation, ok := s.store.GetConversation(conversationID)
	if !ok {
		return messaging.Conversation{}, false, nil
	}
	if conversation.ConnectionID != connectionID || !identityAllowed(policy, conversation.LocalIdentityID) {
		return messaging.Conversation{}, false, nil
	}
	return conversation, true, nil
}

// GetMessages returns cached messages for one exposed conversation.
func (s *Service) GetMessages(ctx context.Context, connectionID messaging.ConnectionID, conversationID messaging.ConversationID) ([]messaging.Message, error) {
	policy, err := s.authorizeRead(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	conversation, ok := s.store.GetConversation(conversationID)
	if !ok {
		return nil, nil
	}
	if conversation.ConnectionID != connectionID || !identityAllowed(policy, conversation.LocalIdentityID) {
		return nil, nil
	}
	return filterMessages(s.store.ListConversationMessages(conversationID), policy), nil
}

// ListContainers returns provider containers on the exposed connection,
// narrowed by any policy container allow-list.
func (s *Service) ListContainers(ctx context.Context, params protocol.ListContainersParams) (protocol.ListContainersResult, error) {
	policy, err := s.authorizeConnection(params.ConnectionID)
	if err != nil {
		return protocol.ListContainersResult{}, err
	}
	result, err := s.broker.ListContainers(ctx, params)
	if err != nil {
		return protocol.ListContainersResult{}, err
	}
	result.Containers = filterContainers(result.Containers, policy)
	return result, nil
}

// SearchIdentities searches local indexed identity/participant data or live
// adapter-backed identity lookup through the broker under this exposure.
func (s *Service) SearchIdentities(ctx context.Context, params protocol.SearchIdentitiesParams) (protocol.SearchIdentitiesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.SearchIdentitiesResult{}, err
	}
	return s.broker.SearchIdentities(ctx, s.exposureID, params)
}

// SearchConversations searches destination/thread metadata through the broker.
func (s *Service) SearchConversations(ctx context.Context, params protocol.SearchConversationsParams) (protocol.SearchConversationsResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.SearchConversationsResult{}, err
	}
	return s.broker.SearchConversations(ctx, s.exposureID, params)
}

// SearchMessages searches cached or remote message content through the broker.
func (s *Service) SearchMessages(ctx context.Context, params protocol.SearchMessagesParams) (protocol.SearchMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.SearchMessagesResult{}, err
	}
	return s.broker.SearchMessages(ctx, s.exposureID, params)
}

// CreateDraft creates a broker-owned draft under this exposure.
func (s *Service) CreateDraft(ctx context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	if _, err := s.authorizeConnection(draft.ConnectionID); err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	if _, ok := s.store.GetDraft(draft.ID); ok {
		return messagingbroker.DraftMutationResult{}, fmt.Errorf("messaging draft %s already exists", draft.ID)
	}
	return s.broker.CreateDraft(ctx, s.exposureID, draft)
}

// UpdateDraft updates a broker-owned draft under this exposure.
func (s *Service) UpdateDraft(ctx context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	if _, err := s.authorizeConnection(draft.ConnectionID); err != nil {
		return messagingbroker.DraftMutationResult{}, err
	}
	existing, ok := s.store.GetDraft(draft.ID)
	if !ok {
		return messagingbroker.DraftMutationResult{}, fmt.Errorf("messaging draft %s not found", draft.ID)
	}
	if existing.ConnectionID != draft.ConnectionID {
		return messagingbroker.DraftMutationResult{}, fmt.Errorf("messaging draft %s belongs to connection %s", draft.ID, existing.ConnectionID)
	}
	return s.broker.UpdateDraft(ctx, s.exposureID, draft)
}

// RequestSend asks the broker to send or approval-gate a draft.
func (s *Service) RequestSend(ctx context.Context, draftID messaging.DraftID, newConversation bool) (messagingbroker.RequestSendDraftResult, error) {
	draft, ok := s.store.GetDraft(draftID)
	if !ok {
		return messagingbroker.RequestSendDraftResult{}, fmt.Errorf("messaging draft %s not found", draftID)
	}
	if _, err := s.authorizeConnection(draft.ConnectionID); err != nil {
		return messagingbroker.RequestSendDraftResult{}, err
	}
	return s.broker.RequestSendDraft(ctx, s.exposureID, draftID, newConversation)
}

// MoveMessages delegates message moves through the broker under this exposure.
func (s *Service) MoveMessages(ctx context.Context, params protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.MoveMessages(ctx, s.exposureID, params)
}

// MoveConversation delegates conversation moves through the broker.
func (s *Service) MoveConversation(ctx context.Context, params protocol.MoveConversationParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.MoveConversation(ctx, s.exposureID, params)
}

// ArchiveMessages delegates message archive operations through the broker.
func (s *Service) ArchiveMessages(ctx context.Context, params protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.ArchiveMessages(ctx, s.exposureID, params)
}

// ArchiveConversation delegates conversation archive operations through the
// broker.
func (s *Service) ArchiveConversation(ctx context.Context, params protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.ArchiveConversation(ctx, s.exposureID, params)
}

// ApplyLabels delegates label mutation through the broker.
func (s *Service) ApplyLabels(ctx context.Context, params protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.ApplyLabels(ctx, s.exposureID, params)
}

// MarkRead delegates read-state mutation through the broker.
func (s *Service) MarkRead(ctx context.Context, params protocol.MarkReadParams) (protocol.ManageMessagesResult, error) {
	if _, err := s.authorizeConnection(params.ConnectionID); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return s.broker.MarkRead(ctx, s.exposureID, params)
}

func (s *Service) exposure() (messaging.Exposure, error) {
	exposure, ok := s.store.GetExposure(s.exposureID)
	if !ok {
		return messaging.Exposure{}, fmt.Errorf("messaging exposure %s not found", s.exposureID)
	}
	if !exposure.Enabled {
		return messaging.Exposure{}, fmt.Errorf("messaging exposure %s is disabled", s.exposureID)
	}
	return exposure, nil
}

func (s *Service) authorizeConnection(connectionID messaging.ConnectionID) (messaging.Policy, error) {
	if strings.TrimSpace(string(connectionID)) == "" {
		return messaging.Policy{}, fmt.Errorf("connection_id is required")
	}
	exposure, err := s.exposure()
	if err != nil {
		return messaging.Policy{}, err
	}
	if exposure.ConnectionID != connectionID {
		return messaging.Policy{}, fmt.Errorf("messaging exposure %s does not allow connection %s", s.exposureID, connectionID)
	}
	effective, err := s.broker.ResolvePolicy(connectionID, s.exposureID)
	if err != nil {
		return messaging.Policy{}, err
	}
	return effective.Policy, nil
}

func (s *Service) authorizeRead(ctx context.Context, connectionID messaging.ConnectionID) (messaging.Policy, error) {
	_ = ctx
	policy, err := s.authorizeConnection(connectionID)
	if err != nil {
		return messaging.Policy{}, err
	}
	decision, err := s.broker.EvaluateReadInbound(connectionID, s.exposureID)
	if err != nil {
		return messaging.Policy{}, err
	}
	if !decision.Allowed() {
		return messaging.Policy{}, fmt.Errorf("read denied by policy: %s", decision.Reason)
	}
	return policy, nil
}

func sanitizeConnection(connection messaging.Connection) messaging.Connection {
	connection.Auth = messaging.AuthInfo{}
	if len(connection.Metadata) == 0 {
		return connection
	}
	metadata := make(map[string]string, len(connection.Metadata))
	for key, value := range connection.Metadata {
		if sensitiveMetadataKey(key) {
			continue
		}
		metadata[key] = value
	}
	connection.Metadata = metadata
	return connection
}

func sensitiveMetadataKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "credential") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "token") ||
		strings.Contains(key, "password")
}

func filterIdentities(identities []messaging.Identity, policy messaging.Policy) []messaging.Identity {
	filtered := make([]messaging.Identity, 0, len(identities))
	for _, identity := range identities {
		if identityAllowed(policy, identity.ID) {
			filtered = append(filtered, identity)
		}
	}
	return filtered
}

func filterConversations(conversations []messaging.Conversation, policy messaging.Policy) []messaging.Conversation {
	filtered := make([]messaging.Conversation, 0, len(conversations))
	for _, conversation := range conversations {
		if identityAllowed(policy, conversation.LocalIdentityID) {
			filtered = append(filtered, conversation)
		}
	}
	return filtered
}

func filterMessages(messages []messaging.Message, policy messaging.Policy) []messaging.Message {
	filtered := make([]messaging.Message, 0, len(messages))
	for _, message := range messages {
		if identityAllowed(policy, message.LocalIdentityID) {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func filterContainers(containers []messaging.Container, policy messaging.Policy) []messaging.Container {
	if len(policy.Rules.AllowedContainerIDs) == 0 {
		return containers
	}
	filtered := make([]messaging.Container, 0, len(containers))
	for _, container := range containers {
		if containerAllowed(policy, container.ID) {
			filtered = append(filtered, container)
		}
	}
	return filtered
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

func containerAllowed(policy messaging.Policy, containerID messaging.ContainerID) bool {
	if len(policy.Rules.AllowedContainerIDs) == 0 {
		return true
	}
	for _, allowed := range policy.Rules.AllowedContainerIDs {
		if allowed == containerID {
			return true
		}
	}
	return false
}
