package secrets

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

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

func TestStorePutAllRecipientsAllowsOtherDeviceToDecrypt(t *testing.T) {
	t.Parallel()

	bundleA, bundleB := newSharedBundles(t)
	backend := s3backend.NewMemory()
	storeA := New(backend, bundleA, Config{DataDir: t.TempDir()}, nil)
	storeB := New(backend, bundleB, Config{DataDir: t.TempDir()}, nil)
	ctx := context.Background()

	registerKVDevice(t, backend, bundleA.DeviceID())
	registerKVDevice(t, backend, bundleB.DeviceID())

	summary, err := storeA.Put(ctx, PutParams{
		Name:               "wallet-all",
		Kind:               KindOWSBackup,
		Payload:            []byte("shared backup"),
		RecipientDeviceIDs: []string{"all"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
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

	got, err := storeB.Get(summary.ID, Requester{Type: RequesterOwner})
	if err != nil {
		t.Fatalf("B Get: %v", err)
	}
	if !bytes.Equal(got.Payload, []byte("shared backup")) {
		t.Fatalf("payload mismatch")
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
