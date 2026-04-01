package fs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Verify that ApproveJoin wraps keys from ALL modules (fs:, kv:, etc.)
// for the joining device. This is the foundation for "join once, get
// everything" across the sky10 ecosystem.
func TestApproveJoin_WrapsAllModuleKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()
	aliceID := shortPubkeyID(alice.Address())
	bobID := shortPubkeyID(bob.Address())

	// Register both devices
	RegisterDevice(ctx, backend, alice.Address(), alice.Address(), "alice-mac", "test")
	RegisterDevice(ctx, backend, bob.Address(), bob.Address(), "bob-mac", "test")

	// Alice creates an fs namespace key
	storeA := New(backend, alice)
	storeA.Put(ctx, "file.txt", strings.NewReader("data"))

	// Simulate a kv module creating its own namespace key (kv:default)
	kvKey, _ := skykey.GenerateSymmetricKey()
	wrapped, _ := WrapNamespaceKey(kvKey, alice.PublicKey)
	backend.Put(ctx, "keys/namespaces/kv:default."+aliceID+".ns.enc",
		bytes.NewReader(wrapped), int64(len(wrapped)))

	// Simulate a future module (link:main)
	linkKey, _ := skykey.GenerateSymmetricKey()
	wrapped2, _ := WrapNamespaceKey(linkKey, alice.PublicKey)
	backend.Put(ctx, "keys/namespaces/link:main."+aliceID+".ns.enc",
		bytes.NewReader(wrapped2), int64(len(wrapped2)))

	// Alice approves Bob
	if err := ApproveJoin(ctx, backend, alice, bob.Address(), "test-invite"); err != nil {
		t.Fatalf("ApproveJoin: %v", err)
	}

	// Bob should have wrapped keys for ALL modules
	allKeys, _ := backend.List(ctx, "keys/namespaces/")

	bobFS := false
	bobKV := false
	bobLink := false
	for _, k := range allKeys {
		if !strings.Contains(k, bobID) {
			continue
		}
		if strings.Contains(k, "fs:") {
			bobFS = true
		}
		if strings.Contains(k, "kv:") {
			bobKV = true
		}
		if strings.Contains(k, "link:") {
			bobLink = true
		}
	}

	if !bobFS {
		t.Error("Bob missing fs: namespace key after approve")
	}
	if !bobKV {
		t.Error("Bob missing kv: namespace key after approve")
	}
	if !bobLink {
		t.Error("Bob missing link: namespace key after approve")
	}
}

// Verify Bob can actually unwrap all module keys after approve.
func TestApproveJoin_BobCanUnwrapAllKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()
	aliceID := shortPubkeyID(alice.Address())

	RegisterDevice(ctx, backend, alice.Address(), alice.Address(), "alice", "test")
	RegisterDevice(ctx, backend, bob.Address(), bob.Address(), "bob", "test")

	// Create fs key
	storeA := New(backend, alice)
	storeA.Put(ctx, "doc.txt", strings.NewReader("content"))

	// Create kv key
	kvKey, _ := skykey.GenerateSymmetricKey()
	wrapped, _ := WrapNamespaceKey(kvKey, alice.PublicKey)
	backend.Put(ctx, "keys/namespaces/kv:settings."+aliceID+".ns.enc",
		bytes.NewReader(wrapped), int64(len(wrapped)))

	// Approve Bob
	ApproveJoin(ctx, backend, alice, bob.Address(), "inv-123")

	// Bob unwraps fs key
	storeB := New(backend, bob)
	fsKey, err := storeB.getOrCreateNamespaceKey(ctx, "default")
	if err != nil {
		t.Fatalf("Bob can't unwrap fs key: %v", err)
	}
	if len(fsKey) != 32 {
		t.Errorf("fs key length = %d", len(fsKey))
	}

	// Bob unwraps kv key
	bobID := shortPubkeyID(bob.Address())
	kvPath := "keys/namespaces/kv:settings." + bobID + ".ns.enc"
	rc, err := backend.Get(ctx, kvPath)
	if err != nil {
		t.Fatalf("Bob's kv key not found: %v", err)
	}
	kvWrapped, _ := io.ReadAll(rc)
	rc.Close()
	unwrapped, err := UnwrapNamespaceKey(kvWrapped, bob.PrivateKey)
	if err != nil {
		t.Fatalf("Bob can't unwrap kv key: %v", err)
	}
	if !bytes.Equal(unwrapped, kvKey) {
		t.Error("Bob's unwrapped kv key doesn't match Alice's original")
	}
}

// Verify no key collisions between modules using the same namespace name.
func TestModulePrefixes_NoCollision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	deviceID := shortPubkeyID(id.Address())

	RegisterDevice(ctx, backend, id.Address(), id.Address(), "dev", "test")

	// Create keys with same base name but different module prefixes
	key1, _ := skykey.GenerateSymmetricKey()
	key2, _ := skykey.GenerateSymmetricKey()

	w1, _ := WrapNamespaceKey(key1, id.PublicKey)
	w2, _ := WrapNamespaceKey(key2, id.PublicKey)

	backend.Put(ctx, "keys/namespaces/fs:default."+deviceID+".ns.enc",
		bytes.NewReader(w1), int64(len(w1)))
	backend.Put(ctx, "keys/namespaces/kv:default."+deviceID+".ns.enc",
		bytes.NewReader(w2), int64(len(w2)))

	// Both should exist independently
	keys, _ := backend.List(ctx, "keys/namespaces/")
	fsCount, kvCount := 0, 0
	for _, k := range keys {
		if strings.Contains(k, "fs:default") {
			fsCount++
		}
		if strings.Contains(k, "kv:default") {
			kvCount++
		}
	}
	if fsCount != 1 {
		t.Errorf("fs:default keys = %d, want 1", fsCount)
	}
	if kvCount != 1 {
		t.Errorf("kv:default keys = %d, want 1", kvCount)
	}

	// Unwrap should produce different keys
	u1, _ := UnwrapNamespaceKey(w1, id.PrivateKey)
	u2, _ := UnwrapNamespaceKey(w2, id.PrivateKey)
	if bytes.Equal(u1, u2) {
		t.Error("fs and kv keys should be independent, but they're equal")
	}
}
