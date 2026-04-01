package fs

import (
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// Verify that fs namespace keys are stored with the fs: prefix in S3.
func TestNamespaceKeyUsesPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Creating a store and putting a file triggers key creation
	store.Put(ctx, "test.txt", strings.NewReader("data"))

	keys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range keys {
		name := strings.TrimPrefix(k, "keys/namespaces/")
		if !strings.HasPrefix(name, "fs:") {
			t.Errorf("namespace key %q missing fs: prefix", k)
		}
	}
	if len(keys) == 0 {
		t.Error("no namespace keys created")
	}
}

// Verify that nsKeyName produces the correct prefix.
func TestNsKeyName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		namespace string
		want      string
	}{
		{"Test", "fs:Test"},
		{"Node", "fs:Node"},
		{"default", "fs:default"},
	}
	for _, tt := range tests {
		got := nsKeyName(tt.namespace)
		if got != tt.want {
			t.Errorf("nsKeyName(%q) = %q, want %q", tt.namespace, got, tt.want)
		}
	}
}

// Verify that resolveNSID writes meta.enc with the fs: prefix.
func TestResolveNSID_FsPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	nsID, err := resolveNSID(ctx, backend, "MyDrive", encKey)
	if err != nil {
		t.Fatal(err)
	}
	if nsID == "" {
		t.Fatal("empty nsID")
	}

	keys, _ := backend.List(ctx, "keys/namespaces/")
	found := false
	for _, k := range keys {
		if k == "keys/namespaces/fs:MyDrive.meta.enc" {
			found = true
		}
	}
	if !found {
		t.Errorf("meta.enc should be at fs:MyDrive.meta.enc, got keys: %v", keys)
	}
}

// Verify that two devices sharing a namespace key via approve both get
// fs: prefixed keys.
func TestApproveWrapsFsPrefixedKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()

	// Alice creates a namespace key by putting a file
	storeA := New(backend, alice)
	storeA.Put(ctx, "shared.txt", strings.NewReader("data"))

	// Simulate approve: wrap Alice's keys for Bob
	simulateApprove(t, ctx, backend, alice, bob)

	// Bob should have a fs: prefixed key
	bobID := shortPubkeyID(bob.Address())
	keys, _ := backend.List(ctx, "keys/namespaces/")

	bobKeys := 0
	for _, k := range keys {
		if strings.Contains(k, bobID) {
			if !strings.Contains(k, "fs:") {
				t.Errorf("Bob's key %q missing fs: prefix", k)
			}
			bobKeys++
		}
	}
	if bobKeys == 0 {
		t.Error("Bob has no wrapped keys after approve")
	}

	// Bob should be able to unwrap the key
	storeB := New(backend, bob)
	key, err := storeB.getOrCreateNamespaceKey(ctx, "default")
	if err != nil {
		t.Fatalf("Bob can't get namespace key: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
}

// Verify that wrapKeyForAllDevices uses the fs: prefix.
func TestWrapKeyForAllDevices_FsPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()

	// Register both devices
	RegisterDevice(ctx, backend, alice.Address(), alice.Address(), "alice-mac", "test")
	RegisterDevice(ctx, backend, bob.Address(), bob.Address(), "bob-mac", "test")

	storeA := New(backend, alice)
	storeA.Put(ctx, "docs/file.txt", strings.NewReader("content"))

	// All namespace keys should have fs: prefix
	keys, _ := backend.List(ctx, "keys/namespaces/")
	for _, k := range keys {
		name := strings.TrimPrefix(k, "keys/namespaces/")
		if strings.HasSuffix(name, ".ns.enc") && !strings.HasPrefix(name, "fs:") {
			t.Errorf("wrapped key %q missing fs: prefix", k)
		}
	}
}

// Verify GrantAccess uses fs: prefix.
func TestGrantAccess_FsPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()

	storeA := New(backend, alice)
	storeA.Put(ctx, "secret.txt", strings.NewReader("secret"))

	if err := GrantAccess(ctx, backend, alice, "default", bob.PublicKey); err != nil {
		t.Fatal(err)
	}

	bobID := shortID(bob.PublicKey)
	expectedPath := "keys/namespaces/fs:default." + bobID + ".ns.enc"
	if _, err := backend.Head(ctx, expectedPath); err != nil {
		t.Errorf("expected wrapped key at %s, not found", expectedPath)
	}
}

// Verify ListAuthorizedKeys works with fs: prefix.
func TestListAuthorizedKeys_FsPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()
	carol, _ := GenerateDeviceKey()

	store := New(backend, alice)
	store.Put(ctx, "team.txt", strings.NewReader("team"))

	GrantAccess(ctx, backend, alice, "default", bob.PublicKey)
	GrantAccess(ctx, backend, alice, "default", carol.PublicKey)

	keys, err := ListAuthorizedKeys(ctx, backend, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 authorized keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !strings.Contains(k, "fs:default.") {
			t.Errorf("authorized key %q missing fs: prefix", k)
		}
	}
}
