package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
	messagingadapters "github.com/sky10/sky10/pkg/messengers/adapters"
)

// ProcessResolver resolves the process spec to supervise for one adapter id.
type ProcessResolver func(adapterID string) (messagingruntime.ProcessSpec, error)

// Config configures one messaging RPC handler.
type Config struct {
	Broker           *messagingbroker.Broker
	Store            *messagingstore.Store
	ProcessResolver  ProcessResolver
	ExternalAdapters *messagingexternal.Registry
	SecretWriter     SecretWriter
}

// Handler dispatches messaging.* RPC methods.
type Handler struct {
	broker           *messagingbroker.Broker
	store            *messagingstore.Store
	processResolver  ProcessResolver
	externalAdapters *messagingexternal.Registry
	secretWriter     SecretWriter
}

// NewHandler creates a messaging RPC handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		broker:           cfg.Broker,
		store:            cfg.Store,
		processResolver:  cfg.ProcessResolver,
		externalAdapters: cfg.ExternalAdapters,
		secretWriter:     cfg.SecretWriter,
	}
}

// Dispatch implements the repo RPC handler contract.
func (h *Handler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	if !strings.HasPrefix(method, "messaging.") {
		return nil, nil, false
	}
	if h == nil || h.broker == nil || h.store == nil {
		return nil, fmt.Errorf("messaging rpc handler is not configured"), true
	}

	switch method {
	case "messaging.adapters":
		return h.rpcAdapters(), nil, true
	case "messaging.adapter":
		result, err := h.rpcAdapter(params)
		return result, err, true
	case "messaging.connections":
		return h.rpcConnections(), nil, true
	case "messaging.createConnection":
		result, err := h.rpcCreateConnection(ctx, params)
		return result, err, true
	case "messaging.runAdapterAction":
		result, err := h.rpcRunAdapterAction(ctx, params)
		return result, err, true
	case "messaging.connectAdapter":
		result, err := h.rpcConnectAdapter(ctx, params)
		return result, err, true
	case "messaging.connectBuiltin":
		result, err := h.rpcConnectAdapter(ctx, params)
		return result, err, true
	case "messaging.connectConnection":
		result, err := h.rpcConnectConnection(ctx, params)
		return result, err, true
	case "messaging.refreshConnection":
		result, err := h.rpcRefreshConnection(ctx, params)
		return result, err, true
	case "messaging.disableConnection":
		result, err := h.rpcDisableConnection(ctx, params)
		return result, err, true
	case "messaging.deleteConnection":
		result, err := h.rpcDeleteConnection(ctx, params)
		return result, err, true
	case "messaging.pollConnection":
		result, err := h.rpcPollConnection(ctx, params)
		return result, err, true
	case "messaging.listContainers":
		result, err := h.rpcListContainers(ctx, params)
		return result, err, true
	case "messaging.searchIdentities":
		result, err := h.rpcSearchIdentities(ctx, params)
		return result, err, true
	case "messaging.searchConversations":
		result, err := h.rpcSearchConversations(ctx, params)
		return result, err, true
	case "messaging.searchMessages":
		result, err := h.rpcSearchMessages(ctx, params)
		return result, err, true
	case "messaging.moveMessages":
		result, err := h.rpcMoveMessages(ctx, params)
		return result, err, true
	case "messaging.moveConversation":
		result, err := h.rpcMoveConversation(ctx, params)
		return result, err, true
	case "messaging.archiveMessages":
		result, err := h.rpcArchiveMessages(ctx, params)
		return result, err, true
	case "messaging.archiveConversation":
		result, err := h.rpcArchiveConversation(ctx, params)
		return result, err, true
	case "messaging.applyLabels":
		result, err := h.rpcApplyLabels(ctx, params)
		return result, err, true
	case "messaging.markRead":
		result, err := h.rpcMarkRead(ctx, params)
		return result, err, true
	default:
		return nil, fmt.Errorf("unknown method: %s", method), true
	}
}

type adapterInfo struct {
	Name         string                      `json:"name"`
	Summary      string                      `json:"summary"`
	Source       string                      `json:"source"`
	Adapter      *messaging.Adapter          `json:"adapter,omitempty"`
	Settings     []messagingexternal.Setting `json:"settings,omitempty"`
	Actions      []messagingexternal.Action  `json:"actions,omitempty"`
	ManifestPath string                      `json:"manifest_path,omitempty"`
	BundleDir    string                      `json:"bundle_dir,omitempty"`
	Bundled      bool                        `json:"bundled,omitempty"`
}

type adapterParams struct {
	AdapterID messaging.AdapterID `json:"adapter_id"`
}

type connectBuiltinParams struct {
	Connection messaging.Connection `json:"connection"`
}

type connectionParams struct {
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Limit        int                    `json:"limit,omitempty"`
}

type moveMessagesParams struct {
	ExposureID             messaging.ExposureID   `json:"exposure_id,omitempty"`
	ConnectionID           messaging.ConnectionID `json:"connection_id"`
	MessageIDs             []messaging.MessageID  `json:"message_ids"`
	DestinationContainerID messaging.ContainerID  `json:"destination_container_id"`
}

type moveConversationParams struct {
	ExposureID             messaging.ExposureID     `json:"exposure_id,omitempty"`
	ConnectionID           messaging.ConnectionID   `json:"connection_id"`
	ConversationID         messaging.ConversationID `json:"conversation_id"`
	DestinationContainerID messaging.ContainerID    `json:"destination_container_id"`
}

type archiveMessagesParams struct {
	ExposureID   messaging.ExposureID   `json:"exposure_id,omitempty"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	MessageIDs   []messaging.MessageID  `json:"message_ids"`
	ContainerID  messaging.ContainerID  `json:"container_id,omitempty"`
}

type archiveConversationParams struct {
	ExposureID     messaging.ExposureID     `json:"exposure_id,omitempty"`
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id"`
	ContainerID    messaging.ContainerID    `json:"container_id,omitempty"`
}

type applyLabelsParams struct {
	ExposureID     messaging.ExposureID     `json:"exposure_id,omitempty"`
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MessageIDs     []messaging.MessageID    `json:"message_ids,omitempty"`
	Add            []messaging.ContainerID  `json:"add,omitempty"`
	Remove         []messaging.ContainerID  `json:"remove,omitempty"`
}

type markReadParams struct {
	ExposureID     messaging.ExposureID     `json:"exposure_id,omitempty"`
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	MessageIDs     []messaging.MessageID    `json:"message_ids,omitempty"`
	Read           bool                     `json:"read"`
}

type searchIdentitiesParams struct {
	ExposureID   messaging.ExposureID   `json:"exposure_id,omitempty"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Query        string                 `json:"query"`
	Source       protocol.SearchSource  `json:"source,omitempty"`
	protocol.PageRequest
}

type searchConversationsParams struct {
	ExposureID   messaging.ExposureID   `json:"exposure_id,omitempty"`
	ConnectionID messaging.ConnectionID `json:"connection_id"`
	Query        string                 `json:"query"`
	Source       protocol.SearchSource  `json:"source,omitempty"`
	protocol.PageRequest
}

type searchMessagesParams struct {
	ExposureID     messaging.ExposureID     `json:"exposure_id,omitempty"`
	ConnectionID   messaging.ConnectionID   `json:"connection_id"`
	ConversationID messaging.ConversationID `json:"conversation_id,omitempty"`
	ContainerID    messaging.ContainerID    `json:"container_id,omitempty"`
	Query          string                   `json:"query"`
	Source         protocol.SearchSource    `json:"source,omitempty"`
	protocol.PageRequest
}

func (h *Handler) rpcAdapters() map[string]interface{} {
	items := messagingadapters.Builtins()
	external := h.externalAdapters.List()
	adapters := make([]adapterInfo, 0, len(items)+len(external))
	for _, item := range items {
		adapters = append(adapters, adapterInfo{
			Name:    item.Name,
			Summary: item.Summary,
			Source:  "builtin",
			Adapter: &messaging.Adapter{
				ID:          messaging.AdapterID(item.Name),
				DisplayName: item.Name,
				Description: item.Summary,
			},
		})
	}
	for _, item := range external {
		adapter := item.Adapter
		adapters = append(adapters, adapterInfo{
			Name:         string(item.Adapter.ID),
			Summary:      item.Adapter.Description,
			Source:       "external",
			Adapter:      &adapter,
			Settings:     item.Settings,
			Actions:      item.Actions,
			ManifestPath: item.ManifestPath,
			BundleDir:    item.BundleDir,
			Bundled:      item.Bundled,
		})
	}
	return map[string]interface{}{
		"adapters": adapters,
		"count":    len(adapters),
	}
}

func (h *Handler) rpcAdapter(params json.RawMessage) (interface{}, error) {
	var p adapterParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(string(p.AdapterID)) == "" {
		return nil, fmt.Errorf("adapter_id is required")
	}
	for _, item := range messagingadapters.Builtins() {
		if item.Name != string(p.AdapterID) {
			continue
		}
		return adapterInfo{
			Name:    item.Name,
			Summary: item.Summary,
			Source:  "builtin",
			Adapter: &messaging.Adapter{
				ID:          messaging.AdapterID(item.Name),
				DisplayName: item.Name,
				Description: item.Summary,
			},
		}, nil
	}
	if item, ok := h.externalAdapters.Info(p.AdapterID); ok {
		adapter := item.Adapter
		return adapterInfo{
			Name:         string(item.Adapter.ID),
			Summary:      item.Adapter.Description,
			Source:       "external",
			Adapter:      &adapter,
			Settings:     item.Settings,
			Actions:      item.Actions,
			ManifestPath: item.ManifestPath,
			BundleDir:    item.BundleDir,
			Bundled:      item.Bundled,
		}, nil
	}
	return nil, fmt.Errorf("messaging adapter %q is not registered", p.AdapterID)
}

func (h *Handler) rpcConnections() map[string]interface{} {
	connections := h.store.ListConnections()
	return map[string]interface{}{
		"connections": connections,
		"count":       len(connections),
	}
}

func (h *Handler) rpcCreateConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.processResolver == nil {
		return nil, fmt.Errorf("messaging process resolver is not configured")
	}

	var p connectBuiltinParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := p.Connection.Validate(); err != nil {
		return nil, err
	}

	process, err := h.processResolver(string(p.Connection.AdapterID))
	if err != nil {
		return nil, err
	}
	return h.broker.CreateConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: p.Connection,
		Process:    process,
	})
}

func (h *Handler) rpcConnectAdapter(ctx context.Context, params json.RawMessage) (interface{}, error) {
	if h.processResolver == nil {
		return nil, fmt.Errorf("messaging process resolver is not configured")
	}

	var p connectBuiltinParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := p.Connection.Validate(); err != nil {
		return nil, err
	}

	process, err := h.processResolver(string(p.Connection.AdapterID))
	if err != nil {
		return nil, err
	}
	if err := h.broker.UpsertConnection(ctx, messagingbroker.RegisterConnectionParams{
		Connection: p.Connection,
		Process:    process,
	}); err != nil {
		return nil, err
	}
	return h.broker.ConnectConnection(ctx, p.Connection.ID)
}

func (h *Handler) rpcConnectConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.ConnectConnection(ctx, p.ConnectionID)
}

func (h *Handler) rpcRefreshConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.RefreshConnection(ctx, p.ConnectionID)
}

func (h *Handler) rpcDisableConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.DisableConnection(ctx, p.ConnectionID)
}

func (h *Handler) rpcDeleteConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.DeleteConnection(ctx, p.ConnectionID)
}

func (h *Handler) rpcPollConnection(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseConnectionParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.PollConnection(ctx, p.ConnectionID, p.Limit)
}

func (h *Handler) rpcListContainers(ctx context.Context, params json.RawMessage) (interface{}, error) {
	p, err := parseListContainersParams(params)
	if err != nil {
		return nil, err
	}
	return h.broker.ListContainers(ctx, p)
}

func (h *Handler) rpcSearchIdentities(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p searchIdentitiesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return nil, err
	}
	return h.broker.SearchIdentities(ctx, p.ExposureID, protocol.SearchIdentitiesParams{
		ConnectionID: p.ConnectionID,
		Query:        p.Query,
		Source:       p.Source,
		PageRequest:  p.PageRequest,
	})
}

func (h *Handler) rpcSearchConversations(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p searchConversationsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return nil, err
	}
	return h.broker.SearchConversations(ctx, p.ExposureID, protocol.SearchConversationsParams{
		ConnectionID: p.ConnectionID,
		Query:        p.Query,
		Source:       p.Source,
		PageRequest:  p.PageRequest,
	})
}

func (h *Handler) rpcSearchMessages(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p searchMessagesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateSearchParams(p.ConnectionID, p.Query); err != nil {
		return nil, err
	}
	return h.broker.SearchMessages(ctx, p.ExposureID, protocol.SearchMessagesParams{
		ConnectionID:   p.ConnectionID,
		ConversationID: p.ConversationID,
		ContainerID:    p.ContainerID,
		Query:          p.Query,
		Source:         p.Source,
		PageRequest:    p.PageRequest,
	})
}

func (h *Handler) rpcMoveMessages(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p moveMessagesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.MoveMessages(ctx, p.ExposureID, protocol.MoveMessagesParams{
		ConnectionID:           p.ConnectionID,
		MessageIDs:             p.MessageIDs,
		DestinationContainerID: p.DestinationContainerID,
	})
}

func (h *Handler) rpcMoveConversation(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p moveConversationParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.MoveConversation(ctx, p.ExposureID, protocol.MoveConversationParams{
		ConnectionID:           p.ConnectionID,
		ConversationID:         p.ConversationID,
		DestinationContainerID: p.DestinationContainerID,
	})
}

func (h *Handler) rpcArchiveMessages(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p archiveMessagesParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.ArchiveMessages(ctx, p.ExposureID, protocol.ArchiveMessagesParams{
		ConnectionID: p.ConnectionID,
		MessageIDs:   p.MessageIDs,
		ContainerID:  p.ContainerID,
	})
}

func (h *Handler) rpcArchiveConversation(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p archiveConversationParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.ArchiveConversation(ctx, p.ExposureID, protocol.ArchiveConversationParams{
		ConnectionID:   p.ConnectionID,
		ConversationID: p.ConversationID,
		ContainerID:    p.ContainerID,
	})
}

func (h *Handler) rpcApplyLabels(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p applyLabelsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.ApplyLabels(ctx, p.ExposureID, protocol.ApplyLabelsParams{
		ConnectionID:   p.ConnectionID,
		ConversationID: p.ConversationID,
		MessageIDs:     p.MessageIDs,
		Add:            p.Add,
		Remove:         p.Remove,
	})
}

func (h *Handler) rpcMarkRead(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p markReadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return h.broker.MarkRead(ctx, p.ExposureID, protocol.MarkReadParams{
		ConnectionID:   p.ConnectionID,
		ConversationID: p.ConversationID,
		MessageIDs:     p.MessageIDs,
		Read:           p.Read,
	})
}

func parseListContainersParams(params json.RawMessage) (protocol.ListContainersParams, error) {
	var p protocol.ListContainersParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.ListContainersParams{}, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return protocol.ListContainersParams{}, fmt.Errorf("connection_id is required")
	}
	return p, nil
}

func parseConnectionParams(params json.RawMessage) (connectionParams, error) {
	var p connectionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return connectionParams{}, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(string(p.ConnectionID)) == "" {
		return connectionParams{}, fmt.Errorf("connection_id is required")
	}
	return p, nil
}

func validateSearchParams(connectionID messaging.ConnectionID, query string) error {
	if strings.TrimSpace(string(connectionID)) == "" {
		return fmt.Errorf("connection_id is required")
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("query is required")
	}
	return nil
}
