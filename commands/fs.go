package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	"github.com/spf13/cobra"
)

func FsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "Encrypted file storage",
	}

	cmd.AddCommand(fsInitCmd())
	cmd.AddCommand(fsPutCmd())
	cmd.AddCommand(fsGetCmd())
	cmd.AddCommand(fsListCmd())
	cmd.AddCommand(fsRemoveCmd())
	cmd.AddCommand(fsInfoCmd())
	cmd.AddCommand(fsServeCmd())
	cmd.AddCommand(fsSyncCmd())
	cmd.AddCommand(fsCompactCmd())
	cmd.AddCommand(fsGCCmd())
	cmd.AddCommand(fsVersionsCmd())
	cmd.AddCommand(fsRestoreCmd())
	cmd.AddCommand(fsSnapshotsCmd())
	cmd.AddCommand(fsDriveCmd())
	cmd.AddCommand(fsInviteCmd())
	cmd.AddCommand(fsJoinCmd())
	cmd.AddCommand(fsApproveCmd())

	return cmd
}

func fsInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize encrypted storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, _ := cmd.Flags().GetString("bucket")
			region, _ := cmd.Flags().GetString("region")
			endpoint, _ := cmd.Flags().GetString("endpoint")
			pathStyle, _ := cmd.Flags().GetBool("path-style")

			id, err := skyfs.GenerateDeviceKey()
			if err != nil {
				return err
			}
			idPath, err := config.DefaultIdentityPath()
			if err != nil {
				return err
			}
			os.MkdirAll(filepath.Dir(idPath), 0700)
			if err := skyfs.SaveKeyWithDescription(id, idPath, "skyfs device key"); err != nil {
				return err
			}
			cfg := &config.Config{
				Bucket: bucket, Region: region, Endpoint: endpoint,
				ForcePathStyle: pathStyle, IdentityFile: idPath,
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			ctx := context.Background()
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}
			if err := skyfs.WriteSchema(ctx, backend); err != nil {
				return err
			}
			// Register this device
			skyfs.RegisterDevice(ctx, backend, id.Address(), skyfs.GetDeviceName(), cmd.Root().Version)

			fmt.Printf("Initialized skyfs\n  Schema:   v%s\n  Identity: %s\n  Bucket:   %s\n",
				skyfs.SchemaVersion, id.Address(), cfg.Bucket)
			return nil
		},
	}
	cmd.Flags().String("bucket", "", "S3 bucket name")
	cmd.Flags().String("region", "us-east-1", "S3 region")
	cmd.Flags().String("endpoint", "", "Custom S3 endpoint")
	cmd.Flags().Bool("path-style", false, "Use path-style S3 addressing")
	cmd.MarkFlagRequired("bucket")
	return cmd
}

func fsPutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <file>",
		Short: "Encrypt and store a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			info, _ := f.Stat()

			remotePath, _ := cmd.Flags().GetString("as")
			if remotePath == "" {
				remotePath = filepath.Base(args[0])
			}

			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}

			pr := skyfs.NewProgressReader(f, info.Size(), func(transferred, total int64) {
				pct := int(float64(transferred) / float64(total) * 100)
				fmt.Fprintf(os.Stderr, "\ruploading %s  %d%%  %s / %s",
					remotePath, pct, formatSize(transferred), formatSize(total))
			})
			if err := store.Put(ctx, remotePath, pr); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\r\033[K")
			fmt.Printf("stored %s (%s)\n", remotePath, formatSize(info.Size()))
			return nil
		},
	}
	cmd.Flags().String("as", "", "Remote path")
	return cmd
}

func fsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Retrieve and decrypt a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("out")
			if out == "" {
				out = filepath.Base(args[0])
			}
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			var downloaded int64
			pw := skyfs.NewProgressWriter(f, 0, func(transferred, _ int64) {
				downloaded = transferred
				fmt.Fprintf(os.Stderr, "\rdownloading %s  %s", args[0], formatSize(transferred))
			})
			if err := store.Get(ctx, args[0], pw); err != nil {
				os.Remove(out)
				return err
			}
			fmt.Fprintf(os.Stderr, "\r\033[K")
			fmt.Printf("retrieved %s → %s (%s)\n", args[0], out, formatSize(downloaded))
			return nil
		},
	}
	cmd.Flags().String("out", "", "Output path")
	return cmd
}

func fsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [prefix]",
		Short: "List stored files",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) > 0 {
				prefix = args[0]
			}
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			entries, err := store.List(ctx, prefix)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("no files found")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "PATH\tSIZE\tMODIFIED\tNAMESPACE\n")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					e.Path, formatSize(e.Size),
					e.Modified.Format("2006-01-02 15:04"), e.Namespace)
			}
			w.Flush()
			return nil
		},
	}
}

func fsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <path>",
		Short: "Remove a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			if err := store.Remove(ctx, args[0]); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	}
}

func fsInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show configuration and stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			info, err := store.Info(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("Identity:   %s\nFiles:      %d\nTotal size: %s\n",
				info.ID, info.FileCount, formatSize(info.TotalSize))
			if len(info.Namespaces) > 0 {
				fmt.Printf("Namespaces: %v\n", info.Namespaces)
			}
			return nil
		},
	}
}

func fsServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start JSON-RPC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
				<-sigCh
				cancel()
			}()

			sockPath, _ := cmd.Flags().GetString("socket")
			if sockPath == "" {
				sockPath = "/tmp/sky10.sock"
			}
			cfgDir, _ := config.Dir()

			// Create store without schema validation — serve starts fast,
			// validates lazily when commands actually hit S3.
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
			store := skyfs.New(backend, id)
			store.SetClient("cli/" + cmd.Root().Version)

			// Register this device in background (ip-api.com can be slow)
			go skyfs.RegisterDevice(ctx, backend, id.Address(), skyfs.GetDeviceName(), cmd.Root().Version)

			server := skyfs.NewRPCServer(store, sockPath, filepath.Join(cfgDir, "drives.json"), cmd.Root().Version, nil)
			fmt.Println(sockPath)
			return server.Serve(ctx)
		},
	}
	cmd.Flags().String("socket", "", "Socket path")
	return cmd
}

func fsSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync <directory>",
		Short: "Sync a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			once, _ := cmd.Flags().GetBool("once")
			ns, _ := cmd.Flags().GetString("namespace")
			prefix, _ := cmd.Flags().GetString("prefix")
			poll, _ := cmd.Flags().GetInt("poll")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
				<-sigCh
				fmt.Fprintln(os.Stderr, "\nshutting down...")
				cancel()
			}()

			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			ignoreMatcher := skyfs.NewIgnoreMatcher(dir)
			syncCfg := skyfs.SyncConfig{LocalRoot: dir, IgnoreFunc: ignoreMatcher.IgnoreFunc()}
			if ns != "" {
				syncCfg.Namespaces = []string{ns}
			}
			if prefix != "" {
				syncCfg.Prefixes = []string{prefix}
			}

			if once {
				engine := skyfs.NewSyncEngine(store, syncCfg)
				result, err := engine.SyncOnce(ctx)
				if err != nil {
					return err
				}
				fmt.Printf("synced: %d uploaded, %d downloaded, %d errors\n",
					result.Uploaded, result.Downloaded, len(result.Errors))
				return nil
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			daemonCfg := skyfs.DaemonConfig{SyncConfig: syncCfg, PollSeconds: poll}
			daemon, err := skyfs.NewDaemon(store, nil, daemonCfg, logger)
			if err != nil {
				return err
			}
			fmt.Printf("syncing %s (poll every %ds, Ctrl+C to stop)\n", dir, poll)
			return daemon.Run(ctx)
		},
	}
	cmd.Flags().Bool("once", false, "Sync once and exit")
	cmd.Flags().String("namespace", "", "Sync only this namespace")
	cmd.Flags().String("prefix", "", "Sync only paths with this prefix")
	cmd.Flags().Int("poll", 30, "Poll interval in seconds")
	return cmd
}

func fsCompactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact ops log into snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			keep, _ := cmd.Flags().GetInt("keep")
			ctx := context.Background()
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
			result, err := skyfs.Compact(ctx, backend, id, keep)
			if err != nil {
				return err
			}
			fmt.Printf("Compacted %d ops\n  Deleted: %d ops, %d snapshots\n  Kept: %d snapshots\n",
				result.OpsCompacted, result.OpsDeleted, result.SnapshotsDeleted, result.SnapshotsKept)
			return nil
		},
	}
	cmd.Flags().Int("keep", 3, "Number of snapshots to keep")
	return cmd
}

func fsGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage collect orphaned blobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			ctx := context.Background()
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
			result, err := skyfs.GC(ctx, backend, id, dryRun)
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Println("Dry run (no changes made):")
			}
			fmt.Printf("Blobs referenced: %d\nBlobs found:      %d\nBlobs deleted:    %d\nBytes reclaimed:  %s\n",
				result.BlobsReferenced, result.BlobsFound, result.BlobsDeleted, formatSize(result.BytesReclaimed))
			return nil
		},
	}
	cmd.Flags().Bool("dry-run", false, "Show what would be deleted")
	return cmd
}

func fsVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <path>",
		Short: "Show file version history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			versions, err := skyfs.ListVersions(ctx, store, args[0])
			if err != nil {
				return err
			}
			if len(versions) == 0 {
				fmt.Println("no versions found")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "TIMESTAMP\tDEVICE\tSIZE\tCHECKSUM\n")
			for _, v := range versions {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					v.Timestamp.Format("2006-01-02 15:04:05"),
					v.Device, formatSize(v.Size), v.Checksum[:12])
			}
			w.Flush()
			return nil
		},
	}
}

func fsRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <path>",
		Short: "Restore a file version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			at, _ := cmd.Flags().GetString("at")
			timestamp, err := time.Parse(time.RFC3339, at)
			if err != nil {
				return fmt.Errorf("invalid timestamp: %w (use RFC3339)", err)
			}
			out, _ := cmd.Flags().GetString("out")
			if out == "" {
				out = filepath.Base(args[0])
			}
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			if err := skyfs.RestoreVersion(ctx, store, args[0], timestamp, f); err != nil {
				f.Close()
				os.Remove(out)
				return err
			}
			info, _ := f.Stat()
			f.Close()
			fmt.Printf("restored %s @ %s → %s (%s)\n",
				args[0], timestamp.Format("2006-01-02 15:04:05"), out, formatSize(info.Size()))
			return nil
		},
	}
	cmd.Flags().String("at", "", "Restore at this timestamp (RFC3339)")
	cmd.Flags().String("out", "", "Output path")
	cmd.MarkFlagRequired("at")
	return cmd
}

func fsSnapshotsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshots",
		Short: "List compacted snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			snapshots, err := skyfs.ListSnapshots(ctx, store)
			if err != nil {
				return err
			}
			if len(snapshots) == 0 {
				fmt.Println("no snapshots")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "TIMESTAMP\tFILES\tSIZE\n")
			for _, s := range snapshots {
				fmt.Fprintf(w, "%s\t%d\t%s\n",
					s.Timestamp.Format("2006-01-02 15:04:05"), s.FileCount, formatSize(s.TotalSize))
			}
			w.Flush()
			return nil
		},
	}
}

// --- shared helpers ---

func openStore(ctx context.Context) (*skyfs.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	id, err := skyfs.LoadKey(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("loading identity: %w", err)
	}
	backend, err := makeBackend(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := skyfs.ValidateSchema(ctx, backend); err != nil {
		return nil, err
	}
	store := skyfs.New(backend, id)
	store.SetClient("cli")
	return store, nil
}

func makeBackend(ctx context.Context, cfg *config.Config) (*s3backend.Backend, error) {
	return s3backend.New(ctx, s3backend.Config{
		Bucket: cfg.Bucket, Region: cfg.Region,
		Endpoint: cfg.Endpoint, ForcePathStyle: cfg.ForcePathStyle,
	})
}

func fsDriveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drive",
		Short: "Manage sync drives (~/Cirrus/ folders)",
	}
	cmd.AddCommand(fsDriveCreateCmd())
	cmd.AddCommand(fsDriveListCmd())
	cmd.AddCommand(fsDriveRemoveCmd())
	return cmd
}

func fsDriveCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <path>",
		Short: "Create a new sync drive",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			cfgDir, _ := config.Dir()
			dm := skyfs.NewDriveManager(store, filepath.Join(cfgDir, "drives.json"))
			ns, _ := cmd.Flags().GetString("namespace")
			if ns == "" {
				ns = args[0]
			}
			drive, err := dm.CreateDrive(args[0], args[1], ns)
			if err != nil {
				return err
			}
			fmt.Printf("Created drive %q → %s (namespace: %s)\n", drive.Name, drive.LocalPath, drive.Namespace)
			return nil
		},
	}
	cmd.Flags().String("namespace", "", "Remote namespace (default: drive name)")
	return cmd
}

func fsDriveListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all drives",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			cfgDir, _ := config.Dir()
			dm := skyfs.NewDriveManager(store, filepath.Join(cfgDir, "drives.json"))
			drives := dm.ListDrives()
			if len(drives) == 0 {
				fmt.Println("No drives. Create one with: sky10 fs drive create <name>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "NAME\tPATH\tNAMESPACE\n")
			for _, d := range drives {
				fmt.Fprintf(w, "%s\t%s\t%s\n", d.Name, d.LocalPath, d.Namespace)
			}
			w.Flush()
			return nil
		},
	}
}

func fsDriveRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a drive (does not delete local files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			store, err := openStore(ctx)
			if err != nil {
				return err
			}
			cfgDir, _ := config.Dir()
			dm := skyfs.NewDriveManager(store, filepath.Join(cfgDir, "drives.json"))
			id := "drive_" + args[0]
			if err := dm.RemoveDrive(id); err != nil {
				return err
			}
			fmt.Printf("Removed drive %q\n", args[0])
			return nil
		},
	}
}

func fsInviteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "invite",
		Short: "Generate an invite code for another device",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			id, err := skyfs.LoadKey(cfg.IdentityFile)
			if err != nil {
				return err
			}
			ctx := context.Background()
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}

			// Read S3 credentials from environment
			accessKey := os.Getenv("S3_ACCESS_KEY_ID")
			secretKey := os.Getenv("S3_SECRET_ACCESS_KEY")
			if accessKey == "" || secretKey == "" {
				return fmt.Errorf("S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY must be set")
			}

			code, err := skyfs.CreateInvite(ctx, backend, skyfs.InviteConfig{
				Endpoint:       cfg.Endpoint,
				Bucket:         cfg.Bucket,
				Region:         cfg.Region,
				AccessKey:      accessKey,
				SecretKey:      secretKey,
				ForcePathStyle: cfg.ForcePathStyle,
				DevicePubKey:   id.Address(),
			})
			if err != nil {
				return err
			}

			fmt.Println("\nShare this invite code with the other device:")
			fmt.Println(code)
			fmt.Println("\nThe other device runs: sky10 fs join <code>")
			return nil
		},
	}
}

func fsJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <invite-code>",
		Short: "Join a bucket using an invite code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			invite, err := skyfs.DecodeInvite(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Joining bucket %s at %s\n", invite.Bucket, invite.Endpoint)

			// Generate new key for this device
			keyPath, err := config.DefaultIdentityPath()
			if err != nil {
				return err
			}

			var id *skyfs.DeviceKey
			if _, err := os.Stat(keyPath); os.IsNotExist(err) {
				id, err = skyfs.GenerateDeviceKey()
				if err != nil {
					return err
				}
				os.MkdirAll(filepath.Dir(keyPath), 0700)
				if err := skyfs.SaveKeyWithDescription(id, keyPath, "skyfs device key"); err != nil {
					return err
				}
				fmt.Printf("Generated key: %s\n", id.Address())
			} else {
				id, err = skyfs.LoadKey(keyPath)
				if err != nil {
					return err
				}
				fmt.Printf("Using existing key: %s\n", id.Address())
			}

			// Write config (WITHOUT reinitializing — don't overwrite existing schema)
			cfg := &config.Config{
				Bucket:         invite.Bucket,
				Region:         invite.Region,
				Endpoint:       invite.Endpoint,
				ForcePathStyle: invite.ForcePathStyle,
				IdentityFile:   keyPath,
			}
			if err := config.Save(cfg); err != nil {
				return err
			}

			// Connect to S3 and submit our public key
			ctx := context.Background()
			os.Setenv("S3_ACCESS_KEY_ID", invite.AccessKey)
			os.Setenv("S3_SECRET_ACCESS_KEY", invite.SecretKey)
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}

			if err := skyfs.SubmitJoin(ctx, backend, invite.InviteID, id.Address()); err != nil {
				return fmt.Errorf("submitting join request: %w", err)
			}

			fmt.Println("Join request submitted. Waiting for approval...")
			fmt.Println("The inviting device needs to run: sky10 fs approve")

			// Poll for approval
			for i := 0; i < 60; i++ { // wait up to 5 minutes
				granted, err := skyfs.IsGranted(ctx, backend, invite.InviteID)
				if err != nil {
					return err
				}
				if granted {
					// Register this device now that we're approved
					skyfs.RegisterDevice(ctx, backend, id.Address(), skyfs.GetDeviceName(), cmd.Root().Version)
					fmt.Println("Approved! You can now sync.")
					return nil
				}
				fmt.Print(".")
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Second):
				}
			}

			fmt.Println("\nTimed out waiting for approval. Run 'sky10 fs join' again later.")
			return nil
		},
	}
}

func fsApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve",
		Short: "Approve a pending join request",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			id, err := skyfs.LoadKey(cfg.IdentityFile)
			if err != nil {
				return err
			}
			ctx := context.Background()
			backend, err := makeBackend(ctx, cfg)
			if err != nil {
				return err
			}

			// Find pending invites
			inviteKeys, err := backend.List(ctx, "invites/")
			if err != nil {
				return err
			}

			// Group by invite ID and find ones with pubkey but no granted
			inviteIDs := make(map[string]bool)
			for _, k := range inviteKeys {
				// Extract invite ID from path: invites/<id>/...
				parts := splitInvitePath(k)
				if parts != "" {
					inviteIDs[parts] = true
				}
			}

			approved := 0
			for inviteID := range inviteIDs {
				// Check if there's a pubkey submission
				joinerAddr, err := skyfs.CheckJoinRequest(ctx, backend, inviteID)
				if err != nil || joinerAddr == "" {
					continue
				}

				// Check if already granted
				granted, _ := skyfs.IsGranted(ctx, backend, inviteID)
				if granted {
					continue
				}

				fmt.Printf("Approving device: %s\n", joinerAddr)
				if err := skyfs.ApproveJoin(ctx, backend, id, joinerAddr, inviteID); err != nil {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					continue
				}
				fmt.Println("  Approved!")
				approved++

				// Don't cleanup yet — joiner needs to poll and see the granted marker.
				// Invite artifacts are small and harmless; cleanup can happen later.
			}

			if approved == 0 {
				fmt.Println("No pending join requests found.")
			}
			return nil
		},
	}
}

// splitInvitePath extracts invite ID from "invites/<id>/..." path.
func splitInvitePath(key string) string {
	if len(key) < 9 || key[:8] != "invites/" {
		return ""
	}
	rest := key[8:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return ""
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
