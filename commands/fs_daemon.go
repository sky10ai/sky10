package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
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

func fsJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <invite-code>",
		Short: "Join using an invite code (P2P or S3)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := args[0]
			if strings.HasPrefix(code, "sky10p2p_") {
				return joinP2P(cmd, code)
			}
			return joinS3(cmd, code)
		},
	}
}

// joinP2P handles joining via the P2P invite flow (no S3 required).
func joinP2P(cmd *cobra.Command, code string) error {
	invite, err := link.DecodeP2PInvite(code)
	if err != nil {
		return err
	}
	fmt.Printf("Joining %s via P2P\n", invite.Address)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Generate local identity (device key only — identity comes from inviter).
	idStore, err := skyid.NewStore()
	if err != nil {
		return err
	}
	deviceKey, err := skykey.Generate()
	if err != nil {
		return fmt.Errorf("generating device key: %w", err)
	}

	// Create a temporary bundle with just the device key for the P2P node.
	tmpManifest := skyid.NewManifest(deviceKey)
	tmpManifest.AddDevice(deviceKey.PublicKey, skyfs.GetDeviceName())
	tmpManifest.Sign(deviceKey.PrivateKey)
	tmpBundle, err := skyid.New(deviceKey, deviceKey, tmpManifest)
	if err != nil {
		return err
	}

	// Start P2P node to connect to inviter.
	node, err := link.New(tmpBundle, link.Config{Mode: link.Private}, nil)
	if err != nil {
		return fmt.Errorf("creating P2P node: %w", err)
	}
	go node.Run(ctx)
	for node.Host() == nil {
		time.Sleep(50 * time.Millisecond)
	}

	resolver := link.NewResolver(node, link.WithNostr(invite.NostrRelays))

	fmt.Println("Connecting to inviter...")
	resp, err := link.RequestJoin(ctx, node, resolver, invite, deviceKey.Address(), skyfs.GetDeviceName())
	if err != nil {
		return fmt.Errorf("join request failed: %w", err)
	}

	if !resp.Approved {
		errMsg := "denied"
		if resp.Error != "" {
			errMsg = resp.Error
		}
		return fmt.Errorf("join request was %s", errMsg)
	}

	// Unwrap the identity key from the response.
	var wrappedIdentity []byte
	if err := json.Unmarshal(resp.IdentityKey, &wrappedIdentity); err != nil {
		return fmt.Errorf("parsing identity key: %w", err)
	}
	identityPriv, err := skykey.UnwrapKey(wrappedIdentity, deviceKey.PrivateKey)
	if err != nil {
		return fmt.Errorf("unwrapping identity key: %w", err)
	}

	// Reconstruct the identity key from the address + private key.
	identityKey, err := skykey.ParseAddress(invite.Address)
	if err != nil {
		return fmt.Errorf("parsing inviter address: %w", err)
	}
	identityKey.PrivateKey = identityPriv

	// Parse the received manifest.
	var manifest skyid.DeviceManifest
	if err := json.Unmarshal(resp.Manifest, &manifest); err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}

	// Build and save the final bundle.
	bundle, err := skyid.New(identityKey, deviceKey, &manifest)
	if err != nil {
		return err
	}
	if err := idStore.Save(bundle); err != nil {
		return err
	}

	// Cache namespace keys locally so KV sync works on next serve.
	deviceID := kv.ShortDeviceID(identityKey)
	for _, nsk := range resp.NSKeys {
		nsKey, err := skykey.UnwrapKey(nsk.Wrapped, identityKey.PrivateKey)
		if err != nil {
			fmt.Printf("  Warning: could not unwrap key for namespace %q\n", nsk.Namespace)
			continue
		}
		kv.CacheKeyLocally(nsk.Namespace, deviceID, nsKey)
		fmt.Printf("  Namespace key: %s\n", nsk.Namespace)
	}

	// Save config with Nostr relays (no S3 needed).
	cfg := &config.Config{NostrRelays: invite.NostrRelays}
	if err := config.Save(cfg); err != nil {
		return err
	}

	fmt.Printf("Joined!\n  Identity: %s\n  Device:   %s\n", bundle.Address(), bundle.DeviceID())
	return nil
}

// joinS3 handles the legacy S3 invite flow.
func joinS3(cmd *cobra.Command, code string) error {
	invite, err := skyfs.DecodeInvite(code)
	if err != nil {
		return err
	}
	fmt.Printf("Joining bucket %s at %s\n", invite.Bucket, invite.Endpoint)

	cfg := &config.Config{
		Bucket: invite.Bucket, Region: invite.Region,
		Endpoint: invite.Endpoint, ForcePathStyle: invite.ForcePathStyle,
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

	idStore, err := skyid.NewStore()
	if err != nil {
		return err
	}
	bundle, err := skyid.SyncIdentity(ctx, idStore, backend, skyfs.GetDeviceName())
	if err != nil {
		return err
	}
	fmt.Printf("Identity: %s\n", bundle.Address())

	if err := skyfs.SubmitJoin(ctx, backend, invite.InviteID, bundle.Address()); err != nil {
		return fmt.Errorf("submitting join request: %w", err)
	}

	fmt.Println("Join request submitted. Waiting for approval...")
	for i := 0; i < 60; i++ {
		granted, err := skyfs.IsGranted(ctx, backend, invite.InviteID)
		if err != nil {
			return err
		}
		if granted {
			skyfs.RegisterDevice(ctx, backend, bundle.DeviceID(), bundle.DevicePubKeyHex(), skyfs.GetDeviceName(), cmd.Root().Version)
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
}
