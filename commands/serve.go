package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/peer"
	skyagent "github.com/sky10/sky10/pkg/agent"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skyjoin "github.com/sky10/sky10/pkg/join"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
	"github.com/sky10/sky10/pkg/logging"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
	"github.com/sky10/sky10/pkg/secrets"
	skyupdate "github.com/sky10/sky10/pkg/update"
	skywallet "github.com/sky10/sky10/pkg/wallet"
	"github.com/spf13/cobra"
)

// ServeCmd returns the top-level `sky10 serve` command.
func ServeCmd() *cobra.Command {
	var linkListenAddrs []string
	var linkBootstrapPeers []string
	var linkRelayPeers []string
	var allowNoLinkRelay bool
	var noDefaultBootstrap bool
	var relayOverrides []string
	var noDefaultRelays bool
	var fsPollSeconds int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the sky10 daemon (RPC server for fs, kv, and more)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
				<-sigCh
				cancel()
			}()

			format, err := logging.ParseFormat(os.Getenv("SKY10_LOG_FORMAT"))
			if err != nil {
				return err
			}
			level, err := logging.ParseLevel(os.Getenv("SKY10_LOG_LEVEL"))
			if err != nil {
				return err
			}
			logRuntime, err := logging.InstallDefault(logging.Config{
				Level:       level,
				Format:      format,
				Stderr:      true,
				Service:     "sky10",
				Version:     Version,
				BufferLines: logging.DefaultBufferLines,
			})
			if err != nil {
				return fmt.Errorf("installing logger: %w", err)
			}
			defer logRuntime.Close()

			logger := logging.WithComponent(logRuntime.Logger, "commands.serve")

			if err := skyfs.KillExistingDaemon(); err != nil {
				logger.Info("daemon: " + err.Error())
			}

			sockPath, _ := cmd.Flags().GetString("socket")
			if sockPath == "" {
				sockPath = skyfs.DaemonSocketPath()
			}
			cfgDir, _ := config.Dir()
			relayBootstrapPath := filepath.Join(cfgDir, "link-relays.json")

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			relays := resolvedRelays(cfg, relayOverrides, noDefaultRelays)
			managedLiveRelays := resolvedManagedLiveRelays(cfg, linkRelayPeers)
			nostrRelayTracker := link.NewNostrRelayTracker(relays)
			linkCfg, relayBootstrapSnapshot, err := resolvedLinkConfig(cfg, linkListenAddrs, linkBootstrapPeers, linkRelayPeers, noDefaultBootstrap, relayBootstrapPath)
			if err != nil {
				return err
			}
			if warning := managedLiveRelayWarning(managedLiveRelays, linkCfg.RelayPeers); warning != "" {
				if len(linkCfg.RelayPeers) > 0 {
					logger.Warn(warning, "cached_peers", len(linkCfg.RelayPeers))
				} else {
					logger.Warn(warning)
				}
			}
			if len(linkCfg.RelayPeers) > 0 {
				if err := link.SaveRelayBootstrapState(relayBootstrapPath, linkCfg.RelayPeers, relayBootstrapSnapshot); err != nil {
					logger.Warn("failed to persist live relay bootstrap cache", "error", err)
				} else if _, snapshot, err := link.LoadRelayBootstrapPeers(relayBootstrapPath); err == nil {
					relayBootstrapSnapshot = snapshot
				}
			}
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}
			type logSetter interface{ SetLogger(*slog.Logger) }
			if ls, ok := backend.(logSetter); ok {
				ls.SetLogger(logRuntime.Logger)
			}

			if backend != nil {
				if _, err := backend.List(ctx, "ops/"); err != nil {
					logger.Warn("S3 credential check failed (will retry)", "error", err)
				}
			}

			idStore, err := skyid.NewStore()
			if err != nil {
				return err
			}
			bundle, err := skyid.SyncIdentity(ctx, idStore, backend, skyfs.GetDeviceName())
			if err != nil {
				return err
			}

			store := skyfs.NewWithDevice(backend, bundle.Identity, bundle.DeviceID())
			store.SetDevicePubKey(bundle.DevicePubKeyHex())
			store.SetClient("cli/" + cmd.Root().Version)

			if backend != nil {
				skyfs.RegisterDevice(ctx, backend, bundle.DeviceID(), bundle.DevicePubKeyHex(), skyfs.GetDeviceName(), cmd.Root().Version)
			}
			skyfs.HandleDumpSignal(logRuntime.Logger)

			hasStorage := backend != nil
			if !hasStorage {
				logger.Info("starting in P2P-only mode (no S3 storage configured)")
			}

			if err := skyfs.KillExistingDaemon(); err != nil {
				logger.Warn("killed stale daemon", "error", err)
			}
			if err := skyfs.WritePIDFile(); err != nil {
				return fmt.Errorf("writing PID file: %w", err)
			}
			defer skyfs.RemovePIDFile()

			server := skyrpc.NewServer(sockPath, cmd.Root().Version, logRuntime.Logger)
			fsHandler := skyfs.NewFSHandler(store, server, filepath.Join(cfgDir, "drives.json"), logRuntime.Logger, logRuntime.Buffer)
			fsHandler.SetDrivePollSeconds(fsPollSeconds)
			server.RegisterHandler(fsHandler)
			server.HandleHTTP("POST /upload", fsHandler.HandleUpload)
			server.HandleHTTP("GET /download", fsHandler.HandleDownload)

			kvStore := kv.New(backend, bundle.Identity, kv.Config{
				Namespace:          "default",
				DeviceID:           bundle.DeviceID(),
				ActorID:            bundle.DevicePubKeyHex(),
				RequireExistingKey: backend == nil && bundle.Manifest != nil && len(bundle.Manifest.Devices) > 1,
				ExpectedPeers:      expectedPrivateNetworkPeers(bundle),
			}, logRuntime.Logger)
			server.RegisterHandler(kv.NewRPCHandler(kvStore))
			kvRunErr := make(chan error, 1)
			go func() {
				kvRunErr <- kvStore.Run(ctx)
			}()
			mailboxStore, err := agentmailbox.NewStore(ctx, agentmailbox.NewScopedKVBackend(kvStore, "mailbox"))
			if err != nil {
				return fmt.Errorf("creating mailbox store: %w", err)
			}
			go func() {
				if err := mailboxStore.RunLifecycle(ctx, agentmailbox.DefaultLifecycleSweepInterval()); err != nil && ctx.Err() == nil {
					logger.Warn("mailbox lifecycle failed", "error", err)
				}
			}()

			secretsStore := secrets.New(backend, bundle, secrets.Config{BootstrapKV: kvStore}, nil)
			server.RegisterHandler(secrets.NewRPCHandler(secretsStore))
			secretsRunErr := make(chan error, 1)
			go func() {
				secretsRunErr <- secretsStore.Run(ctx)
			}()
			reconcileTrustedSecrets := func(reason string) {
				reconcileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				count, err := secretsStore.ReconcileTrustedScope(reconcileCtx)
				if err != nil {
					logger.Warn("failed to reconcile trusted secrets", "reason", reason, "error", err)
					return
				}
				if count > 0 {
					logger.Info("reconciled trusted secrets", "reason", reason, "count", count)
				}
			}

			// Skylink P2P node — network mode enables DHT, relay, and external peers.
			linkNode, err := link.New(bundle, linkCfg, logRuntime.Logger)
			if err != nil {
				return fmt.Errorf("creating link node: %w", err)
			}
			runtimeHealth := link.NewRuntimeHealthTracker()
			liveRelayTracker := link.NewLiveRelayTracker(linkCfg.RelayPeers, relayBootstrapSnapshot)
			rememberLiveRelays := func() {
				if linkNode == nil || linkNode.Host() == nil {
					return
				}
				observed, snapshot, changed := liveRelayTracker.ObserveHostAddrs(linkNode.Host().Addrs())
				if !changed || len(observed) == 0 {
					return
				}
				if err := link.SaveRelayBootstrapState(relayBootstrapPath, observed, snapshot); err != nil {
					logger.Warn("failed to refresh live relay bootstrap cache", "error", err)
					return
				}
			}
			linkNode.SetLiveRelayPreferenceProvider(func() link.LiveRelayPreference {
				if host := linkNode.Host(); host != nil {
					return liveRelayTracker.Preference(host.Addrs())
				}
				return liveRelayTracker.Preference(nil)
			})
			var nostrDiscovery *link.NostrDiscovery
			if len(relays) > 0 {
				nostrDiscovery = link.NewNostrDiscoveryWithTracker(relays, logRuntime.Logger, nostrRelayTracker)
			}
			var resolverOpts []link.ResolverOption
			if backend != nil {
				resolverOpts = append(resolverOpts, link.WithBackend(backend))
			}
			if nostrDiscovery != nil {
				resolverOpts = append(resolverOpts, link.WithNostrDiscovery(
					nostrDiscovery,
				))
			}
			linkResolver := link.NewResolver(linkNode, resolverOpts...)
			link.RegisterPrivateNetworkHandlers(linkNode)
			server.RegisterHandler(link.NewRPCHandler(
				linkNode,
				linkResolver,
				link.WithRuntimeHealthTracker(runtimeHealth),
				link.WithLiveRelayHealthProvider(func() link.LiveRelayHealth {
					if host := linkNode.Host(); host != nil {
						return liveRelayTracker.Health(host.Addrs())
					}
					return liveRelayTracker.Health(nil)
				}),
				link.WithMailboxHealthProvider(func() link.MailboxHealth {
					stats := mailboxStore.Stats()
					return link.MailboxHealth{
						Queued:              stats.Queued,
						Failed:              stats.Failed,
						HandedOff:           stats.HandedOff,
						PendingPrivate:      stats.PendingPrivate,
						PendingSky10Network: stats.PendingSky10Network,
						LastHandoffAt:       optionalTime(stats.LastHandoffAt),
						LastDeliveredAt:     optionalTime(stats.LastDeliveredAt),
						LastFailureAt:       optionalTime(stats.LastFailureAt),
					}
				}),
				link.WithRelayHealthProvider(nostrRelayTracker.Snapshot),
				link.WithNostrCoordinationProvider(nostrRelayTracker.CoordinationSnapshot),
			))
			var triggerPrivateNetwork func(reason, detail string)
			var kvSync *kv.P2PSync
			var fsSync *skyfs.P2PSync
			var agentRouter *skyagent.Router
			identityRPC := skyid.NewRPCHandler(bundle)
			server.RegisterHandler(identityRPC)
			updateRPC := skyupdate.NewRPCHandler(Version, server.Emit)
			updateRPC.SetRestartHandler(func() error {
				os.Exit(75)
				return nil
			})
			server.RegisterHandler(updateRPC)
			sandboxManager, err := skysandbox.NewManager(server.Emit, logRuntime.Logger)
			if err != nil {
				return fmt.Errorf("creating sandbox manager: %w", err)
			}
			sandboxManager.SetHostIdentityProvider(func(context.Context) (string, error) {
				return bundle.Address(), nil
			})
			sandboxManager.SetIdentityInviteIssuer(func(ctx context.Context) (*skysandbox.IdentityInvite, error) {
				code, err := createIdentityInvite(ctx, backend, bundle, linkNode, relays, skyid.InviteOptions{Mode: skyid.InviteModeP2P})
				if err != nil {
					return nil, err
				}
				return &skysandbox.IdentityInvite{
					HostIdentity: bundle.Address(),
					Code:         code,
				}, nil
			})
			sandboxManager.SetOpenClawSharedEnvResolver(func(ctx context.Context) (map[string]string, error) {
				return skysandbox.ResolveOpenClawProviderEnv(ctx, func(ctx context.Context, idOrName string) ([]byte, error) {
					secret, err := secretsStore.Get(idOrName, secrets.Requester{Type: secrets.RequesterOwner})
					if err != nil {
						if errors.Is(err, secrets.ErrNotFound) {
							return nil, skysandbox.ErrProviderSecretNotFound
						}
						return nil, err
					}
					return secret.Payload, nil
				})
			})
			sandboxManager.SetHermesSharedEnvResolver(func(ctx context.Context) (map[string]string, error) {
				return skysandbox.ResolveHermesProviderEnv(ctx, func(ctx context.Context, idOrName string) ([]byte, error) {
					secret, err := secretsStore.Get(idOrName, secrets.Requester{Type: secrets.RequesterOwner})
					if err != nil {
						if errors.Is(err, secrets.ErrNotFound) {
							return nil, skysandbox.ErrProviderSecretNotFound
						}
						return nil, err
					}
					return secret.Payload, nil
				})
			})
			server.RegisterHandler(skysandbox.NewRPCHandler(sandboxManager))
			server.HandleHTTP("GET /rpc/sandboxes/{slug}/terminal", sandboxManager.HandleTerminal)

			var privateNetworkMu sync.Mutex
			privateNetworkManager := link.NewManager(
				logging.WithComponent(logRuntime.Logger, "link.manager"),
				func(runCtx context.Context, batch link.ConvergenceBatch) error {
					privateNetworkMu.Lock()
					defer privateNetworkMu.Unlock()

					var errs []error

					membership, source, err := linkResolver.ResolveMembership(runCtx, bundle.Address())
					if err != nil {
						logger.Warn("private-network membership resolve failed", "error", err)
						errs = append(errs, fmt.Errorf("resolve membership: %w", err))
					} else if source != "local" {
						manifest, err := membership.ToManifest(bundle.Identity)
						if err != nil {
							logger.Warn("private-network membership cache rebuild failed", "error", err)
							errs = append(errs, fmt.Errorf("membership cache rebuild: %w", err))
						} else if !manifest.HasDevice(bundle.Device.PublicKey) {
							err := fmt.Errorf("resolved membership missing current device")
							logger.Warn("resolved membership missing current device; keeping local cache",
								"identity", bundle.Address(),
							)
							errs = append(errs, err)
						} else {
							bundle.Manifest = manifest
							if err := idStore.Save(bundle); err != nil {
								logger.Warn("saving refreshed private-network cache failed", "error", err)
								errs = append(errs, fmt.Errorf("save refreshed membership: %w", err))
							}
						}
					}

					if err := linkNode.PublishRecord(runCtx); err != nil {
						runtimeHealth.RecordPublish("dht", err)
						logger.Warn("failed to publish private-network records to DHT", "error", err)
						errs = append(errs, fmt.Errorf("publish DHT presence: %w", err))
					} else {
						runtimeHealth.RecordPublish("dht", nil)
						logger.Info("published private-network records to DHT")
					}

					if nostrDiscovery != nil {
						membershipRecord, err := linkNode.CurrentMembershipRecord()
						if err != nil {
							logger.Warn("building private-network membership record failed", "error", err)
							errs = append(errs, fmt.Errorf("build membership record: %w", err))
						} else if outcome, err := nostrDiscovery.PublishMembership(runCtx, bundle.Identity, membershipRecord); err != nil {
							runtimeHealth.RecordNostrPublish("nostr_membership", outcome, err)
							logger.Warn("failed to publish private-network membership to Nostr", "error", err)
							errs = append(errs, fmt.Errorf("publish nostr membership: %w", err))
						} else {
							runtimeHealth.RecordNostrPublish("nostr_membership", outcome, nil)
							if outcome.Degraded {
								logger.Warn("published private-network membership to Nostr below quorum",
									"successes", outcome.Successes,
									"quorum", outcome.Quorum,
								)
							}
						}

						presenceRecord, err := linkNode.CurrentPresenceRecordForPublish(runCtx, 0)
						if err != nil {
							logger.Warn("building private-network presence record failed", "error", err)
							errs = append(errs, fmt.Errorf("build presence record: %w", err))
						} else if outcome, err := nostrDiscovery.PublishPresence(runCtx, bundle.Device, presenceRecord); err != nil {
							runtimeHealth.RecordNostrPublish("nostr_presence", outcome, err)
							logger.Warn("failed to publish private-network presence to Nostr", "error", err)
							errs = append(errs, fmt.Errorf("publish nostr presence: %w", err))
						} else {
							runtimeHealth.RecordNostrPublish("nostr_presence", outcome, nil)
							if outcome.Degraded {
								logger.Warn("published private-network presence to Nostr below quorum",
									"successes", outcome.Successes,
									"quorum", outcome.Quorum,
								)
							}
						}
					}

					if err := link.AutoConnect(runCtx, linkResolver); err != nil {
						logger.Warn("private-network auto-connect failed", "error", err)
						errs = append(errs, fmt.Errorf("auto-connect: %w", err))
					}
					if kvSync != nil {
						go kvSync.PushToAll(context.Background())
					}
					if fsSync != nil {
						go fsSync.PushToAll(context.Background())
					}
					if agentRouter != nil {
						go agentRouter.DrainOutbox(context.Background(), "")
						go agentRouter.DrainNetworkOutbox(context.Background(), "")
					}

					return errors.Join(errs...)
				},
				link.WithManagerSafetySweep(2*time.Minute),
			)
			go func() {
				if err := privateNetworkManager.Run(ctx); err != nil && ctx.Err() == nil {
					logger.Warn("private-network manager stopped", "error", err)
				}
			}()
			triggerPrivateNetwork = privateNetworkManager.Trigger
			if nostrDiscovery != nil {
				go func() {
					err := nostrDiscovery.RunIdentitySubscription(ctx, bundle.Address(), func(update link.NostrDiscoveryUpdate) {
						detail := update.Identity
						if update.DevicePubKey != "" {
							detail = update.DevicePubKey
						}
						runtimeHealth.RecordCoordination("nostr_"+update.Type, "ok", detail)
						if triggerPrivateNetwork != nil {
							triggerPrivateNetwork("nostr_"+update.Type, detail)
						}
					})
					if err != nil && ctx.Err() == nil {
						logger.Warn("nostr identity subscription stopped", "error", err)
					}
				}()
			}
			configureIdentityRPCHandler(identityRPC, bundle, idStore, backend, linkNode, relays, func() {
				reconcileTrustedSecrets("device_removed")
				triggerPrivateNetwork("device_removed", "")
			})

			// Agent registry — local agent registration and message routing.
			agentRegistry := skyagent.NewRegistry(bundle.DeviceID(), skyfs.GetDeviceName(), logRuntime.Logger)
			agentRouter = skyagent.NewRouter(agentRegistry, linkNode, server.Emit, bundle.DeviceID(), logRuntime.Logger)
			agentRouter.SetMailbox(mailboxStore)
			agentRouter.SetResolver(linkResolver)
			mailboxRetryManager := link.NewManager(
				logging.WithComponent(logRuntime.Logger, "agent.mailbox_retry"),
				func(runCtx context.Context, batch link.ConvergenceBatch) error {
					return runMailboxRetryBatch(runCtx, agentRouter, mailboxStore, batch)
				},
			)
			go func() {
				if err := mailboxRetryManager.Run(ctx); err != nil && ctx.Err() == nil {
					logger.Warn("mailbox retry manager stopped", "error", err)
				}
			}()
			agentRouter.SetMailboxObserver(func(action string, record agentmailbox.Record) {
				runtimeHealth.RecordMailbox(action, string(record.State), record.Item.ID)
				reason, detail, ok := mailboxRetryTrigger(action, record)
				if ok {
					mailboxRetryManager.Trigger(reason, detail)
				}
			})
			if len(relays) > 0 {
				agentRouter.SetNetworkRelay(agentmailbox.NewRelayDropbox(
					bundle.Identity,
					agentmailbox.NewNostrRelayTransportWithTracker(relays, logRuntime.Logger, nostrRelayTracker),
					logRuntime.Logger,
				))
				publicQueue := agentmailbox.NewPublicQueue(
					bundle.Identity,
					agentmailbox.NewNostrQueueTransportWithTracker(relays, logRuntime.Logger, nostrRelayTracker),
					logRuntime.Logger,
				)
				agentRouter.SetNetworkQueue(publicQueue)
				go func() {
					if err := publicQueue.RunSubscription(ctx); err != nil && ctx.Err() == nil {
						logger.Warn("public queue subscriber stopped", "error", err)
					}
				}()
			}
			agentRPC := skyagent.NewRPCHandler(agentRegistry, bundle.Identity, server.Emit)
			agentRPC.SetRouter(agentRouter)
			agentRPC.SetMailbox(mailboxStore)
			server.RegisterHandler(agentRPC)
			skyagent.RegisterLinkHandlers(linkNode, agentRegistry, server.Emit, agentRouter)
			agentRPC.SetPeerNotifier(func(ctx context.Context, topic string) {
				linkNode.NotifyOwn(ctx, topic)
			})
			if len(relays) > 0 {
				relaySubscriptionLabel := "mailbox:" + bundle.Address()
				go func() {
					if err := agentRouter.RunNetworkRelaySubscriber(ctx); err != nil && ctx.Err() == nil {
						logger.Warn("network relay subscriber stopped", "error", err)
					}
				}()
				go agentRouter.RunAdaptiveNetworkRelayPoller(ctx, func() time.Duration {
					return nostrRelayTracker.AdaptivePollInterval(
						relaySubscriptionLabel,
						5*agentmailbox.DefaultRelayPollInterval(),
						2*agentmailbox.DefaultRelayPollInterval(),
						agentmailbox.DefaultRelayPollInterval(),
					)
				})
			}
			// TODO: re-enable health checker once agents reliably heartbeat.
			// go skyagent.NewHealthChecker(agentRegistry, server.Emit, nil).Run(ctx)

			server.RegisterHandler(skyapps.NewRPCHandler(server.Emit))

			// Wallet handler — opt-in, only active when ows is installed.
			walletClient := skywallet.NewClient()
			if walletClient != nil {
				logger.Info("wallet: OWS detected, enabling wallet RPC")
			}
			server.RegisterHandler(skywallet.NewRPCHandler(walletClient, server.Emit))

			// Wire sync notifications: KV changes notify own devices.
			kvStore.SetNotifier(func(ns string) {
				linkNode.NotifyOwn(ctx, "kv:"+ns)
			})
			secretsStore.SetNotifier(func(ns string) {
				linkNode.NotifyOwn(ctx, "kv:"+ns)
			})
			linkNode.OnSyncNotify(func(from peer.ID, topic string) {
				switch {
				case topic == "kv:default":
					kvStore.Poke()
				case topic == "kv:secrets":
					secretsStore.Poke()
				case strings.HasPrefix(topic, "agent:connected:"):
					deviceID := strings.TrimPrefix(topic, "agent:connected:")
					server.Emit("agent:connected", map[string]string{"from": from.String(), "device_id": deviceID})
					if triggerPrivateNetwork != nil {
						triggerPrivateNetwork("agent_connected", deviceID)
					}
				case strings.HasPrefix(topic, "agent:disconnected:"):
					deviceID := strings.TrimPrefix(topic, "agent:disconnected:")
					server.Emit("agent:disconnected", map[string]string{"from": from.String(), "device_id": deviceID})
					if triggerPrivateNetwork != nil {
						triggerPrivateNetwork("agent_disconnected", deviceID)
					}
				case strings.HasPrefix(topic, "agent:"):
					server.Emit(topic, map[string]string{"from": from.String()})
					if triggerPrivateNetwork != nil {
						triggerPrivateNetwork("agent_update", topic)
					}
				}
			})

			// In P2P-only mode, wire direct KV snapshot sync over libp2p.
			kvSync = kv.NewP2PSync(kvStore, linkNode, bundle.Identity, nil)
			kvSync.AddStore(secretsStore.Transport())
			kvStore.SetP2PSync(kvSync)
			secretsStore.SetP2PSync(kvSync)
			fsSync = skyfs.NewP2PSync(linkNode, logRuntime.Logger)
			fsHandler.SetP2PSync(fsSync)

			linkRunErr := make(chan error, 1)
			go func() {
				linkRunErr <- linkNode.Run(ctx)
			}()
			go func() {
				if err := <-linkRunErr; err != nil && ctx.Err() == nil {
					logger.Warn("link node failed", "error", err)
				}
			}()

			// After the link node starts, publish multiaddrs and auto-connect.
			go func() {
				// Wait for host to be ready, but don't spin forever if the link
				// node failed during startup.
				deadline := time.Now().Add(10 * time.Second)
				for linkNode.Host() == nil {
					if ctx.Err() != nil {
						return
					}
					if time.Now().After(deadline) {
						logger.Warn("link node did not become ready before startup timeout")
						return
					}
					time.Sleep(50 * time.Millisecond)
				}

				// Register P2P join handler as soon as the host exists.
				joinHandler := skyjoin.NewHandler(bundle, nil, logRuntime.Logger)
				joinHandler.SetNSKeyProvider(func() []skyjoin.NSKey {
					out := make([]skyjoin.NSKey, 0, 4)
					if ns, key := kvStore.NamespaceKey(); key != nil {
						out = append(out, skyjoin.NSKey{Namespace: ns, Key: key})
					}
					if ns, key := secretsStore.Transport().NamespaceKey(); key != nil {
						out = append(out, skyjoin.NSKey{Namespace: ns, Key: key})
					}
					out = append(out, fsHandler.NamespaceKeys(context.Background())...)
					return out
				})
				joinHandler.SetOnBundleUpdated(func(updated *skyid.Bundle) error {
					if err := idStore.Save(updated); err != nil {
						return err
					}
					reconcileTrustedSecrets("join")
					if triggerPrivateNetwork != nil {
						triggerPrivateNetwork("bundle_updated", "join")
					}
					return nil
				})
				linkNode.Host().SetStreamHandler(skyjoin.Protocol, joinHandler.HandleStream)
				logger.Info("P2P join handler registered")

				// Register KV sync protocol before any bootstrap work that can block
				// on slow discovery/publish paths. Otherwise a freshly joined peer can
				// connect and immediately fail with "protocols not supported".
				kvSync.RegisterProtocol()
				fsSync.RegisterProtocol()
				fsSync.StartAntiEntropy(ctx, 0)

				addrs := link.HostMultiaddrs(linkNode)

				// Publish multiaddrs to S3 device registry (if configured).
				if backend != nil {
					if err := skyfs.UpdateDeviceMultiaddrs(ctx, backend, bundle.DeviceID(), addrs); err != nil {
						logger.Warn("failed to publish multiaddrs to S3", "error", err)
					} else {
						logger.Info("published multiaddrs to S3 device registry", "count", len(addrs))
					}
				}

				rememberLiveRelays()
				if triggerPrivateNetwork != nil {
					triggerPrivateNetwork("startup", "")
				}
				watchPrivateNetworkEvents(ctx, logger, linkNode, runtimeHealth, triggerPrivateNetwork, rememberLiveRelays)
			}()

			// Log KV startup errors but don't block the daemon.
			go func() {
				if err := <-kvRunErr; err != nil {
					logger.Warn("kv store failed", "error", err)
				}
			}()
			go func() {
				if err := <-secretsRunErr; err != nil {
					slog.Warn("secrets store failed", "error", err)
				}
			}()

			// Check for updates on startup and every 2 hours.
			go skyupdate.PeriodicCheck(ctx, Version, server.Emit)

			server.OnServe(func() {
				fsHandler.StartDrives()
				if hasStorage {
					fsHandler.StartAutoApprove(ctx)
				}
				go sandboxManager.RunManagedReconnectLoop(ctx)
			})

			fmt.Println(sockPath)

			httpPort, _ := cmd.Flags().GetInt("http-port")
			go func() {
				if err := server.ServeHTTP(ctx, httpPort); err != nil {
					logger.Error("HTTP server failed", "error", err)
				}
			}()
			time.Sleep(100 * time.Millisecond)
			if addr := server.HTTPAddr(); addr != "" {
				fmt.Printf("http://localhost%s\n", addr)
			}

			return server.Serve(ctx)
		},
	}
	cmd.Flags().String("socket", "", "Socket path")
	cmd.Flags().Int("http-port", skyrpc.DefaultHTTPPort, "HTTP RPC port")
	cmd.Flags().StringSliceVar(&linkListenAddrs, "link-listen", nil, "Additional libp2p listen addresses")
	cmd.Flags().StringSliceVar(&linkBootstrapPeers, "link-bootstrap", nil, "Bootstrap peer multiaddrs for libp2p discovery")
	cmd.Flags().StringSliceVar(&linkRelayPeers, "link-relay", nil, "Static libp2p relay multiaddrs for live skylink fallback")
	cmd.Flags().BoolVar(&allowNoLinkRelay, "allow-no-link-relay", false, "Deprecated compatibility no-op")
	_ = cmd.Flags().MarkHidden("allow-no-link-relay")
	cmd.Flags().BoolVar(&noDefaultBootstrap, "no-default-bootstrap", false, "Disable default public libp2p bootstrap peers")
	cmd.Flags().StringSliceVar(&relayOverrides, "nostr-relay", nil, "Nostr relay URLs for private-network discovery")
	cmd.Flags().BoolVar(&noDefaultRelays, "no-default-relays", false, "Disable default public Nostr relays")
	cmd.Flags().IntVar(&fsPollSeconds, "fs-poll-seconds", 30, "Remote poll interval in seconds for sync drives")
	return cmd
}

func watchPrivateNetworkEvents(ctx context.Context, logger *slog.Logger, linkNode *link.Node, tracker *link.RuntimeHealthTracker, trigger func(reason, detail string), onAddressUpdate func()) {
	if linkNode == nil || linkNode.Host() == nil || trigger == nil {
		return
	}

	sub, err := linkNode.Host().EventBus().Subscribe([]any{
		new(event.EvtLocalAddressesUpdated),
		new(event.EvtLocalReachabilityChanged),
		new(event.EvtPeerConnectednessChanged),
	})
	if err != nil {
		logger.Warn("failed to subscribe to local address updates", "error", err)
		return
	}

	go func() {
		defer sub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-sub.Out():
				if !ok {
					return
				}
				switch evt := raw.(type) {
				case event.EvtLocalAddressesUpdated:
					if onAddressUpdate != nil {
						onAddressUpdate()
					}
					if tracker != nil {
						tracker.RecordAddressUpdate(len(evt.Current))
					}
					logger.Info("republishing private-network presence after local address update",
						"current_addrs", len(evt.Current),
					)
					trigger("address_change", fmt.Sprintf("%d", len(evt.Current)))
				case event.EvtLocalReachabilityChanged:
					if tracker != nil {
						tracker.RecordReachability(evt.Reachability.String())
					}
					logger.Info("republishing private-network presence after reachability change",
						"reachability", evt.Reachability.String(),
					)
					trigger("reachability_change", evt.Reachability.String())
				case event.EvtPeerConnectednessChanged:
					if !isPrivateNetworkPeer(linkNode, evt.Peer) {
						continue
					}
					if tracker != nil {
						tracker.RecordPeerConnectedness(evt.Peer.String(), evt.Connectedness.String())
					}
					logger.Info("private-network peer connectedness changed",
						"peer_id", evt.Peer.String(),
						"connectedness", evt.Connectedness.String(),
					)
					trigger("peer_connectedness", evt.Peer.String()+":"+evt.Connectedness.String())
				default:
					continue
				}
			}
		}
	}()
}

func isPrivateNetworkPeer(linkNode *link.Node, pid peer.ID) bool {
	if linkNode == nil || linkNode.Bundle() == nil || linkNode.Bundle().Manifest == nil {
		return false
	}
	for _, device := range linkNode.Bundle().Manifest.Devices {
		expected, err := link.PeerIDFromPubKey(device.PublicKey)
		if err != nil {
			continue
		}
		if expected == pid {
			return true
		}
	}
	return false
}

func expectedPrivateNetworkPeers(bundle *skyid.Bundle) int {
	if bundle == nil || bundle.Manifest == nil {
		return 0
	}
	if len(bundle.Manifest.Devices) <= 1 {
		return 0
	}
	return len(bundle.Manifest.Devices) - 1
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	cp := t
	return &cp
}
