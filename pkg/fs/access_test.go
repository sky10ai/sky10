package fs

import (
	"bytes"
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
	keys, _ := backend.List(ctx, "keys/namespaces/shared."+bobID)
	if len(keys) != 1 {
		t.Errorf("expected 1 key for Bob, got %d", len(keys))
	}
}

func TestRevokeAccess(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	alice, _ := GenerateDeviceKey()
	bob, _ := GenerateDeviceKey()

	storeA := New(backend, alice)
	if err := storeA.Put(ctx, "private/secret.md", strings.NewReader("secret")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Grant then revoke
	GrantAccess(ctx, backend, alice, "private", bob.PublicKey)

	if err := RevokeAccess(ctx, backend, alice, "private", bob.PublicKey); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}

	// Bob's key should be gone
	bobID := shortID(bob.PublicKey)
	keys, _ := backend.List(ctx, "keys/namespaces/private."+bobID)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys for Bob after revoke, got %d", len(keys))
	}

	// Alice should still be able to read (namespace key was rotated)
	storeA2 := New(backend, alice)
	var buf bytes.Buffer
	if err := storeA2.Get(ctx, "private/secret.md", &buf); err != nil {
		t.Fatalf("Alice Get after revoke: %v", err)
	}
	if buf.String() != "secret" {
		t.Errorf("got %q, want %q", buf.String(), "secret")
	}
}

func TestRotateNamespaceKey(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()

	store := New(backend, id)
	if err := store.Put(ctx, "docs/a.md", strings.NewReader("content a")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := store.Put(ctx, "docs/b.md", strings.NewReader("content b")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if err := store.Put(ctx, "other/c.md", strings.NewReader("content c")); err != nil {
		t.Fatalf("Put c: %v", err)
	}

	// Rotate the "docs" namespace key
	if err := RotateNamespaceKey(ctx, backend, id, "docs"); err != nil {
		t.Fatalf("RotateNamespaceKey: %v", err)
	}

	// All files should still be readable with the new key
	store2 := New(backend, id)
	for _, tc := range []struct {
		path string
		want string
	}{
		{"docs/a.md", "content a"},
		{"docs/b.md", "content b"},
		{"other/c.md", "content c"},
	} {
		var buf bytes.Buffer
		if err := store2.Get(ctx, tc.path, &buf); err != nil {
			t.Errorf("Get %s after rotate: %v", tc.path, err)
			continue
		}
		if buf.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.path, buf.String(), tc.want)
		}
	}
}

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
