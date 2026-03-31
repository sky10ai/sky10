package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	"github.com/spf13/cobra"
)

func makeBackend(ctx context.Context, cfg *config.Config) (*s3backend.Backend, error) {
	return s3backend.New(ctx, s3backend.Config{
		Bucket: cfg.Bucket, Region: cfg.Region,
		Endpoint: cfg.Endpoint, ForcePathStyle: cfg.ForcePathStyle,
	})
}

// Direct commands that need S3 access without a running daemon.

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

			cfg := &config.Config{
				Bucket: invite.Bucket, Region: invite.Region,
				Endpoint: invite.Endpoint, ForcePathStyle: invite.ForcePathStyle,
				IdentityFile: keyPath,
			}
			if err := config.Save(cfg); err != nil {
				return err
			}

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
			for i := 0; i < 60; i++ {
				granted, err := skyfs.IsGranted(ctx, backend, invite.InviteID)
				if err != nil {
					return err
				}
				if granted {
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
			fmt.Println("\nTimed out. Run 'sky10 fs join' again later.")
			return nil
		},
	}
}
