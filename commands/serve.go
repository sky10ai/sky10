package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skyjoin "github.com/sky10/sky10/pkg/join"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
	"github.com/sky10/sky10/pkg/logging"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
	skyupdate "github.com/sky10/sky10/pkg/update"
	skywallet "github.com/sky10/sky10/pkg/wallet"
	"github.com/spf13/cobra"
)

// ServeCmd returns the top-level `sky10 serve` command.
func ServeCmd() *cobra.Command {
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
				FilePath:    skyfs.DaemonLogPath(),
				Service:     "sky10",
				Version:     Version,
				BufferLines: 1000,
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

			cfg, err := config.Load()
			if err != nil {
				return err
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

			// Skylink P2P node — network mode enables DHT, relay, and external peers.
			linkNode, err := link.New(bundle, link.Config{Mode: link.Network}, logRuntime.Logger)
			if err != nil {
				return fmt.Errorf("creating link node: %w", err)
			}
			var resolverOpts []link.ResolverOption
			if backend != nil {
				resolverOpts = append(resolverOpts, link.WithBackend(backend))
			}
			resolverOpts = append(resolverOpts, link.WithNostr(cfg.Relays()))
			linkResolver := link.NewResolver(linkNode, resolverOpts...)
			link.RegisterPrivateNetworkHandlers(linkNode)
			server.RegisterHandler(link.NewRPCHandler(linkNode, linkResolver))
			var refreshPrivateNetwork func()
			var kvSync *kv.P2PSync
			identityRPC := skyid.NewRPCHandler(bundle)
			server.RegisterHandler(identityRPC)
			updateRPC := skyupdate.NewRPCHandler(Version, server.Emit)
			updateRPC.SetRestartHandler(func() error {
				os.Exit(75)
				return nil
			})
			server.RegisterHandler(updateRPC)

			var privateNetworkMu sync.Mutex
			refreshPrivateNetwork = func() {
				privateNetworkMu.Lock()
				defer privateNetworkMu.Unlock()

				membership, source, err := linkResolver.ResolveMembership(ctx, bundle.Address())
				if err != nil {
					logger.Warn("private-network membership resolve failed", "error", err)
				} else if source != "local" {
					manifest, err := membership.ToManifest(bundle.Identity)
					if err != nil {
						logger.Warn("private-network membership cache rebuild failed", "error", err)
					} else if !manifest.HasDevice(bundle.Device.PublicKey) {
						logger.Warn("resolved membership missing current device; keeping local cache",
							"identity", bundle.Address(),
						)
					} else {
						bundle.Manifest = manifest
						if err := idStore.Save(bundle); err != nil {
							logger.Warn("saving refreshed private-network cache failed", "error", err)
						}
					}
				}

				if err := linkNode.PublishRecord(ctx); err != nil {
					logger.Warn("failed to publish private-network records to DHT", "error", err)
				} else {
					logger.Info("published private-network records to DHT")
				}

				if len(cfg.Relays()) > 0 {
					nostr := link.NewNostrDiscovery(cfg.Relays(), nil)
					membershipRecord, err := linkNode.CurrentMembershipRecord()
					if err != nil {
						logger.Warn("building private-network membership record failed", "error", err)
					} else if err := nostr.PublishMembership(ctx, bundle.Identity, membershipRecord); err != nil {
						logger.Warn("failed to publish private-network membership to Nostr", "error", err)
					}

					presenceRecord, err := linkNode.CurrentPresenceRecord(0)
					if err != nil {
						logger.Warn("building private-network presence record failed", "error", err)
					} else if err := nostr.PublishPresence(ctx, bundle.Device, presenceRecord); err != nil {
						logger.Warn("failed to publish private-network presence to Nostr", "error", err)
					}
				}

				link.AutoConnect(ctx, linkResolver)
				if kvSync != nil {
					go kvSync.PushToAll(context.Background())
				}
			}
			configureIdentityRPCHandler(identityRPC, bundle, idStore, backend, linkNode, cfg.Relays(), refreshPrivateNetwork)

			// Agent registry — local agent registration and message routing.
			agentRegistry := skyagent.NewRegistry(bundle.DeviceID(), skyfs.GetDeviceName(), logRuntime.Logger)
			agentRouter := skyagent.NewRouter(agentRegistry, linkNode, server.Emit, bundle.DeviceID(), logRuntime.Logger)
			agentRPC := skyagent.NewRPCHandler(agentRegistry, bundle.Identity, server.Emit)
			agentRPC.SetRouter(agentRouter)
			server.RegisterHandler(agentRPC)
			skyagent.RegisterLinkHandlers(linkNode, agentRegistry, server.Emit, agentRouter)
			agentRPC.SetPeerNotifier(func(ctx context.Context, topic string) {
				linkNode.NotifyOwn(ctx, topic)
			})
			// TODO: re-enable health checker once agents reliably heartbeat.
			// go skyagent.NewHealthChecker(agentRegistry, server.Emit, nil).Run(ctx)

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
			linkNode.OnSyncNotify(func(from peer.ID, topic string) {
				switch {
				case topic == "kv:default":
					kvStore.Poke()
				case strings.HasPrefix(topic, "agent:"):
					// Remote device agent change — emit SSE so web UI refreshes.
					server.Emit(topic, map[string]string{"from": from.String()})
				}
			})

			// In P2P-only mode, wire direct KV snapshot sync over libp2p.
			kvSync = kv.NewP2PSync(kvStore, linkNode, bundle.Identity, nil)
			kvStore.SetP2PSync(kvSync)

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
					ns, key := kvStore.NamespaceKey()
					if key == nil {
						return nil
					}
					return []skyjoin.NSKey{{Namespace: ns, Key: key}}
				})
				joinHandler.SetOnBundleUpdated(func(updated *skyid.Bundle) error {
					if err := idStore.Save(updated); err != nil {
						return err
					}
					go refreshPrivateNetwork()
					return nil
				})
				linkNode.Host().SetStreamHandler(skyjoin.Protocol, joinHandler.HandleStream)
				logger.Info("P2P join handler registered")

				// Register KV sync protocol before any bootstrap work that can block
				// on slow discovery/publish paths. Otherwise a freshly joined peer can
				// connect and immediately fail with "protocols not supported".
				kvSync.RegisterProtocol()

				addrs := link.HostMultiaddrs(linkNode)

				// Publish multiaddrs to S3 device registry (if configured).
				if backend != nil {
					if err := skyfs.UpdateDeviceMultiaddrs(ctx, backend, bundle.DeviceID(), addrs); err != nil {
						logger.Warn("failed to publish multiaddrs to S3", "error", err)
					} else {
						logger.Info("published multiaddrs to S3 device registry", "count", len(addrs))
					}
				}

				refreshPrivateNetwork()

				go func() {
					ticker := time.NewTicker(2 * time.Minute)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							refreshPrivateNetwork()
						}
					}
				}()
			}()

			// Log KV startup errors but don't block the daemon.
			go func() {
				if err := <-kvRunErr; err != nil {
					logger.Warn("kv store failed", "error", err)
				}
			}()

			// Check for updates on startup and every 2 hours.
			go skyupdate.PeriodicCheck(ctx, Version, server.Emit)

			server.OnServe(func() {
				if hasStorage {
					fsHandler.StartDrives()
					fsHandler.StartAutoApprove(ctx)
				}
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
	return cmd
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
