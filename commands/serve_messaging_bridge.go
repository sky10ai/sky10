package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingshim "github.com/sky10/sky10/pkg/messaging/shim"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
	bridgemessengers "github.com/sky10/sky10/pkg/sandbox/bridge/messengers"
)

const defaultMessagingBridgeEventLimit = 100

func installMessagingBridgeEndpoint(
	server *skyrpc.Server,
	agentRegistry *skyagent.Registry,
	runtime *messagingRuntime,
	sandboxAgents *sandboxAgentSource,
	sandboxManager *skysandbox.Manager,
	logger *slog.Logger,
) error {
	if server == nil {
		return errors.New("messenger bridge: nil rpc server")
	}
	if agentRegistry == nil {
		return errors.New("messenger bridge: nil agent registry")
	}
	if runtime == nil || runtime.broker == nil || runtime.store == nil {
		return errors.New("messenger bridge: messaging runtime unavailable")
	}

	backend := &messagingBridgeBackend{
		broker:        runtime.broker,
		store:         runtime.store,
		agentRegistry: agentRegistry,
		sandboxAgents: sandboxAgents,
		files:         newMessengerBridgeFiles(sandboxAgents),
	}
	forwarder := bridgemessengers.NewForwardingBackend()
	endpointBackend := messagingEndpointBackend(backend, forwarder)
	endpoint := bridgemessengers.NewEndpoint(
		endpointBackend,
		newAgentIdentityResolver(agentRegistry),
		bridge.WithLogger(logging.WithComponent(logger, "sandbox.bridge.messengers")),
	)
	localHandler := endpoint.Handler()
	server.HandleHTTP("GET "+bridgemessengers.EndpointPath, bridgemessengers.HandlerWithHostBridge(localHandler, forwarder))

	if sandboxManager != nil {
		bridgeManager := skysandbox.NewMessengersBridgeManager(backend, logger)
		sandboxManager.AddBridgeConnector(bridgeManager.Connect, bridgeManager.Close)
	}
	return nil
}

func messagingEndpointBackend(local bridgemessengers.Backend, forwarder *bridgemessengers.ForwardingBackend) bridgemessengers.Backend {
	if sandboxGuestMode() {
		return forwarder
	}
	return bridgemessengers.PreferForwardingBackend{
		Forwarder: forwarder,
		Local:     local,
	}
}

type messagingBridgeBackend struct {
	broker        messagingshim.Broker
	store         *messagingstore.Store
	agentRegistry *skyagent.Registry
	sandboxAgents *sandboxAgentSource
	files         *messengerBridgeFiles
}

func (b *messagingBridgeBackend) ListConnections(ctx context.Context, params bridgemessengers.ListConnectionsParams) ([]messaging.Connection, error) {
	out := make([]messaging.Connection, 0)
	seen := make(map[messaging.ConnectionID]struct{})
	for _, connection := range b.store.ListConnections() {
		if params.AdapterID != "" && connection.AdapterID != params.AdapterID {
			continue
		}
		exposure, ok := b.matchingExposure(ctx, params.AgentID, connection.ID)
		if !ok {
			continue
		}
		service, err := b.serviceForExposure(exposure.ID)
		if err != nil {
			return nil, err
		}
		connections, err := service.ListConnections(ctx)
		if err != nil {
			return nil, err
		}
		for _, listed := range connections {
			if _, exists := seen[listed.ID]; exists {
				continue
			}
			seen[listed.ID] = struct{}{}
			out = append(out, listed)
		}
	}
	return out, nil
}

func (b *messagingBridgeBackend) ListConversations(ctx context.Context, params bridgemessengers.ListConversationsParams) ([]messaging.Conversation, error) {
	service, err := b.serviceForAgentConnection(ctx, params.AgentID, params.ConnectionID)
	if err != nil {
		return nil, err
	}
	return service.ListConversations(ctx, params.ConnectionID)
}

func (b *messagingBridgeBackend) ListEvents(ctx context.Context, params bridgemessengers.ListEventsParams) ([]messaging.Event, error) {
	service, err := b.serviceForAgentConnection(ctx, params.AgentID, params.ConnectionID)
	if err != nil {
		return nil, err
	}
	if _, err := service.ListConversations(ctx, params.ConnectionID); err != nil {
		return nil, err
	}
	events := b.store.ListConnectionEvents(params.ConnectionID)
	events = eventsAfter(events, params.AfterEventID)
	limit := params.Limit
	if limit == 0 {
		limit = defaultMessagingBridgeEventLimit
	}
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (b *messagingBridgeBackend) GetMessages(ctx context.Context, params bridgemessengers.GetMessagesParams) ([]messaging.Message, error) {
	service, err := b.serviceForAgentConnection(ctx, params.AgentID, params.ConnectionID)
	if err != nil {
		return nil, err
	}
	messages, err := service.GetMessages(ctx, params.ConnectionID, params.ConversationID)
	if err != nil {
		return nil, err
	}
	if b.files == nil {
		return messages, nil
	}
	return b.files.MaterializeMessages(ctx, params.AgentID, messages)
}

func (b *messagingBridgeBackend) CreateDraft(ctx context.Context, params bridgemessengers.CreateDraftParams) (result messagingbroker.DraftMutationResult, err error) {
	service, err := b.serviceForAgentConnection(ctx, params.AgentID, params.ConnectionID)
	if err != nil {
		return result, err
	}
	localIdentityID, err := b.resolveDraftLocalIdentity(ctx, service, params)
	if err != nil {
		return result, err
	}
	replyToRemoteID := b.resolveReplyToRemoteID(params)
	draftID := params.DraftID
	if draftID == "" {
		draftID = messaging.DraftID("draft/" + uuid.NewString())
	}
	draft := messaging.Draft{
		ID:              draftID,
		ConnectionID:    params.ConnectionID,
		ConversationID:  params.ConversationID,
		LocalIdentityID: localIdentityID,
		ReplyToRemoteID: replyToRemoteID,
		Parts:           append([]messaging.MessagePart(nil), params.Parts...),
		Status:          messaging.DraftStatusPending,
		Metadata:        cloneMessagingBridgeMap(params.Metadata),
	}
	if b.files != nil {
		draft, err = b.files.HostDraftRefs(ctx, params.AgentID, draft)
		if err != nil {
			return result, err
		}
	}
	return service.CreateDraft(ctx, draft)
}

func (b *messagingBridgeBackend) RequestSend(ctx context.Context, params bridgemessengers.RequestSendParams) (messagingbroker.RequestSendDraftResult, error) {
	draft, ok := b.store.GetDraft(params.DraftID)
	if !ok {
		return messagingbroker.RequestSendDraftResult{}, fmt.Errorf("messaging draft %s not found", params.DraftID)
	}
	service, err := b.serviceForAgentConnection(ctx, params.AgentID, draft.ConnectionID)
	if err != nil {
		return messagingbroker.RequestSendDraftResult{}, err
	}
	return service.RequestSend(ctx, params.DraftID, params.NewConversation)
}

func (b *messagingBridgeBackend) serviceForAgentConnection(ctx context.Context, agentID string, connectionID messaging.ConnectionID) (*messagingshim.Service, error) {
	exposure, ok := b.matchingExposure(ctx, agentID, connectionID)
	if !ok {
		return nil, fmt.Errorf("messaging connection %s is not exposed to agent %s", connectionID, agentID)
	}
	return b.serviceForExposure(exposure.ID)
}

func (b *messagingBridgeBackend) serviceForExposure(exposureID messaging.ExposureID) (*messagingshim.Service, error) {
	return messagingshim.New(messagingshim.Config{
		Broker:     b.broker,
		Store:      b.store,
		ExposureID: exposureID,
	})
}

func (b *messagingBridgeBackend) matchingExposure(ctx context.Context, agentID string, connectionID messaging.ConnectionID) (messaging.Exposure, bool) {
	candidates := b.agentSubjectCandidates(ctx, agentID)
	for _, exposure := range b.store.ListConnectionExposures(connectionID) {
		if !exposure.Enabled {
			continue
		}
		if exposureMatchesAgent(exposure, candidates) {
			return exposure, true
		}
	}
	return messaging.Exposure{}, false
}

func (b *messagingBridgeBackend) agentSubjectCandidates(ctx context.Context, agentID string) map[string]struct{} {
	candidates := make(map[string]struct{})
	addAgentSubjectCandidate(candidates, agentID)
	if b.agentRegistry != nil {
		if info := b.agentRegistry.Get(agentID); info != nil {
			addAgentInfoSubjectCandidates(candidates, *info)
		}
	}
	if b.sandboxAgents != nil {
		if target, ok := b.sandboxAgents.Resolve(ctx, agentID); ok {
			addAgentInfoSubjectCandidates(candidates, target.Agent)
			addAgentSubjectCandidate(candidates, target.Sandbox.Slug)
			addAgentSubjectCandidate(candidates, target.Sandbox.Name)
		}
	}
	return candidates
}

func addAgentInfoSubjectCandidates(candidates map[string]struct{}, info skyagent.AgentInfo) {
	addAgentSubjectCandidate(candidates, info.ID)
	addAgentSubjectCandidate(candidates, info.Name)
	addAgentSubjectCandidate(candidates, info.KeyName)
}

func addAgentSubjectCandidate(candidates map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	candidates[value] = struct{}{}
}

func exposureMatchesAgent(exposure messaging.Exposure, candidates map[string]struct{}) bool {
	switch exposure.SubjectKind {
	case messaging.ExposureSubjectKindAgent:
		return subjectMatchesCandidates(exposure.SubjectID, "agent", candidates)
	case messaging.ExposureSubjectKindRuntime:
		return subjectMatchesCandidates(exposure.SubjectID, "runtime", candidates)
	default:
		return false
	}
}

func subjectMatchesCandidates(subject, prefix string, candidates map[string]struct{}) bool {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return false
	}
	if _, ok := candidates[subject]; ok {
		return true
	}
	if trimmed, ok := strings.CutPrefix(subject, prefix+":"); ok {
		_, ok = candidates[strings.TrimSpace(trimmed)]
		return ok
	}
	return false
}

func (b *messagingBridgeBackend) resolveDraftLocalIdentity(ctx context.Context, service *messagingshim.Service, params bridgemessengers.CreateDraftParams) (messaging.IdentityID, error) {
	if params.LocalIdentityID != "" {
		return params.LocalIdentityID, nil
	}
	conversation, ok, err := service.GetConversation(ctx, params.ConnectionID, params.ConversationID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("messaging conversation %s not found", params.ConversationID)
	}
	return conversation.LocalIdentityID, nil
}

func (b *messagingBridgeBackend) resolveReplyToRemoteID(params bridgemessengers.CreateDraftParams) string {
	replyToRemoteID := strings.TrimSpace(params.ReplyToRemoteID)
	if replyToRemoteID != "" || params.ReplyToMessageID == "" {
		return replyToRemoteID
	}
	message, ok := b.store.GetMessage(params.ReplyToMessageID)
	if !ok {
		return ""
	}
	if message.ConnectionID != params.ConnectionID || message.ConversationID != params.ConversationID {
		return ""
	}
	return strings.TrimSpace(message.RemoteID)
}

func eventsAfter(events []messaging.Event, after messaging.EventID) []messaging.Event {
	if after == "" {
		return events
	}
	for idx, event := range events {
		if event.ID == after {
			return events[idx+1:]
		}
	}
	return events
}

func cloneMessagingBridgeMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
