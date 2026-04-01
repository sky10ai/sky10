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
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
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
			idStore, err := skyid.NewStore()
			if err != nil {
				return err
			}
			bundle, err := idStore.Load()
			if err != nil {
				return err
			}
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}

			if _, err := backend.List(ctx, "ops/"); err != nil {
				slog.Warn("S3 credential check failed (will retry)", "error", err)
			}

			store := skyfs.New(backend, bundle.Identity)
			store.SetClient("cli/" + cmd.Root().Version)

			go skyfs.RegisterDevice(ctx, backend, bundle.Address(), skyfs.GetDeviceName(), cmd.Root().Version)
			skyfs.HandleDumpSignal(slog.Default())

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

			kvStore := kv.New(backend, bundle.Identity, kv.Config{Namespace: "default"}, nil)
			server.RegisterHandler(kv.NewRPCHandler(kvStore))
			go func() {
				if err := kvStore.Run(ctx); err != nil {
					slog.Warn("kv store failed", "error", err)
				}
			}()

			// Skylink P2P node (private mode — own devices only).
			linkNode, err := link.New(bundle, link.Config{Mode: link.Private}, nil)
			if err != nil {
				return fmt.Errorf("creating link node: %w", err)
			}
			linkResolver := link.NewResolver(linkNode, link.WithBackend(backend))
			server.RegisterHandler(link.NewRPCHandler(linkNode, linkResolver))
			server.RegisterHandler(skyid.NewRPCHandler(bundle))

			// Wire sync notifications: KV changes notify own devices.
			kvStore.SetNotifier(func(ns string) {
				linkNode.NotifyOwn(ctx, "kv:"+ns)
			})
			linkNode.OnSyncNotify(func(from peer.ID, topic string) {
				if topic == "kv:default" {
					kvStore.Poke()
				}
			})

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
				// Publish our multiaddrs to the S3 device registry.
				addrs := make([]string, 0, len(linkNode.Host().Addrs()))
				for _, a := range linkNode.Host().Addrs() {
					addrs = append(addrs, a.String()+"/p2p/"+linkNode.PeerID().String())
				}
				if err := skyfs.UpdateDeviceMultiaddrs(ctx, backend, bundle.Address(), addrs); err != nil {
					slog.Warn("failed to publish multiaddrs", "error", err)
				} else {
					slog.Info("published multiaddrs to device registry", "count", len(addrs))
				}
				// Auto-connect to own devices.
				link.AutoConnect(ctx, linkNode, backend)
			}()

			server.OnServe(func() {
				fsHandler.StartDrives()
				fsHandler.StartAutoApprove(ctx)
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
