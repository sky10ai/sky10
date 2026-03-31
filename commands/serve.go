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

	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	"github.com/sky10/sky10/pkg/kv"
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
			id, err := skyfs.LoadKey(cfg.IdentityFile)
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

			store := skyfs.New(backend, id)
			store.SetClient("cli/" + cmd.Root().Version)

			go skyfs.RegisterDevice(ctx, backend, id.Address(), skyfs.GetDeviceName(), cmd.Root().Version)
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

			// Resolve namespace key from fs key infrastructure so all
			// modules (fs, kv) share the same encryption keys.
			kvNSKey, err := store.GetOrCreateNamespaceKey(ctx, "default")
			if err != nil {
				slog.Warn("kv namespace key resolution failed", "error", err)
			}
			kvStore := kv.New(backend, id, kv.Config{Namespace: "default", Key: kvNSKey}, nil)
			server.RegisterHandler(kv.NewRPCHandler(kvStore))
			go func() {
				if err := kvStore.Run(ctx); err != nil {
					slog.Warn("kv store failed", "error", err)
				}
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
