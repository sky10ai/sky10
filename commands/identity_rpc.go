package commands

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skyjoin "github.com/sky10/sky10/pkg/join"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func configureIdentityRPCHandler(
	handler *skyid.RPCHandler,
	bundle *skyid.Bundle,
	idStore *skyid.Store,
	backend adapter.Backend,
	linkNode *link.Node,
	resolver *link.Resolver,
	relays []string,
	refreshPrivateNetwork func(),
) {
	if handler == nil || bundle == nil {
		return
	}

	handler.SetDeviceMetadataProvider(func(ctx context.Context) (map[string]skyid.DeviceMetadata, error) {
		return privateNetworkDeviceMetadata(ctx, bundle, backend, linkNode, resolver)
	})
	handler.SetInviteHandler(func(ctx context.Context) (string, error) {
		return createIdentityInvite(ctx, backend, bundle, relays)
	})
	handler.SetApproveHandler(func(ctx context.Context) (int, error) {
		return approveIdentityJoins(ctx, backend, bundle)
	})
	handler.SetDeviceRemoveHandler(func(ctx context.Context, devicePubKey string) (interface{}, error) {
		return removeIdentityDevice(ctx, bundle, idStore, backend, devicePubKey, refreshPrivateNetwork)
	})
}

func privateNetworkDeviceMetadata(
	ctx context.Context,
	bundle *skyid.Bundle,
	backend adapter.Backend,
	linkNode *link.Node,
	resolver *link.Resolver,
) (map[string]skyid.DeviceMetadata, error) {
	metadata := make(map[string]skyid.DeviceMetadata)

	if backend != nil {
		devices, err := skyfs.ListDevices(ctx, backend)
		if err == nil {
			for _, device := range devices {
				pubHex, err := canonicalDevicePubKey(device.PubKey)
				if err != nil {
					continue
				}
				meta := metadata[pubHex]
				meta.Alias = device.Alias
				meta.Platform = device.Platform
				meta.IP = device.IP
				meta.Location = device.Location
				meta.Version = device.Version
				meta.LastSeen = device.LastSeen
				meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, device.Multiaddrs...)
				metadata[pubHex] = meta
			}
		}
	}

	if linkNode != nil {
		rec, err := linkNode.CurrentPresenceRecord(0)
		if err == nil {
			meta := metadata[rec.DevicePubKey]
			if meta.Version == "" {
				meta.Version = rec.Version
			}
			if !rec.PublishedAt.IsZero() {
				meta.LastSeen = rec.PublishedAt.UTC().Format(time.RFC3339)
			}
			meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, rec.Multiaddrs...)
			metadata[rec.DevicePubKey] = meta
		}
	}

	if resolver != nil {
		resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		resolution, err := resolver.ResolveAll(resolveCtx, bundle.Address())
		if err == nil {
			for _, peer := range resolution.Peers {
				if peer == nil || peer.Info == nil {
					continue
				}
				meta := metadata[strings.ToLower(peer.DevicePubKey)]
				addrs := make([]string, 0, len(peer.Info.Addrs))
				for _, addr := range peer.Info.Addrs {
					addrs = append(addrs, addr.String())
				}
				meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, addrs...)
				if !peer.PublishedAt.IsZero() {
					meta.LastSeen = peer.PublishedAt.UTC().Format(time.RFC3339)
				}
				metadata[strings.ToLower(peer.DevicePubKey)] = meta
			}
		}
	}

	return metadata, nil
}

func createIdentityInvite(ctx context.Context, backend adapter.Backend, bundle *skyid.Bundle, relays []string) (string, error) {
	if backend == nil {
		return skyjoin.CreateP2PInvite(bundle.Identity.Address(), relays)
	}

	home, _ := os.UserHomeDir()
	cfgData, err := os.ReadFile(filepath.Join(home, ".sky10", "fs", "config.json"))
	var cfg struct {
		Endpoint       string `json:"endpoint"`
		Bucket         string `json:"bucket"`
		Region         string `json:"region"`
		ForcePathStyle bool   `json:"force_path_style"`
	}
	if err == nil {
		_ = json.Unmarshal(cfgData, &cfg)
	}

	return skyjoin.CreateS3Invite(ctx, backend, skyjoin.S3InviteConfig{
		Endpoint:       cfg.Endpoint,
		Bucket:         cfg.Bucket,
		Region:         cfg.Region,
		AccessKey:      os.Getenv("S3_ACCESS_KEY_ID"),
		SecretKey:      os.Getenv("S3_SECRET_ACCESS_KEY"),
		ForcePathStyle: cfg.ForcePathStyle,
		DevicePubKey:   bundle.Identity.Address(),
	})
}

func approveIdentityJoins(ctx context.Context, backend adapter.Backend, bundle *skyid.Bundle) (int, error) {
	if backend == nil {
		return 0, nil
	}

	inviteKeys, err := backend.List(ctx, "invites/")
	if err != nil {
		return 0, err
	}

	inviteIDs := make(map[string]struct{})
	for _, key := range inviteKeys {
		if inviteID := splitInvitePath(key); inviteID != "" {
			inviteIDs[inviteID] = struct{}{}
		}
	}

	approved := 0
	for inviteID := range inviteIDs {
		joinerAddr, err := skyjoin.CheckJoinRequest(ctx, backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := skyjoin.IsGranted(ctx, backend, inviteID)
		if granted && joinerHasAllNamespaceKeys(ctx, backend, bundle.Identity.Address(), joinerAddr) {
			continue
		}
		if err := skyjoin.ApproveJoin(ctx, backend, bundle.Identity, joinerAddr, inviteID); err != nil {
			continue
		}
		approved++
	}

	return approved, nil
}

func removeIdentityDevice(
	ctx context.Context,
	bundle *skyid.Bundle,
	idStore *skyid.Store,
	backend adapter.Backend,
	devicePubKey string,
	refreshPrivateNetwork func(),
) (interface{}, error) {
	if bundle == nil || idStore == nil {
		return nil, fmt.Errorf("identity store not configured")
	}

	pubHex, err := canonicalDevicePubKey(devicePubKey)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(pubHex, bundle.DevicePubKeyHex()) {
		return nil, fmt.Errorf("cannot remove this device")
	}

	raw, err := hex.DecodeString(pubHex)
	if err != nil {
		return nil, fmt.Errorf("decoding device public key: %w", err)
	}
	devicePub := ed25519.PublicKey(raw)
	if !bundle.Manifest.HasDevice(devicePub) {
		return nil, fmt.Errorf("device not found in private network")
	}

	bundle.Manifest.RemoveDevice(devicePub)
	if err := bundle.Manifest.Sign(bundle.Identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing updated private-network membership: %w", err)
	}
	if err := idStore.Save(bundle); err != nil {
		return nil, fmt.Errorf("saving updated private-network membership: %w", err)
	}

	snapshotsDeleted := 0
	if backend != nil {
		deleted, err := deleteDeviceArtifacts(ctx, backend, devicePub)
		if err != nil {
			return nil, err
		}
		snapshotsDeleted = deleted
	}

	if refreshPrivateNetwork != nil {
		go refreshPrivateNetwork()
	}

	return map[string]any{
		"status":            "ok",
		"snapshots_deleted": snapshotsDeleted,
	}, nil
}

func deleteDeviceArtifacts(ctx context.Context, backend adapter.Backend, devicePub ed25519.PublicKey) (int, error) {
	if backend == nil {
		return 0, nil
	}

	deviceID := "D-" + skykey.FromPublicKey(devicePub).ShortID()
	if err := backend.Delete(ctx, "devices/"+deviceID+".json"); err != nil && !errors.Is(err, adapter.ErrNotFound) {
		return 0, err
	}

	allKeys, err := backend.List(ctx, "fs/")
	if err != nil {
		return 0, err
	}

	deleted := 0
	snapshotPath := "/snapshots/" + deviceID + "/"
	for _, key := range allKeys {
		if !strings.Contains(key, snapshotPath) {
			continue
		}
		if err := backend.Delete(ctx, key); err != nil && !errors.Is(err, adapter.ErrNotFound) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func joinerHasAllNamespaceKeys(ctx context.Context, backend adapter.Backend, approverAddress, joinerAddress string) bool {
	joinerID := "D-" + skykey.ShortIDFromAddress(joinerAddress)
	myID := "D-" + skykey.ShortIDFromAddress(approverAddress)

	allKeys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return false
	}

	namespaces := make(map[string]struct{})
	for _, key := range allKeys {
		ns := extractNamespaceName(key)
		if strings.Contains(key, "."+myID+".") || strings.HasSuffix(key, ns+".ns.enc") {
			namespaces[ns] = struct{}{}
		}
	}

	for ns := range namespaces {
		joinerKeyPath := "keys/namespaces/" + ns + "." + joinerID + ".ns.enc"
		if _, err := backend.Head(ctx, joinerKeyPath); err != nil {
			return false
		}
	}
	return true
}

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

func extractNamespaceName(path string) string {
	name := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		name = path[i+1:]
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

func canonicalDevicePubKey(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("device public key required")
	}

	lower := strings.ToLower(trimmed)
	if raw, err := hex.DecodeString(lower); err == nil && len(raw) == ed25519.PublicKeySize {
		return lower, nil
	}

	key, err := skykey.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid device public key: %q", value)
	}
	return hex.EncodeToString(key.PublicKey), nil
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	out := append([]string(nil), existing...)
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
