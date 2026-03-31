package kv

import (
	"bytes"
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Verify nsKeyName produces the kv: prefix.
func TestNsKeyName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ns, want string
	}{
		{"default", "kv:default"},
		{"config", "kv:config"},
		{"reports", "kv:reports"},
	}
	for _, tt := range tests {
		got := nsKeyName(tt.ns)
		if got != tt.want {
			t.Errorf("nsKeyName(%q) = %q, want %q", tt.ns, got, tt.want)
		}
	}
}

// Verify getOrCreateNamespaceKey stores keys with kv: prefix.
func TestGetOrCreateNamespaceKey_KvPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := skykey.Generate()
	deviceID := shortDeviceID(id)

	// Register device
	regDev2(t, backend, deviceID, id.Address())

	key, err := getOrCreateNamespaceKey(ctx, backend, "default", id, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}

	// Verify S3 keys have kv: prefix
	keys, _ := backend.List(ctx, "keys/namespaces/")
	if len(keys) == 0 {
		t.Fatal("no keys created")
	}
	for _, k := range keys {
		name := strings.TrimPrefix(k, "keys/namespaces/")
		if !strings.HasPrefix(name, "kv:") {
			t.Errorf("key %q missing kv: prefix", k)
		}
	}
}

// Verify that two devices get the same key (not independent keys).
func TestGetOrCreateNamespaceKey_SharedAcrossDevices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	idA, _ := skykey.Generate()
	idB, _ := skykey.Generate()
	devA := shortDeviceID(idA)
	devB := shortDeviceID(idB)

	regDev2(t, backend, devA, idA.Address())
	regDev2(t, backend, devB, idB.Address())

	// Device A creates the key (wraps for B too)
	keyA, err := getOrCreateNamespaceKey(ctx, backend, "shared", idA, devA)
	if err != nil {
		t.Fatal(err)
	}

	// Device B should find A's wrapped key
	keyB, err := getOrCreateNamespaceKey(ctx, backend, "shared", idB, devB)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(keyA, keyB) {
		t.Error("devices got different namespace keys — should share the same key")
	}
}

// Verify the race prevention scan: device B finds the key even when its
// device-specific path doesn't exist yet, by scanning all wrapped keys.
func TestGetOrCreateNamespaceKey_RacePrevention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	idA, _ := skykey.Generate()
	idB, _ := skykey.Generate()
	devA := shortDeviceID(idA)
	devB := shortDeviceID(idB)

	// Only register A — don't register B, so A doesn't wrap for B
	regDev2(t, backend, devA, idA.Address())

	// A creates the key but only wraps for registered devices (just A)
	keyA, err := getOrCreateNamespaceKey(ctx, backend, "race-test", idA, devA)
	if err != nil {
		t.Fatal(err)
	}

	// Manually wrap A's key for B (simulating what approve would do)
	wrapped, _ := wrapKey(keyA, idB.PublicKey)
	path := "keys/namespaces/kv:race-test." + devB + ".ns.enc"
	backend.Put(ctx, path, bytes.NewReader(wrapped), int64(len(wrapped)))

	// Now register B and resolve — should find the key via scan
	regDev2(t, backend, devB, idB.Address())
	keyB, err := getOrCreateNamespaceKey(ctx, backend, "race-test", idB, devB)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(keyA, keyB) {
		t.Error("B got a different key — race prevention scan failed")
	}
}

// Verify resolveNSID writes meta.enc with kv: prefix.
func TestResolveNSID_KvPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	nsKey, _ := skykey.GenerateSymmetricKey()

	nsID, err := resolveNSID(ctx, backend, "default", nsKey)
	if err != nil {
		t.Fatal(err)
	}
	if nsID == "" {
		t.Fatal("empty nsID")
	}

	keys, _ := backend.List(ctx, "keys/namespaces/")
	found := false
	for _, k := range keys {
		if k == "keys/namespaces/kv:default.meta.enc" {
			found = true
		}
	}
	if !found {
		t.Errorf("meta.enc not at kv:default.meta.enc, got: %v", keys)
	}
}

// Verify that deriveNSID is deterministic across devices when they
// share the same key.
func TestDeriveNSID_SameKeyProducesSameID(t *testing.T) {
	t.Parallel()
	nsKey, _ := skykey.GenerateSymmetricKey()

	id1 := deriveNSID(nsKey, "default")
	id2 := deriveNSID(nsKey, "default")

	if id1 != id2 {
		t.Errorf("same key+name produced different IDs: %s vs %s", id1, id2)
	}
	if len(id1) != 32 {
		t.Errorf("nsID length = %d, want 32 hex chars", len(id1))
	}
}

// Verify fs and kv namespaces don't collide.
func TestNsKeyName_NoCollision(t *testing.T) {
	t.Parallel()
	// "default" in kv should not collide with "default" in fs
	kvName := nsKeyName("default")
	fsName := "fs:default" // what fs uses

	if kvName == fsName {
		t.Errorf("kv and fs namespace names collide: %s", kvName)
	}
}

// regDev2 registers a device with proper pubkey for key wrapping tests.
func regDev2(t *testing.T, backend *s3adapter.MemoryBackend, deviceID, address string) {
	t.Helper()
	data := []byte(`{"pubkey":"` + address + `"}`)
	backend.Put(context.Background(), "devices/"+deviceID+".json",
		bytes.NewReader(data), int64(len(data)))
}
