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
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skyjoin "github.com/sky10/sky10/pkg/join"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv"
	"github.com/sky10/sky10/pkg/link"
)

func configureIdentityRPCHandler(
	handler *skyid.RPCHandler,
	bundle *skyid.Bundle,
	idStore *skyid.Store,
	backend adapter.Backend,
	linkNode *link.Node,
	relays []string,
	refreshPrivateNetwork func(),
) {
	if handler == nil || bundle == nil {
		return
	}

	handler.SetDeviceMetadataProvider(func(ctx context.Context) (map[string]skyid.DeviceMetadata, error) {
		return privateNetworkDeviceMetadata(ctx, bundle, backend, linkNode)
	})
	handler.SetInviteHandler(func(ctx context.Context) (string, error) {
		return createIdentityInvite(ctx, backend, bundle, relays)
	})
	handler.SetJoinHandler(func(ctx context.Context, code string) (interface{}, error) {
		return joinIdentity(ctx, code, bundle, idStore, linkNode)
	})
	handler.SetApproveHandler(func(ctx context.Context) (int, error) {
		return approveIdentityJoins(ctx, backend, bundle)
	})
	handler.SetDeviceRemoveHandler(func(ctx context.Context, devicePubKey string) (interface{}, error) {
		return removeIdentityDevice(ctx, bundle, idStore, backend, devicePubKey, refreshPrivateNetwork)
	})
}

type identityJoinResult struct {
	Status       string `json:"status"`
	Identity     string `json:"identity"`
	DeviceID     string `json:"device_id"`
	DevicePubKey string `json:"device_pubkey"`
	Restarting   bool   `json:"restarting"`
}

func privateNetworkDeviceMetadata(
	ctx context.Context,
	bundle *skyid.Bundle,
	backend adapter.Backend,
	linkNode *link.Node,
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
		if host := linkNode.Host(); host != nil && bundle.Manifest != nil {
			deviceByPeerID := make(map[string]string, len(bundle.Manifest.Devices))
			for _, device := range bundle.Manifest.Devices {
				pid, err := link.PeerIDFromPubKey(device.PublicKey)
				if err != nil {
					continue
				}
				deviceByPeerID[pid.String()] = strings.ToLower(hex.EncodeToString(device.PublicKey))
			}
			for _, pid := range linkNode.ConnectedPrivateNetworkPeers() {
				pubHex, ok := deviceByPeerID[pid.String()]
				if !ok {
					continue
				}
				info := host.Peerstore().PeerInfo(pid)
				meta := metadata[pubHex]
				addrs := make([]string, 0, len(info.Addrs))
				for _, addr := range info.Addrs {
					addrs = append(addrs, addr.String()+"/p2p/"+pid.String())
				}
				meta.Multiaddrs = appendUniqueStrings(meta.Multiaddrs, addrs...)
				if meta.LastSeen == "" {
					meta.LastSeen = time.Now().UTC().Format(time.RFC3339)
				}
				metadata[pubHex] = meta
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

func joinIdentity(
	ctx context.Context,
	code string,
	currentBundle *skyid.Bundle,
	idStore *skyid.Store,
	linkNode *link.Node,
) (interface{}, error) {
	if currentBundle == nil || idStore == nil {
		return nil, fmt.Errorf("identity store not configured")
	}
	if len(currentBundle.Manifest.Devices) > 1 {
		return nil, fmt.Errorf("identity.join requires an unlinked device")
	}

	if skyjoin.IsP2PInvite(code) {
		return joinIdentityP2P(ctx, code, currentBundle, idStore, linkNode)
	}
	return joinIdentityS3(ctx, code, currentBundle, idStore)
}

func joinIdentityP2P(
	ctx context.Context,
	code string,
	currentBundle *skyid.Bundle,
	idStore *skyid.Store,
	linkNode *link.Node,
) (interface{}, error) {
	if linkNode == nil {
		return nil, fmt.Errorf("private-network link node not available")
	}

	invite, err := skyjoin.DecodeP2PInvite(code)
	if err != nil {
		return nil, err
	}
	if err := waitForLinkHost(ctx, linkNode); err != nil {
		return nil, err
	}

	resolver := link.NewResolver(linkNode, link.WithNostr(invite.NostrRelays))
	info, err := resolver.Resolve(ctx, invite.Address)
	if err != nil {
		return nil, fmt.Errorf("resolving inviter: %w", err)
	}
	if err := linkNode.Host().Connect(ctx, *info); err != nil {
		return nil, fmt.Errorf("connecting to inviter: %w", err)
	}

	resp, err := skyjoin.RequestP2PJoin(
		ctx,
		linkNode.Host(),
		info.ID,
		invite,
		currentBundle.Device.Address(),
		skyfs.GetDeviceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("join request failed: %w", err)
	}
	if !resp.Approved {
		errMsg := "denied"
		if strings.TrimSpace(resp.Error) != "" {
			errMsg = strings.TrimSpace(resp.Error)
		}
		return nil, fmt.Errorf("join request was %s", errMsg)
	}

	var wrappedIdentity []byte
	if err := json.Unmarshal(resp.IdentityKey, &wrappedIdentity); err != nil {
		return nil, fmt.Errorf("parsing identity key: %w", err)
	}
	identityPriv, err := skykey.UnwrapKey(wrappedIdentity, currentBundle.Device.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("unwrapping identity key: %w", err)
	}

	identityKey, err := skykey.ParseAddress(invite.Address)
	if err != nil {
		return nil, fmt.Errorf("parsing inviter address: %w", err)
	}
	identityKey.PrivateKey = identityPriv

	var manifest skyid.DeviceManifest
	if err := json.Unmarshal(resp.Manifest, &manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	joinedBundle, err := skyid.New(identityKey, currentBundle.Device, &manifest)
	if err != nil {
		return nil, err
	}
	if err := idStore.Save(joinedBundle); err != nil {
		return nil, fmt.Errorf("saving joined private-network bundle: %w", err)
	}

	deviceID := kv.ShortDeviceID(identityKey)
	for _, nsk := range resp.NSKeys {
		nsKey, err := skykey.UnwrapKey(nsk.Wrapped, identityKey.PrivateKey)
		if err != nil {
			continue
		}
		kv.CacheKeyLocally(nsk.Namespace, deviceID, nsKey)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	cfg.NostrRelays = append([]string(nil), invite.NostrRelays...)
	if err := config.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving config: %w", err)
	}

	scheduleDaemonRestart()
	return identityJoinResult{
		Status:       "joined",
		Identity:     joinedBundle.Address(),
		DeviceID:     joinedBundle.DeviceID(),
		DevicePubKey: joinedBundle.DevicePubKeyHex(),
		Restarting:   true,
	}, nil
}

func joinIdentityS3(
	ctx context.Context,
	code string,
	currentBundle *skyid.Bundle,
	idStore *skyid.Store,
) (interface{}, error) {
	invite, err := skyjoin.DecodeS3Invite(code)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	cfg.Bucket = invite.Bucket
	cfg.Region = invite.Region
	cfg.Endpoint = invite.Endpoint
	cfg.ForcePathStyle = invite.ForcePathStyle
	if err := config.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving config: %w", err)
	}

	os.Setenv("S3_ACCESS_KEY_ID", invite.AccessKey)
	os.Setenv("S3_SECRET_ACCESS_KEY", invite.SecretKey)

	backend, err := makeBackend(ctx, cfg)
	if err != nil {
		return nil, err
	}

	bundle, err := skyid.SyncIdentity(ctx, idStore, backend, skyfs.GetDeviceName())
	if err != nil {
		return nil, err
	}
	if currentBundle != nil && bundle.Address() != currentBundle.Address() && len(currentBundle.Manifest.Devices) > 1 {
		return nil, fmt.Errorf("identity.join would replace an existing private network")
	}

	if err := skyjoin.SubmitJoin(ctx, backend, invite.InviteID, bundle.Address()); err != nil {
		return nil, fmt.Errorf("submitting join request: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		granted, err := skyjoin.IsGranted(waitCtx, backend, invite.InviteID)
		if err != nil {
			return nil, err
		}
		if granted {
			if err := skyfs.RegisterDevice(waitCtx, backend, bundle.DeviceID(), bundle.DevicePubKeyHex(), skyfs.GetDeviceName(), Version); err != nil {
				return nil, fmt.Errorf("registering device: %w", err)
			}
			scheduleDaemonRestart()
			return identityJoinResult{
				Status:       "joined",
				Identity:     bundle.Address(),
				DeviceID:     bundle.DeviceID(),
				DevicePubKey: bundle.DevicePubKeyHex(),
				Restarting:   true,
			}, nil
		}

		select {
		case <-waitCtx.Done():
			return nil, fmt.Errorf("join approval still pending")
		case <-time.After(2 * time.Second):
		}
	}
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

func waitForLinkHost(ctx context.Context, node *link.Node) error {
	deadline := time.Now().Add(10 * time.Second)
	for node.Host() == nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("link node not ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func scheduleDaemonRestart() {
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(75)
	}()
}
