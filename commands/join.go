package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	"github.com/sky10/sky10/pkg/join"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
	"github.com/spf13/cobra"
)

// JoinCmd returns the top-level `sky10 join` command.
func JoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <invite-code>",
		Short: "Join another device's identity using an invite code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := args[0]
			if join.IsP2PInvite(code) {
				return runJoinP2P(cmd, code)
			}
			return runJoinS3(cmd, code)
		},
	}
}

func runJoinP2P(cmd *cobra.Command, code string) error {
	invite, err := join.DecodeP2PInvite(code)
	if err != nil {
		return err
	}
	fmt.Printf("Joining %s via P2P\n", invite.Address)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	idStore, err := skyid.NewStore()
	if err != nil {
		return err
	}
	deviceKey, err := skykey.Generate()
	if err != nil {
		return fmt.Errorf("generating device key: %w", err)
	}

	// Temporary bundle with just the device key for the P2P node.
	tmpManifest := skyid.NewManifest(deviceKey)
	tmpManifest.AddDevice(deviceKey.PublicKey, skyfs.GetDeviceName())
	tmpManifest.Sign(deviceKey.PrivateKey)
	tmpBundle, err := skyid.New(deviceKey, deviceKey, tmpManifest)
	if err != nil {
		return err
	}

	node, err := link.New(tmpBundle, link.Config{Mode: link.Private}, nil)
	if err != nil {
		return fmt.Errorf("creating P2P node: %w", err)
	}
	go node.Run(ctx)
	for node.Host() == nil {
		time.Sleep(50 * time.Millisecond)
	}

	resolver := link.NewResolver(node, link.WithNostr(invite.NostrRelays))

	// Resolve and connect to inviter.
	fmt.Println("Connecting to inviter...")
	info, err := resolver.Resolve(ctx, invite.Address)
	if err != nil {
		return fmt.Errorf("resolving inviter: %w", err)
	}
	if err := node.Host().Connect(ctx, *info); err != nil {
		return fmt.Errorf("connecting to inviter: %w", err)
	}

	resp, err := join.RequestP2PJoin(ctx, node.Host(), info.ID, invite, deviceKey.Address(), skyfs.GetDeviceName())
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
	if !hasNamespaceKey(resp.NSKeys, "default") {
		return fmt.Errorf("join response missing default KV namespace key")
	}

	// Unwrap identity key.
	var wrappedIdentity []byte
	if err := json.Unmarshal(resp.IdentityKey, &wrappedIdentity); err != nil {
		return fmt.Errorf("parsing identity key: %w", err)
	}
	identityPriv, err := skykey.UnwrapKey(wrappedIdentity, deviceKey.PrivateKey)
	if err != nil {
		return fmt.Errorf("unwrapping identity key: %w", err)
	}

	identityKey, err := skykey.ParseAddress(invite.Address)
	if err != nil {
		return fmt.Errorf("parsing inviter address: %w", err)
	}
	identityKey.PrivateKey = identityPriv

	var manifest skyid.DeviceManifest
	if err := json.Unmarshal(resp.Manifest, &manifest); err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}

	bundle, err := skyid.New(identityKey, deviceKey, &manifest)
	if err != nil {
		return err
	}

	// Cache namespace keys locally.
	deviceID := bundle.DeviceID()
	for _, nsk := range resp.NSKeys {
		nsKey, err := skykey.UnwrapKey(nsk.Wrapped, identityKey.PrivateKey)
		if err != nil {
			fmt.Printf("  Warning: could not unwrap key for namespace %q\n", nsk.Namespace)
			continue
		}
		kv.CacheKeyLocally(nsk.Namespace, deviceID, nsKey)
		fmt.Printf("  Namespace key: %s\n", nsk.Namespace)
	}
	if err := idStore.Save(bundle); err != nil {
		return err
	}

	cfg := &config.Config{NostrRelays: invite.NostrRelays}
	if err := config.Save(cfg); err != nil {
		return err
	}

	fmt.Printf("Joined!\n  Identity: %s\n  Device:   %s\n", bundle.Address(), bundle.DeviceID())
	return nil
}

func runJoinS3(cmd *cobra.Command, code string) error {
	invite, err := join.DecodeS3Invite(code)
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

	if err := join.SubmitJoin(ctx, backend, invite.InviteID, bundle.Address()); err != nil {
		return fmt.Errorf("submitting join request: %w", err)
	}

	fmt.Println("Join request submitted. Waiting for approval...")
	for i := 0; i < 60; i++ {
		granted, err := join.IsGranted(ctx, backend, invite.InviteID)
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
	fmt.Println("\nTimed out. Run 'sky10 join' again later.")
	return nil
}
