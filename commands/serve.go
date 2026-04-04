package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
			server.RegisterHandler(link.NewRPCHandler(linkNode, linkResolver))
			server.RegisterHandler(skyid.NewRPCHandler(bundle))
			server.RegisterHandler(skyupdate.NewRPCHandler(Version, server.Emit))

			// Agent registry — local agent registration and dispatch.
			agentRegistry := skyagent.NewRegistry(bundle.DeviceID(), skyfs.GetDeviceName(), nil)
			agentCaller := skyagent.NewCaller()
			server.RegisterHandler(skyagent.NewRPCHandler(agentRegistry, agentCaller, server.Emit))
			go skyagent.NewHealthChecker(agentRegistry, agentCaller, server.Emit, nil).Run(ctx)

			// Show connected P2P peers in device list.
			fsHandler.SetPeerDevices(func() []skyfs.DeviceInfo {
				var peers []skyfs.DeviceInfo
				for _, pid := range linkNode.ConnectedPeers() {
					peers = append(peers, skyfs.DeviceInfo{
						ID:       "D-" + pid.String()[:8],
						Name:     pid.String()[:16],
						LastSeen: time.Now().UTC().Format(time.RFC3339),
					})
				}
				return peers
			})

			// Wire sync notifications: KV changes notify own devices.
			kvStore.SetNotifier(func(ns string) {
				linkNode.NotifyOwn(ctx, "kv:"+ns)
			})
			linkNode.OnSyncNotify(func(from peer.ID, topic string) {
				if topic == "kv:default" {
					kvStore.Poke()
				}
			})

			// In P2P-only mode, wire direct KV snapshot sync over libp2p.
			kvSync := kv.NewP2PSync(kvStore, linkNode, bundle.Identity, nil)
			kvStore.SetP2PSync(kvSync)

			go func() {
				if err := linkNode.Run(ctx); err != nil {
					slog.Warn("link node failed", "error", err)
				}
			}()

			// After the link node starts, publish multiaddrs and auto-connect.
			go func() {
				// Wait for host to be ready.
				for linkNode.Host() == nil {
					time.Sleep(50 * time.Millisecond)
				}

				addrs := link.HostMultiaddrs(linkNode)

				// Publish multiaddrs to S3 device registry (if configured).
				if backend != nil {
					if err := skyfs.UpdateDeviceMultiaddrs(ctx, backend, bundle.DeviceID(), addrs); err != nil {
						slog.Warn("failed to publish multiaddrs to S3", "error", err)
					} else {
						slog.Info("published multiaddrs to S3 device registry", "count", len(addrs))
					}
				}

				// Publish multiaddrs to Nostr relays.
				nostr := link.NewNostrDiscovery(cfg.Relays(), nil)
				nostrSK := link.NostrSecretKey(bundle.Device)
				if err := nostr.Publish(ctx, nostrSK, bundle.Address(), addrs); err != nil {
					slog.Warn("failed to publish multiaddrs to Nostr", "error", err)
				} else {
					slog.Info("published multiaddrs to Nostr", "count", len(addrs))
				}

				// Auto-connect to own devices. Retry after 15s in case the
				// other device hasn't published its new addrs to Nostr yet.
				link.AutoConnect(ctx, linkNode, backend, cfg.Relays())

				// Register KV sync protocol handler after link is ready.
				kvSync.RegisterProtocol()

				go func() {
					time.Sleep(15 * time.Second)
					if len(linkNode.ConnectedPeers()) == 0 {
						slog.Info("retrying auto-connect...")
						link.AutoConnect(ctx, linkNode, backend, cfg.Relays())
					}
				}()

				// Register P2P join handler (auto-approve — invite code is auth).
				joinHandler := skyjoin.NewHandler(bundle, nil, nil)
				joinHandler.SetNSKeyProvider(func() []skyjoin.NSKey {
					ns, key := kvStore.NamespaceKey()
					if key == nil {
						return nil
					}
					return []skyjoin.NSKey{{Namespace: ns, Key: key}}
				})
				linkNode.Host().SetStreamHandler(skyjoin.Protocol, joinHandler.HandleStream)
				slog.Info("P2P join handler registered")
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
