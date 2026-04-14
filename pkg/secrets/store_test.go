package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	s3backend "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

func TestStorePutDefaultsAndGetByName(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)

	summary, err := store.Put(context.Background(), PutParams{
		Name:    "wallet-defaults",
		Payload: []byte("bootstrap mnemonic"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if summary.Kind != KindBlob {
		t.Fatalf("Kind = %q, want %q", summary.Kind, KindBlob)
	}
	if summary.Scope != ScopeCurrent {
		t.Fatalf("Scope = %q, want %q", summary.Scope, ScopeCurrent)
	}
	if summary.ContentType != "application/octet-stream" {
		t.Fatalf("ContentType = %q", summary.ContentType)
	}
	if !sameStringSet(summary.RecipientDeviceIDs, []string{bundle.DeviceID()}) {
		t.Fatalf("RecipientDeviceIDs = %v, want current device %s", summary.RecipientDeviceIDs, bundle.DeviceID())
	}

	got, err := store.Get("wallet-defaults", Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("Get by name: %v", err)
	}
	if !bytes.Equal(got.Payload, []byte("bootstrap mnemonic")) {
		t.Fatalf("payload mismatch")
	}
}

func TestStoreRoundTripDoesNotLeakPlaintext(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	dir := t.TempDir()
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: dir}, nil)

	payload := []byte("ows backup bytes that should never hit the kv log in plaintext")
	summary, err := store.Put(context.Background(), PutParams{
		Name:    "agent-treasury-backup",
		Kind:    KindOWSBackup,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload mismatch")
	}

	logPath := filepath.Join(dir, "transport", "kv-ops.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logPath, err)
	}
	for _, needle := range [][]byte{
		[]byte("agent-treasury-backup"),
		payload,
	} {
		if bytes.Contains(raw, needle) {
			t.Fatalf("local KV log leaked plaintext %q", needle)
		}
	}
}

func TestStoreWritesInternalKeyPrefix(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)

	if _, err := store.Put(context.Background(), PutParams{
		Name:    "internal-prefix",
		Payload: []byte("secret"),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if got := store.transport.List(headPrefix); len(got) != 1 {
		t.Fatalf("headPrefix keys = %v, want 1 current head key", got)
	}
	if got := store.transport.List(legacyHeadPrefix); len(got) != 0 {
		t.Fatalf("legacyHeadPrefix keys = %v, want none for new writes", got)
	}
	if got := store.transport.List(versionPrefix); len(got) == 0 {
		t.Fatal("expected version records under current internal prefix")
	}
}

func TestStoreReadsLegacyKeyLayout(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	recipients, err := store.resolveRecipients(ScopeCurrent, nil)
	if err != nil {
		t.Fatalf("resolveRecipients: %v", err)
	}
	payload := []byte("legacy secret")
	dataKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatalf("GenerateSymmetricKey: %v", err)
	}
	ciphertext, err := skykey.Encrypt(payload, dataKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	secretID := "legacy-secret-id"
	versionID := "legacy-version-id"
	now := time.Now().UTC()
	versionPlain := versionPayload{
		SecretID:    secretID,
		VersionID:   versionID,
		Kind:        KindBlob,
		ContentType: "text/plain; charset=utf-8",
		Size:        int64(len(payload)),
		SHA256:      checksumHex(payload),
		ChunkCount:  1,
		CreatedAt:   now,
	}
	versionJSON, err := json.Marshal(versionPlain)
	if err != nil {
		t.Fatalf("Marshal version: %v", err)
	}
	versionValue, err := marshalSealedValue(versionJSON, dataKey, recipients)
	if err != nil {
		t.Fatalf("marshalSealedValue version: %v", err)
	}

	headPlain := headPayload{
		SecretID:           secretID,
		Name:               "legacy-name",
		Kind:               KindBlob,
		ContentType:        "text/plain; charset=utf-8",
		LatestVersionID:    versionID,
		Size:               int64(len(payload)),
		SHA256:             checksumHex(payload),
		CreatedAt:          now,
		UpdatedAt:          now,
		RecipientDeviceIDs: recipientIDs(recipients),
	}
	headJSON, err := json.Marshal(headPlain)
	if err != nil {
		t.Fatalf("Marshal head: %v", err)
	}
	headValue, err := marshalSealedValue(headJSON, dataKey, recipients)
	if err != nil {
		t.Fatalf("marshalSealedValue head: %v", err)
	}

	if err := store.transport.Set(ctx, legacyChunkKey(secretID, versionID, 0), ciphertext); err != nil {
		t.Fatalf("Set legacy chunk: %v", err)
	}
	if err := store.transport.Set(ctx, legacyVersionMetaKey(secretID, versionID), versionValue); err != nil {
		t.Fatalf("Set legacy version meta: %v", err)
	}
	if err := store.transport.Set(ctx, legacyHeadKey(secretID), headValue); err != nil {
		t.Fatalf("Set legacy head: %v", err)
	}

	got, err := store.Get("legacy-name", Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("Get legacy secret: %v", err)
	}
	if got.Scope != ScopeCurrent {
		t.Fatalf("Scope = %q, want %q", got.Scope, ScopeCurrent)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestStoreRecipientFilteringAndRewrap(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := newSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())

	payload := []byte("encrypted ows backup")
	summary, err := storeA.Put(ctx, PutParams{
		Name:               "wallet-a",
		Kind:               KindOWSBackup,
		Payload:            payload,
		RecipientDeviceIDs: []string{bundleA.DeviceID()},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if summary.Scope != ScopeExplicit {
		t.Fatalf("Scope = %q, want %q", summary.Scope, ScopeExplicit)
	}
	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}

	itemsB, err := storeB.List()
	if err != nil {
		t.Fatalf("B List: %v", err)
	}
	if len(itemsB) != 0 {
		t.Fatalf("B should not see decryptable heads, got %d", len(itemsB))
	}

	_, err = storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("B Get error = %v, want ErrAccessDenied", err)
	}

	if _, err := storeA.Rewrap(ctx, RewrapParams{
		IDOrName:           summary.ID,
		RecipientDeviceIDs: []string{bundleA.DeviceID(), bundleB.DeviceID()},
	}); err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce after rewrap: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce after rewrap: %v", err)
	}

	got, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("B Get after rewrap: %v", err)
	}
	if !bytes.Equal(got.Payload, payload) {
		t.Fatalf("B payload mismatch")
	}
}

func TestStorePutByNameCreatesNewVersionAndPreservesMetadata(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	first, err := store.Put(ctx, PutParams{
		Name:               "wallet-versioned",
		Kind:               KindOWSBackup,
		ContentType:        "application/ows+tar",
		Payload:            []byte("first version"),
		RecipientDeviceIDs: []string{bundle.DeviceID()},
		Policy: AccessPolicy{
			AllowedAgents: []string{"A-allowed"},
		},
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	second, err := store.Put(ctx, PutParams{
		Name:    "wallet-versioned",
		Payload: []byte("second version"),
	})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("ID changed: first=%s second=%s", first.ID, second.ID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt changed: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if second.UpdatedAt.Before(first.UpdatedAt) {
		t.Fatalf("UpdatedAt regressed: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
	if second.Kind != first.Kind {
		t.Fatalf("Kind changed: first=%q second=%q", first.Kind, second.Kind)
	}
	if second.Scope != first.Scope {
		t.Fatalf("Scope changed: first=%q second=%q", first.Scope, second.Scope)
	}
	if second.ContentType != first.ContentType {
		t.Fatalf("ContentType changed: first=%q second=%q", first.ContentType, second.ContentType)
	}
	if !sameStringSet(second.RecipientDeviceIDs, first.RecipientDeviceIDs) {
		t.Fatalf("RecipientDeviceIDs changed: first=%v second=%v", first.RecipientDeviceIDs, second.RecipientDeviceIDs)
	}
	if !sameStringSet(second.Policy.AllowedAgents, first.Policy.AllowedAgents) {
		t.Fatalf("AllowedAgents changed: first=%v second=%v", first.Policy.AllowedAgents, second.Policy.AllowedAgents)
	}
	if second.Policy.RequireApproval != first.Policy.RequireApproval {
		t.Fatalf("RequireApproval changed: first=%v second=%v", first.Policy.RequireApproval, second.Policy.RequireApproval)
	}

	got, err := store.Get("wallet-versioned", Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("Get latest by name: %v", err)
	}
	if got.ID != first.ID {
		t.Fatalf("Get returned unexpected secret ID %s", got.ID)
	}
	if !bytes.Equal(got.Payload, []byte("second version")) {
		t.Fatalf("Get payload mismatch: %q", got.Payload)
	}
}

func TestStoreDeleteRemovesHeadAndVersions(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	first, err := store.Put(ctx, PutParams{
		Name:    "delete-me",
		Payload: []byte("first"),
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := store.Put(ctx, PutParams{
		Name:    "delete-me",
		Payload: []byte("second"),
	})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("ID changed: first=%s second=%s", first.ID, second.ID)
	}
	if _, err := store.Put(ctx, PutParams{
		Name:    "keep-me",
		Payload: []byte("keep"),
	}); err != nil {
		t.Fatalf("third Put: %v", err)
	}

	if err := store.Delete(ctx, DeleteParams{IDOrName: "delete-me"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.Get(second.ID, Requester{Type: RequesterOwner}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted secret error = %v, want ErrNotFound", err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].Name != "keep-me" {
		t.Fatalf("List after delete = %+v, want only keep-me", items)
	}

	if got := store.transport.List(headPrefix); len(got) != 1 {
		t.Fatalf("headPrefix keys after delete = %v, want 1", got)
	}
	if got := store.transport.List(versionPrefix + second.ID + "/"); len(got) != 0 {
		t.Fatalf("version keys for deleted secret = %v, want none", got)
	}
}

func TestAgentPolicySoftGate(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	summary, err := store.Put(ctx, PutParams{
		Name:    "policy-test",
		Payload: []byte("secret"),
		Policy: AccessPolicy{
			AllowedAgents: []string{"A-allowed"},
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := store.Get(summary.ID, Requester{Type: RequesterAgent, ID: "A-denied"}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied agent error = %v, want ErrAccessDenied", err)
	}
	if _, err := store.Get(summary.ID, Requester{Type: RequesterAgent, ID: "A-allowed"}); err != nil {
		t.Fatalf("allowed agent Get: %v", err)
	}
}

func TestStoreAgentPolicyApprovalRequired(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	summary, err := store.Put(ctx, PutParams{
		Name:    "approval-test",
		Payload: []byte("secret"),
		Policy: AccessPolicy{
			AllowedAgents:   []string{"A-allowed"},
			RequireApproval: true,
		},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := store.Get(summary.ID, Requester{Type: RequesterAgent, ID: "A-allowed"}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("allowed agent error = %v, want ErrApprovalRequired", err)
	}
	if _, err := store.Get(summary.ID, Requester{Type: RequesterOwner}); err != nil {
		t.Fatalf("owner Get: %v", err)
	}
}

func TestStoreTrustedScopeExcludesSandboxDevices(t *testing.T) {
	t.Parallel()

	bundleA, bundleB, bundleSandbox := newRoleSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	storeSandbox := New(backend, bundleSandbox, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())
	registerKVDevice(t, backend, bundleSandbox.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:    "wallet-trusted",
		Kind:    KindOWSBackup,
		Scope:   ScopeTrusted,
		Payload: []byte("shared backup"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if summary.Scope != ScopeTrusted {
		t.Fatalf("Scope = %q, want %q", summary.Scope, ScopeTrusted)
	}
	if !sameStringSet(summary.RecipientDeviceIDs, []string{bundleA.DeviceID(), bundleB.DeviceID()}) {
		t.Fatalf("RecipientDeviceIDs = %v", summary.RecipientDeviceIDs)
	}

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}
	if err := storeSandbox.SyncOnce(ctx); err != nil {
		t.Fatalf("sandbox SyncOnce: %v", err)
	}

	got, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("B Get: %v", err)
	}
	if !bytes.Equal(got.Payload, []byte("shared backup")) {
		t.Fatalf("payload mismatch")
	}

	if _, err := storeSandbox.Get(summary.ID, Requester{Type: RequesterOwner}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("sandbox Get error = %v, want ErrAccessDenied", err)
	}
}

func TestStoreReconcileTrustedScopeIncludesNewTrustedDevice(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device A: %v", err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device B: %v", err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "device-a")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatalf("id.New A: %v", err)
	}

	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()
	registerKVDevice(t, backend, bundleA.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:    "wallet-before-join",
		Scope:   ScopeTrusted,
		Payload: []byte("preexisting trusted secret"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !sameStringSet(summary.RecipientDeviceIDs, []string{bundleA.DeviceID()}) {
		t.Fatalf("RecipientDeviceIDs = %v", summary.RecipientDeviceIDs)
	}

	manifest.AddDevice(deviceB.PublicKey, "device-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Re-sign manifest: %v", err)
	}
	registerKVDevice(t, backend, "D-"+skykey.FromPublicKey(deviceB.PublicKey).ShortID())

	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatalf("id.New B: %v", err)
	}
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce before reconcile: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce before reconcile: %v", err)
	}

	if _, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("B Get before reconcile error = %v, want ErrAccessDenied", err)
	}

	rewrapped, err := storeA.ReconcileTrustedScope(ctx)
	if err != nil {
		t.Fatalf("ReconcileTrustedScope: %v", err)
	}
	if rewrapped != 1 {
		t.Fatalf("rewrapped = %d, want 1", rewrapped)
	}

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}

	got, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("B Get after reconcile: %v", err)
	}
	if got.Scope != ScopeTrusted {
		t.Fatalf("Scope = %q, want %q", got.Scope, ScopeTrusted)
	}
	if !bytes.Equal(got.Payload, []byte("preexisting trusted secret")) {
		t.Fatalf("payload mismatch")
	}
}

func TestStoreReconcileTrustedScopeExcludesRemovedTrustedDevice(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := newSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:    "wallet-remove-device",
		Scope:   ScopeTrusted,
		Payload: []byte("trusted secret"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce before removal: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce before removal: %v", err)
	}
	if _, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner}); err != nil {
		t.Fatalf("B Get before removal: %v", err)
	}

	bundleA.Manifest.RemoveDevice(bundleB.Device.PublicKey)
	if err := bundleA.Manifest.Sign(bundleA.Identity.PrivateKey); err != nil {
		t.Fatalf("Re-sign manifest: %v", err)
	}

	rewrapped, err := storeA.ReconcileTrustedScope(ctx)
	if err != nil {
		t.Fatalf("ReconcileTrustedScope: %v", err)
	}
	if rewrapped != 1 {
		t.Fatalf("rewrapped = %d, want 1", rewrapped)
	}

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce after removal: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce after removal: %v", err)
	}
	if _, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("B Get after removal error = %v, want ErrAccessDenied", err)
	}
}

func TestStoreReconcileTrustedScopeExcludesDowngradedDevice(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := newSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:    "wallet-downgrade-device",
		Scope:   ScopeTrusted,
		Payload: []byte("trusted secret"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce before downgrade: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce before downgrade: %v", err)
	}
	if _, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner}); err != nil {
		t.Fatalf("B Get before downgrade: %v", err)
	}

	for i := range bundleA.Manifest.Devices {
		if bytes.Equal(bundleA.Manifest.Devices[i].PublicKey, bundleB.Device.PublicKey) {
			bundleA.Manifest.Devices[i].Role = id.DeviceRoleSandbox
		}
	}
	if err := bundleA.Manifest.Sign(bundleA.Identity.PrivateKey); err != nil {
		t.Fatalf("Re-sign manifest: %v", err)
	}

	rewrapped, err := storeA.ReconcileTrustedScope(ctx)
	if err != nil {
		t.Fatalf("ReconcileTrustedScope: %v", err)
	}
	if rewrapped != 1 {
		t.Fatalf("rewrapped = %d, want 1", rewrapped)
	}

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce after downgrade: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce after downgrade: %v", err)
	}
	if _, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("B Get after downgrade error = %v, want ErrAccessDenied", err)
	}
}

func TestStoreReconcileTrustedScopeLeavesExplicitSecretPinned(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := newSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:               "wallet-explicit-pinned",
		Scope:              ScopeExplicit,
		Payload:            []byte("explicit secret"),
		RecipientDeviceIDs: []string{bundleA.DeviceID(), bundleB.DeviceID()},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	bundleA.Manifest.RemoveDevice(bundleB.Device.PublicKey)
	if err := bundleA.Manifest.Sign(bundleA.Identity.PrivateKey); err != nil {
		t.Fatalf("Re-sign manifest: %v", err)
	}

	rewrapped, err := storeA.ReconcileTrustedScope(ctx)
	if err != nil {
		t.Fatalf("ReconcileTrustedScope: %v", err)
	}
	if rewrapped != 0 {
		t.Fatalf("rewrapped = %d, want 0", rewrapped)
	}

	if err := storeA.SyncOnce(ctx); err != nil {
		t.Fatalf("A SyncOnce: %v", err)
	}
	if err := storeB.SyncOnce(ctx); err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}

	got, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("B Get after reconcile: %v", err)
	}
	if got.Scope != ScopeExplicit {
		t.Fatalf("Scope = %q, want %q", got.Scope, ScopeExplicit)
	}
	if !bytes.Equal(got.Payload, []byte("explicit secret")) {
		t.Fatalf("payload mismatch")
	}
}

func TestStoreReconcileTrustedScopeLeavesCurrentSecretUnchanged(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device A: %v", err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device B: %v", err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "device-a")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatalf("id.New A: %v", err)
	}

	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()
	registerKVDevice(t, backend, bundleA.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:    "wallet-current-only",
		Payload: []byte("current secret"),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if summary.Scope != ScopeCurrent {
		t.Fatalf("Scope = %q, want %q", summary.Scope, ScopeCurrent)
	}

	manifest.AddDevice(deviceB.PublicKey, "device-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Re-sign manifest: %v", err)
	}
	registerKVDevice(t, backend, "D-"+skykey.FromPublicKey(deviceB.PublicKey).ShortID())

	rewrapped, err := storeA.ReconcileTrustedScope(ctx)
	if err != nil {
		t.Fatalf("ReconcileTrustedScope: %v", err)
	}
	if rewrapped != 0 {
		t.Fatalf("rewrapped = %d, want 0", rewrapped)
	}
}

func TestStoreExplicitScopeRequiresRecipients(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)

	_, err := store.Put(context.Background(), PutParams{
		Name:    "wallet-explicit-missing",
		Scope:   ScopeExplicit,
		Payload: []byte("secret"),
	})
	if err == nil {
		t.Fatal("Put succeeded, want error")
	}
	if err.Error() != `scope "explicit" requires at least one recipient device` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreLegacyAllAliasMapsToTrustedScope(t *testing.T) {
	t.Parallel()

	bundleA, bundleB, bundleSandbox := newRoleSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())
	registerKVDevice(t, backend, bundleSandbox.DeviceID())

	summary, err := storeA.Put(context.Background(), PutParams{
		Name:               "wallet-all-legacy",
		Payload:            []byte("shared backup"),
		RecipientDeviceIDs: []string{"all"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if summary.Scope != ScopeTrusted {
		t.Fatalf("Scope = %q, want %q", summary.Scope, ScopeTrusted)
	}
}

func TestStoreRejectsUnknownRecipientDevice(t *testing.T) {
	t.Parallel()

	bundle := newSingleBundle(t)
	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)

	_, err := store.Put(context.Background(), PutParams{
		Name:               "wallet-invalid-recipient",
		Payload:            []byte("secret"),
		RecipientDeviceIDs: []string{"D-missing"},
	})
	if err == nil {
		t.Fatal("Put succeeded, want error")
	}
	if err.Error() != "unknown recipient device: D-missing" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreDevicesExposeRolesAndDefaultTrusted(t *testing.T) {
	t.Parallel()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	current, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate current device: %v", err)
	}
	sandbox, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate sandbox device: %v", err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(current.PublicKey, "current")
	manifest.AddDeviceWithRole(sandbox.PublicKey, "sandbox", id.DeviceRoleSandbox)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}

	bundle, err := id.New(identity, current, manifest)
	if err != nil {
		t.Fatalf("id.New: %v", err)
	}

	store := New(s3backend.NewMemory(), bundle, Config{DataDir: t.TempDir()}, nil)
	devices := store.Devices()
	if len(devices) != 2 {
		t.Fatalf("Devices count = %d, want 2", len(devices))
	}

	byID := make(map[string]Device, len(devices))
	for _, device := range devices {
		byID[device.ID] = device
	}

	currentDevice, ok := byID[bundle.DeviceID()]
	if !ok {
		t.Fatalf("current device %s missing from Devices()", bundle.DeviceID())
	}
	if !currentDevice.Current {
		t.Fatal("current device should be marked current")
	}
	if currentDevice.Role != id.DeviceRoleTrusted {
		t.Fatalf("current device role = %q, want %q", currentDevice.Role, id.DeviceRoleTrusted)
	}

	sandboxID := "D-" + skykey.FromPublicKey(sandbox.PublicKey).ShortID()
	sandboxDevice, ok := byID[sandboxID]
	if !ok {
		t.Fatalf("sandbox device %s missing from Devices()", sandboxID)
	}
	if sandboxDevice.Role != id.DeviceRoleSandbox {
		t.Fatalf("sandbox device role = %q, want %q", sandboxDevice.Role, id.DeviceRoleSandbox)
	}
	if sandboxDevice.Current {
		t.Fatal("sandbox device should not be current")
	}
}

func newSingleBundle(t *testing.T) *id.Bundle {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	device, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device: %v", err)
	}
	manifest := id.NewManifest(identity)
	manifest.AddDevice(device.PublicKey, "device-a")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}
	bundle, err := id.New(identity, device, manifest)
	if err != nil {
		t.Fatalf("id.New: %v", err)
	}
	return bundle
}

func newSharedBundles(t *testing.T) (*id.Bundle, *id.Bundle) {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device A: %v", err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device B: %v", err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "device-a")
	manifest.AddDevice(deviceB.PublicKey, "device-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatalf("id.New A: %v", err)
	}
	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatalf("id.New B: %v", err)
	}
	return bundleA, bundleB
}

func newRoleSharedBundles(t *testing.T) (*id.Bundle, *id.Bundle, *id.Bundle) {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device A: %v", err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate device B: %v", err)
	}
	deviceSandbox, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate sandbox device: %v", err)
	}

	manifest := id.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "device-a")
	manifest.AddDevice(deviceB.PublicKey, "device-b")
	manifest.AddDeviceWithRole(deviceSandbox.PublicKey, "device-sandbox", id.DeviceRoleSandbox)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatalf("Sign manifest: %v", err)
	}

	bundleA, err := id.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatalf("id.New A: %v", err)
	}
	bundleB, err := id.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatalf("id.New B: %v", err)
	}
	bundleSandbox, err := id.New(identity, deviceSandbox, manifest)
	if err != nil {
		t.Fatalf("id.New sandbox: %v", err)
	}
	return bundleA, bundleB, bundleSandbox
}

func registerKVDevice(t *testing.T, backend *s3backend.MemoryBackend, deviceID string) {
	t.Helper()
	data := []byte(`{}`)
	if err := backend.Put(context.Background(), "devices/"+deviceID+".json", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("register device %s: %v", deviceID, err)
	}
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}

	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			return false
		}
	}
	return true
}
