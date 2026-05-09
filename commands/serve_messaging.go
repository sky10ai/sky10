package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	messengerbundles "github.com/sky10/sky10/external/messengers"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skyapps "github.com/sky10/sky10/pkg/apps"
	skyconfig "github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
	messagingrpc "github.com/sky10/sky10/pkg/messaging/rpc"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
	messagingadapters "github.com/sky10/sky10/pkg/messengers/adapters"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysecrets "github.com/sky10/sky10/pkg/secrets"
)

const (
	defaultMessagingKVRoot       = "_sys/messaging"
	defaultMessagingPollInterval = 30 * time.Second
	defaultMessagingPolicySource = "sky10-default-runtime-access"
)

type messagingRuntime struct {
	broker *messagingbroker.Broker
	store  *messagingstore.Store
}

var defaultMessagingRuntimeSubjects = []string{
	"runtime:" + sandboxTemplateOpenClaw,
	"runtime:" + sandboxTemplateHermes,
}

func setupMessaging(
	ctx context.Context,
	server *skyrpc.Server,
	kvStore *kv.Store,
	mailboxStore *agentmailbox.Store,
	secretsStore *skysecrets.Store,
	secretsRPC *skysecrets.RPCHandler,
	approvalTo *agentmailbox.Principal,
	logger *slog.Logger,
) (*messagingRuntime, error) {
	rootDir, err := skyconfig.RootDir()
	if err != nil {
		return nil, fmt.Errorf("messaging root dir: %w", err)
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("messaging executable path: %w", err)
	}

	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(kvStore, defaultMessagingKVRoot))
	if err != nil {
		return nil, fmt.Errorf("creating messaging store: %w", err)
	}
	installMessagingEventFanout(store, server.Emit)

	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:              store,
		RootDir:            filepath.Join(rootDir, "messaging"),
		CredentialResolver: messagingbroker.SecretsResolver{Store: secretsStore},
		ApprovalMailbox:    mailboxStore,
		ApprovalTo:         approvalTo,
	})
	if err != nil {
		return nil, fmt.Errorf("creating messaging broker: %w", err)
	}
	if err := ensureDefaultMessagingRuntimeAccess(ctx, store, logger); err != nil {
		return nil, err
	}

	externalRegistry, err := messagingexternal.NewMaterializedBundledRegistry(
		messengerbundles.FS(),
		".",
		filepath.Join(rootDir, "messaging", "adapters"),
		messagingexternal.ResolveOptions{BunPath: messagingBunPath()},
	)
	if err != nil {
		return nil, fmt.Errorf("discovering external messaging adapters: %w", err)
	}

	processResolver := func(adapterID string) (messagingruntime.ProcessSpec, error) {
		process, err := messagingadapters.BuiltinProcessSpec(executablePath, adapterID)
		if err == nil {
			return process, nil
		}
		if externalRegistry == nil {
			return messagingruntime.ProcessSpec{}, err
		}
		process, externalErr := externalRegistry.ProcessSpec(messaging.AdapterID(adapterID))
		if externalErr != nil {
			return messagingruntime.ProcessSpec{}, externalErr
		}
		return process, nil
	}
	if err := restoreMessagingConnections(ctx, b, store, processResolver, logger); err != nil {
		return nil, err
	}

	server.RegisterHandler(messagingrpc.NewHandler(messagingrpc.Config{
		Broker:           b,
		Store:            store,
		ProcessResolver:  processResolver,
		ExternalAdapters: externalRegistry,
		SecretWriter:     secretsStore,
		BunPath:          messagingBunPath,
		HelperRootDir:    filepath.Join(rootDir, "messaging", "helpers"),
		ConnectionConfigured: func(ctx context.Context, connection messaging.Connection) (messaging.Connection, error) {
			return ensureDefaultMessagingConnectionRuntimeAccess(ctx, store, connection, logger)
		},
	}))

	secretsRPC.AddReferenceResolver(messagingrpc.SecretReferenceResolver{Connections: store})

	go runMessagingPollLoop(ctx, b, store, logging.WithComponent(logger, "messaging.poll"))
	return &messagingRuntime{broker: b, store: store}, nil
}

func resolveMessagingMailboxDecision(ctx context.Context, runtime *messagingRuntime, record agentmailbox.Record, actor agentmailbox.Principal, approved bool) error {
	if runtime == nil || runtime.broker == nil || runtime.store == nil {
		return nil
	}
	if record.Item.Kind != agentmailbox.ItemKindApprovalRequest {
		return nil
	}
	approvalID := messaging.ApprovalID(strings.TrimSpace(record.Item.RequestID))
	if approvalID == "" {
		return nil
	}
	if _, ok := runtime.store.GetApproval(approvalID); !ok {
		return nil
	}
	actorID := strings.TrimSpace(actor.ID)
	if actorID == "" {
		actorID = "human"
	}
	if !approved {
		if _, err := runtime.broker.RejectDraftSend(ctx, approvalID, actorID, "rejected from mailbox"); err != nil {
			return fmt.Errorf("reject messaging approval %s: %w", approvalID, err)
		}
		return nil
	}
	result, err := runtime.broker.ApproveDraftSend(ctx, approvalID, actorID)
	if err != nil {
		return fmt.Errorf("approve messaging approval %s: %w", approvalID, err)
	}
	if result.Draft.ID == "" {
		return nil
	}
	if _, err := runtime.broker.RequestSendDraft(ctx, result.Approval.ExposureID, result.Draft.ID, false); err != nil {
		return fmt.Errorf("send approved messaging draft %s: %w", result.Draft.ID, err)
	}
	return nil
}

func ensureDefaultMessagingRuntimeAccess(ctx context.Context, store *messagingstore.Store, logger *slog.Logger) error {
	if store == nil {
		return fmt.Errorf("messaging runtime access requires store")
	}
	for _, connection := range store.ListConnections() {
		if _, err := ensureDefaultMessagingConnectionRuntimeAccess(ctx, store, connection, logger); err != nil {
			return err
		}
	}
	return nil
}

func ensureDefaultMessagingConnectionRuntimeAccess(ctx context.Context, store *messagingstore.Store, connection messaging.Connection, logger *slog.Logger) (messaging.Connection, error) {
	if store == nil {
		return messaging.Connection{}, fmt.Errorf("messaging runtime access requires store")
	}
	if connection.Status == messaging.ConnectionStatusDisabled {
		return connection, nil
	}
	policyID := connection.DefaultPolicyID
	if policyID == "" {
		policyID = defaultMessagingRuntimePolicyID(connection.ID)
		connection.DefaultPolicyID = policyID
		if err := store.PutConnection(ctx, connection); err != nil {
			return messaging.Connection{}, fmt.Errorf("configure default messaging policy for %s: %w", connection.ID, err)
		}
	}
	policy, ok := store.GetPolicy(policyID)
	if !ok || policy.Metadata["source"] == defaultMessagingPolicySource {
		if err := store.PutPolicy(ctx, defaultMessagingRuntimePolicy(policyID)); err != nil {
			return messaging.Connection{}, fmt.Errorf("store default messaging policy for %s: %w", connection.ID, err)
		}
	}
	for _, subjectID := range defaultMessagingRuntimeSubjects {
		exposureID := defaultMessagingRuntimeExposureID(connection.ID, subjectID)
		if _, ok := store.GetExposure(exposureID); ok {
			continue
		}
		if err := store.PutExposure(ctx, messaging.Exposure{
			ID:           exposureID,
			ConnectionID: connection.ID,
			SubjectID:    subjectID,
			SubjectKind:  messaging.ExposureSubjectKindRuntime,
			PolicyID:     policyID,
			Enabled:      true,
		}); err != nil {
			return messaging.Connection{}, fmt.Errorf("store default messaging exposure for %s to %s: %w", connection.ID, subjectID, err)
		}
		if logger != nil {
			logger.Info("created default messaging runtime exposure", "connection_id", connection.ID, "subject", subjectID)
		}
	}
	return connection, nil
}

func defaultMessagingRuntimePolicyID(connectionID messaging.ConnectionID) messaging.PolicyID {
	return messaging.PolicyID("policy/messaging/default-runtime/" + string(connectionID))
}

func defaultMessagingRuntimeExposureID(connectionID messaging.ConnectionID, subjectID string) messaging.ExposureID {
	subjectID = strings.TrimPrefix(strings.TrimSpace(subjectID), "runtime:")
	if subjectID == "" {
		subjectID = "runtime"
	}
	return messaging.ExposureID("exposure/messaging/default-runtime/" + string(connectionID) + "/" + subjectID)
}

func defaultMessagingRuntimePolicy(policyID messaging.PolicyID) messaging.Policy {
	return messaging.Policy{
		ID:   policyID,
		Name: "Default runtime messaging access",
		Rules: messaging.PolicyRules{
			ReadInbound:           true,
			CreateDrafts:          true,
			SendMessages:          true,
			RequireApproval:       true,
			ReplyOnly:             true,
			AllowNewConversations: false,
			AllowAttachments:      false,
			MarkRead:              false,
			ManageMessages:        false,
			SearchIdentities:      true,
			SearchConversations:   true,
			SearchMessages:        true,
		},
		Metadata: map[string]string{
			"source": defaultMessagingPolicySource,
		},
	}
}

func messagingBunPath() string {
	status, err := skyapps.StatusFor(skyapps.AppBun)
	if err == nil && status != nil && strings.TrimSpace(status.ActivePath) != "" {
		return status.ActivePath
	}
	return "bun"
}

func installMessagingEventFanout(store *messagingstore.Store, emit func(string, interface{})) {
	if store == nil || emit == nil {
		return
	}
	store.AddEventObserver(func(event messaging.Event) {
		emit(messaging.FanoutEventName, event)
	})
}

func restoreMessagingConnections(
	ctx context.Context,
	b *messagingbroker.Broker,
	store *messagingstore.Store,
	processResolver messagingrpc.ProcessResolver,
	logger *slog.Logger,
) error {
	if b == nil || store == nil {
		return fmt.Errorf("messaging restore requires broker and store")
	}
	for _, connection := range store.ListConnections() {
		if connection.Status == messaging.ConnectionStatusDisabled {
			logger.Info("skipping disabled messaging connection restore", "connection_id", connection.ID, "adapter_id", connection.AdapterID)
			continue
		}
		process, err := processResolver(string(connection.AdapterID))
		if err != nil {
			logger.Warn("skipping messaging connection restore", "connection_id", connection.ID, "adapter_id", connection.AdapterID, "error", err)
			continue
		}
		if err := b.UpsertConnection(ctx, messagingbroker.RegisterConnectionParams{
			Connection: connection,
			Process:    process,
		}); err != nil {
			logger.Warn("failed to restore messaging connection", "connection_id", connection.ID, "adapter_id", connection.AdapterID, "error", err)
			continue
		}
		if _, err := b.ConnectConnection(ctx, connection.ID); err != nil {
			logger.Warn("failed to reconnect messaging connection", "connection_id", connection.ID, "adapter_id", connection.AdapterID, "error", err)
			continue
		}
		logger.Info("restored messaging connection", "connection_id", connection.ID, "adapter_id", connection.AdapterID)
	}
	return nil
}

func runMessagingPollLoop(ctx context.Context, b *messagingbroker.Broker, store *messagingstore.Store, logger *slog.Logger) {
	ticker := time.NewTicker(defaultMessagingPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, connection := range store.ListConnections() {
				if connection.Status != messaging.ConnectionStatusConnected {
					continue
				}
				limit := connectionPollLimit(connection)
				result, err := b.PollConnection(ctx, connection.ID, limit)
				if err != nil {
					logger.Warn("messaging poll failed", "connection_id", connection.ID, "adapter_id", connection.AdapterID, "error", err)
					continue
				}
				if len(result.Events) > 0 {
					logger.Info("messaging poll persisted events",
						"connection_id", connection.ID,
						"adapter_id", connection.AdapterID,
						"events", len(result.Events),
						"messages", len(result.Messages),
					)
				}
			}
		}
	}
}

func connectionPollLimit(connection messaging.Connection) int {
	if connection.Metadata == nil {
		return 100
	}
	value := strings.TrimSpace(connection.Metadata["poll_limit"])
	if value == "" {
		return 100
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 100
	}
	return limit
}
