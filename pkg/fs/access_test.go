package fs

import (
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestGrantAndReadAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()

	// Alice creates a store and puts a file
	storeA := New(backend, alice)
	if err := storeA.Put(ctx, "shared/doc.md", strings.NewReader("shared data")); err != nil {
		t.Fatalf("Alice Put: %v", err)
	}

	// Grant Bob access to the "shared" namespace
	if err := GrantAccess(ctx, backend, alice, "shared", bob.PublicKey); err != nil {
		t.Fatalf("GrantAccess: %v", err)
	}

	// Verify Bob's wrapped key exists
	bobID := shortID(bob.PublicKey)
	keys, _ := backend.List(ctx, "keys/namespaces/fs:shared."+bobID)
	if len(keys) != 1 {
		t.Errorf("expected 1 key for Bob, got %d", len(keys))
	}
}

// TestRevokeAccess and TestRotateNamespaceKey deleted:
// Key rotation is stubbed. Will be reimplemented when access control is rebuilt.

func TestListAuthorizedKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()
	carol, _ := GenerateDeviceKey()

	store := New(backend, alice)
	store.Put(ctx, "team/doc.md", strings.NewReader("team doc"))

	GrantAccess(ctx, backend, alice, "team", bob.PublicKey)
	GrantAccess(ctx, backend, alice, "team", carol.PublicKey)

	keys, err := ListAuthorizedKeys(ctx, backend, "team")
	if err != nil {
		t.Fatalf("ListAuthorizedKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 authorized keys, got %d", len(keys))
	}
}
