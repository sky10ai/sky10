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

			if err := skyfs.KillExistingDaemon(); err != nil {
				slog.Info("daemon: " + err.Error())
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

			if backend != nil {
				if _, err := backend.List(ctx, "ops/"); err != nil {
					slog.Warn("S3 credential check failed (will retry)", "error", err)
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
			skyfs.HandleDumpSignal(slog.Default())

			hasStorage := backend != nil
			if !hasStorage {
				slog.Info("starting in P2P-only mode (no S3 storage configured)")
			}

			if err := skyfs.KillExistingDaemon(); err != nil {
				slog.Warn("killed stale daemon", "error", err)
			}
			if err := skyfs.WritePIDFile(); err != nil {
				return fmt.Errorf("writing PID file: %w", err)
			}
			defer skyfs.RemovePIDFile()

			server := skyrpc.NewServer(sockPath, cmd.Root().Version, nil)
			fsHandler := skyfs.NewFSHandler(store, server, filepath.Join(cfgDir, "drives.json"))
			server.RegisterHandler(fsHandler)
			server.HandleHTTP("POST /upload", fsHandler.HandleUpload)
			server.HandleHTTP("GET /download", fsHandler.HandleDownload)

			kvStore := kv.New(backend, bundle.Identity, kv.Config{Namespace: "default"}, nil)
			server.RegisterHandler(kv.NewRPCHandler(kvStore))
			kvRunErr := make(chan error, 1)
			go func() {
				kvRunErr <- kvStore.Run(ctx)
			}()

			// Skylink P2P node — network mode enables DHT, relay, and external peers.
			linkNode, err := link.New(bundle, link.Config{Mode: link.Network}, nil)
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
			identityRPC := skyid.NewRPCHandler(bundle)
			server.RegisterHandler(identityRPC)
			server.RegisterHandler(skyupdate.NewRPCHandler(Version, server.Emit))

			var privateNetworkMu sync.Mutex
			refreshPrivateNetwork = func() {
				privateNetworkMu.Lock()
				defer privateNetworkMu.Unlock()

				membership, source, err := linkResolver.ResolveMembership(ctx, bundle.Address())
				if err != nil {
					slog.Warn("private-network membership resolve failed", "error", err)
				} else if source != "local" {
					manifest, err := membership.ToManifest(bundle.Identity)
					if err != nil {
						slog.Warn("private-network membership cache rebuild failed", "error", err)
					} else if !manifest.HasDevice(bundle.Device.PublicKey) {
						slog.Warn("resolved membership missing current device; keeping local cache",
							"identity", bundle.Address(),
						)
					} else {
						bundle.Manifest = manifest
						if err := idStore.Save(bundle); err != nil {
							slog.Warn("saving refreshed private-network cache failed", "error", err)
						}
					}
				}

				if err := linkNode.PublishRecord(ctx); err != nil {
					slog.Warn("failed to publish private-network records to DHT", "error", err)
				} else {
					slog.Info("published private-network records to DHT")
				}

				if len(cfg.Relays()) > 0 {
					nostr := link.NewNostrDiscovery(cfg.Relays(), nil)
					membershipRecord, err := linkNode.CurrentMembershipRecord()
					if err != nil {
						slog.Warn("building private-network membership record failed", "error", err)
					} else if err := nostr.PublishMembership(ctx, bundle.Identity, membershipRecord); err != nil {
						slog.Warn("failed to publish private-network membership to Nostr", "error", err)
					}

					presenceRecord, err := linkNode.CurrentPresenceRecord(0)
					if err != nil {
						slog.Warn("building private-network presence record failed", "error", err)
					} else if err := nostr.PublishPresence(ctx, bundle.Device, presenceRecord); err != nil {
						slog.Warn("failed to publish private-network presence to Nostr", "error", err)
					}
				}

				link.AutoConnect(ctx, linkResolver)
			}
			configureIdentityRPCHandler(identityRPC, bundle, idStore, backend, linkNode, linkResolver, cfg.Relays(), refreshPrivateNetwork)

			// Agent registry — local agent registration and message routing.
			agentRegistry := skyagent.NewRegistry(bundle.DeviceID(), skyfs.GetDeviceName(), nil)
			agentRouter := skyagent.NewRouter(agentRegistry, linkNode, server.Emit, bundle.DeviceID(), nil)
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
				slog.Info("wallet: OWS detected, enabling wallet RPC")
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
			kvSync := kv.NewP2PSync(kvStore, linkNode, bundle.Identity, nil)
			kvStore.SetP2PSync(kvSync)

			linkRunErr := make(chan error, 1)
			go func() {
				linkRunErr <- linkNode.Run(ctx)
			}()
			go func() {
				if err := <-linkRunErr; err != nil && ctx.Err() == nil {
					slog.Warn("link node failed", "error", err)
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
						slog.Warn("link node did not become ready before startup timeout")
						return
					}
					time.Sleep(50 * time.Millisecond)
				}

				// Register P2P join handler as soon as the host exists.
				joinHandler := skyjoin.NewHandler(bundle, nil, nil)
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
				slog.Info("P2P join handler registered")

				addrs := link.HostMultiaddrs(linkNode)

				// Publish multiaddrs to S3 device registry (if configured).
				if backend != nil {
					if err := skyfs.UpdateDeviceMultiaddrs(ctx, backend, bundle.DeviceID(), addrs); err != nil {
						slog.Warn("failed to publish multiaddrs to S3", "error", err)
					} else {
						slog.Info("published multiaddrs to S3 device registry", "count", len(addrs))
					}
				}

				refreshPrivateNetwork()

				// Register KV sync protocol handler after link is ready.
				kvSync.RegisterProtocol()

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
					slog.Warn("kv store failed", "error", err)
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
					slog.Error("HTTP server failed", "error", err)
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
