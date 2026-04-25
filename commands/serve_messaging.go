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

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skyconfig "github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
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
)

func setupMessaging(
	ctx context.Context,
	server *skyrpc.Server,
	kvStore *kv.Store,
	mailboxStore *agentmailbox.Store,
	secretsStore *skysecrets.Store,
	logger *slog.Logger,
) error {
	rootDir, err := skyconfig.RootDir()
	if err != nil {
		return fmt.Errorf("messaging root dir: %w", err)
	}
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("messaging executable path: %w", err)
	}

	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(kvStore, defaultMessagingKVRoot))
	if err != nil {
		return fmt.Errorf("creating messaging store: %w", err)
	}
	installMessagingEventFanout(store, server.Emit)

	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:              store,
		RootDir:            filepath.Join(rootDir, "messaging"),
		CredentialResolver: messagingbroker.SecretsResolver{Store: secretsStore},
		ApprovalMailbox:    mailboxStore,
	})
	if err != nil {
		return fmt.Errorf("creating messaging broker: %w", err)
	}

	processResolver := func(adapterID string) (messagingruntime.ProcessSpec, error) {
		return messagingadapters.BuiltinProcessSpec(executablePath, adapterID)
	}
	if err := restoreMessagingConnections(ctx, b, store, processResolver, logger); err != nil {
		return err
	}

	server.RegisterHandler(messagingrpc.NewHandler(messagingrpc.Config{
		Broker:          b,
		Store:           store,
		ProcessResolver: processResolver,
	}))

	go runMessagingPollLoop(ctx, b, store, logging.WithComponent(logger, "messaging.poll"))
	return nil
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
