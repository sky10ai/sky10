package commands

import (
	"context"
	"fmt"

	"github.com/sky10/sky10/pkg/adapter"
	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	"github.com/spf13/cobra"
)

// makeBackend returns an S3 backend when storage is configured, or nil
// when running in S3-free (P2P-only) mode.
func makeBackend(ctx context.Context, cfg *config.Config) (adapter.Backend, error) {
	if !cfg.HasStorage() {
		return nil, nil
	}
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

			cfg := &config.Config{
				Bucket: bucket, Region: region, Endpoint: endpoint,
				ForcePathStyle: pathStyle,
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
			idStore, err := skyid.NewStore()
			if err != nil {
				return err
			}
			bundle, err := skyid.SyncIdentity(ctx, idStore, backend, skyfs.GetDeviceName())
			if err != nil {
				return err
			}
			skyfs.RegisterDevice(ctx, backend, bundle.DeviceID(), bundle.DevicePubKeyHex(), skyfs.GetDeviceName(), cmd.Root().Version)

			fmt.Printf("Initialized skyfs\n  Schema:   v%s\n  Identity: %s\n  Bucket:   %s\n",
				skyfs.SchemaVersion, bundle.Address(), cfg.Bucket)
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
