package id

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

// TestSyncIdentityFirstDevice verifies that the first device to call
// SyncIdentity publishes its identity key to S3 and returns a valid bundle.
func TestSyncIdentityFirstDevice(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))
	storeA := NewStoreAt(t.TempDir())

	bundleA, err := SyncIdentity(context.Background(), storeA, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	if bundleA.Address() == "" {
		t.Error("expected non-empty identity address")
	}
	if bundleA.Address() == bundleA.DeviceAddress() {
		t.Error("identity and device addresses should differ")
	}
	if !bundleA.Manifest.Verify(bundleA.Identity.PublicKey) {
		t.Error("manifest should be valid")
	}

	// Identity key should be published in S3.
	keys, err := backend.List(context.Background(), "identity/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Error("expected identity key to be published in S3")
	}
}

// TestSyncIdentitySecondDeviceAdoptsFirst verifies that when a second
// device calls SyncIdentity, it adopts the first device's identity — NOT
// its own independently generated one.
func TestSyncIdentitySecondDeviceAdoptsFirst(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))

	// Device A initializes first.
	storeA := NewStoreAt(t.TempDir())
	bundleA, err := SyncIdentity(context.Background(), storeA, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	// Device B initializes second — should adopt A's identity.
	storeB := NewStoreAt(t.TempDir())
	bundleB, err := SyncIdentity(context.Background(), storeB, backend, "phone")
	if err != nil {
		t.Fatal(err)
	}

	// CRITICAL: both devices must have the same identity address.
	if bundleA.Address() != bundleB.Address() {
		t.Errorf("identity mismatch: device A = %s, device B = %s",
			bundleA.Address(), bundleB.Address())
	}

	// Device keys must differ.
	if bundleA.DeviceAddress() == bundleB.DeviceAddress() {
		t.Error("device addresses should differ")
	}

	// Both bundles' identity private keys must be the same.
	if !bundleA.Identity.PrivateKey.Equal(bundleB.Identity.PrivateKey) {
		t.Error("identity private keys should be identical")
	}

	// Both devices should be in B's manifest (B is the latest).
	if !bundleB.Manifest.HasDevice(bundleB.Device.PublicKey) {
		t.Error("B's manifest should contain B's device")
	}
}

// TestSyncIdentityThreeDevicesAllConverge verifies that three devices
// independently calling SyncIdentity all end up with the same identity.
func TestSyncIdentityThreeDevicesAllConverge(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))
	ctx := context.Background()

	stores := make([]*Store, 3)
	bundles := make([]*Bundle, 3)
	names := []string{"laptop", "phone", "server"}

	for i := 0; i < 3; i++ {
		stores[i] = NewStoreAt(t.TempDir())
		var err error
		bundles[i], err = SyncIdentity(ctx, stores[i], backend, names[i])
		if err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}

	// All three should share the same identity address.
	for i := 1; i < 3; i++ {
		if bundles[i].Address() != bundles[0].Address() {
			t.Errorf("device %d address %s != device 0 address %s",
				i, bundles[i].Address(), bundles[0].Address())
		}
	}

	// All three should have unique device addresses.
	seen := make(map[string]bool)
	for i, b := range bundles {
		if seen[b.DeviceAddress()] {
			t.Errorf("device %d has duplicate device address", i)
		}
		seen[b.DeviceAddress()] = true
	}
}

// TestSyncIdentityNamespaceKeySharing verifies that after sync, both
// devices can unwrap a namespace key wrapped for the shared identity.
func TestSyncIdentityNamespaceKeySharing(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))
	ctx := context.Background()

	storeA := NewStoreAt(t.TempDir())
	bundleA, err := SyncIdentity(ctx, storeA, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	storeB := NewStoreAt(t.TempDir())
	bundleB, err := SyncIdentity(ctx, storeB, backend, "phone")
	if err != nil {
		t.Fatal(err)
	}

	// Wrap a namespace key for the shared identity.
	nsKey, _ := skykey.GenerateSymmetricKey()
	wrapped, err := skykey.WrapKey(nsKey, bundleA.Identity.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	// Both devices should be able to unwrap.
	unwrappedA, err := skykey.UnwrapKey(wrapped, bundleA.Identity.PrivateKey)
	if err != nil {
		t.Fatalf("device A unwrap: %v", err)
	}
	unwrappedB, err := skykey.UnwrapKey(wrapped, bundleB.Identity.PrivateKey)
	if err != nil {
		t.Fatalf("device B unwrap: %v", err)
	}

	if !bytes.Equal(unwrappedA, nsKey) || !bytes.Equal(unwrappedB, nsKey) {
		t.Error("both devices should unwrap to the same namespace key")
	}
}

// TestSyncIdentityMigrationAdoptsExisting verifies that a device with a
// legacy key.json adopts an existing S3 identity rather than promoting
// its own key.
func TestSyncIdentityMigrationAdoptsExisting(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))
	ctx := context.Background()

	// Device A initializes first (no legacy key).
	storeA := NewStoreAt(t.TempDir())
	bundleA, err := SyncIdentity(ctx, storeA, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	// Device B has a legacy key.json (different key from A's identity).
	dirB := t.TempDir()
	legacyKey, _ := skykey.Generate()
	skykey.Save(legacyKey, filepath.Join(dirB, legacyFile))
	storeB := NewStoreAt(dirB)

	bundleB, err := SyncIdentity(ctx, storeB, backend, "phone")
	if err != nil {
		t.Fatal(err)
	}

	// B should have adopted A's identity, NOT promoted its own legacy key.
	if bundleB.Address() != bundleA.Address() {
		t.Errorf("migrated device should adopt existing identity: got %s, want %s",
			bundleB.Address(), bundleA.Address())
	}

	// B's identity should NOT be the legacy key.
	if bundleB.Address() == legacyKey.Address() {
		t.Error("migrated device should NOT use its own legacy key as identity")
	}
}

// TestSyncIdentityIdempotent verifies that calling SyncIdentity twice
// on the same device returns the same bundle.
func TestSyncIdentityIdempotent(t *testing.T) {
	h := startMinIO(t)
	if h == nil {
		return
	}
	backend := h.backend(t, newBucket(t))
	ctx := context.Background()

	store := NewStoreAt(t.TempDir())
	bundle1, err := SyncIdentity(ctx, store, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	bundle2, err := SyncIdentity(ctx, store, backend, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	if bundle1.Address() != bundle2.Address() {
		t.Error("identity should be stable across calls")
	}
	if bundle1.DeviceAddress() != bundle2.DeviceAddress() {
		t.Error("device should be stable across calls")
	}
}
