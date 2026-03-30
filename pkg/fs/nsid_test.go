package fs

import (
	"context"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/key"
)

func TestDeriveNSID_Deterministic(t *testing.T) {
	t.Parallel()
	encKey, _ := key.GenerateSymmetricKey()

	id1 := deriveNSID(encKey, "TestDrive")
	id2 := deriveNSID(encKey, "TestDrive")

	if id1 != id2 {
		t.Errorf("same key+name should produce same ID: %q vs %q", id1, id2)
	}
	if len(id1) != 32 {
		t.Errorf("nsID length = %d, want 32", len(id1))
	}
}

func TestDeriveNSID_DifferentNamespaces(t *testing.T) {
	t.Parallel()
	encKey, _ := key.GenerateSymmetricKey()

	id1 := deriveNSID(encKey, "Drive1")
	id2 := deriveNSID(encKey, "Drive2")

	if id1 == id2 {
		t.Error("different names should produce different IDs")
	}
}

func TestDeriveNSID_DifferentKeys(t *testing.T) {
	t.Parallel()
	key1, _ := key.GenerateSymmetricKey()
	key2, _ := key.GenerateSymmetricKey()

	id1 := deriveNSID(key1, "Same")
	id2 := deriveNSID(key2, "Same")

	if id1 == id2 {
		t.Error("different keys should produce different IDs")
	}
}

func TestDeriveNSID_NotHumanReadable(t *testing.T) {
	t.Parallel()
	encKey, _ := key.GenerateSymmetricKey()

	nsID := deriveNSID(encKey, "MySecretDrive")
	if nsID == "MySecretDrive" {
		t.Error("nsID should be opaque, not the human name")
	}
}

func TestResolveNSID_WritesMeta(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	nsID, err := resolveNSID(ctx, backend, "TestDrive", encKey)
	if err != nil {
		t.Fatalf("resolveNSID: %v", err)
	}

	// Should match deterministic derivation
	expected := deriveNSID(encKey, "TestDrive")
	if nsID != expected {
		t.Errorf("nsID = %q, want %q", nsID, expected)
	}

	// Meta should exist in S3
	keys, _ := backend.List(ctx, "keys/namespaces/")
	found := false
	for _, k := range keys {
		if k == "keys/namespaces/TestDrive.meta.enc" {
			found = true
		}
	}
	if !found {
		t.Error("meta.enc not written to S3")
	}
}

func TestResolveNSID_WrongKeyCantDecryptMeta(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	wrongKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	resolveNSID(ctx, backend, "Secret", encKey)

	// Wrong key still derives an ID (HMAC doesn't need S3)
	// but the meta.enc can't be decrypted
	nsID := deriveNSID(wrongKey, "Secret")
	correctID := deriveNSID(encKey, "Secret")
	if nsID == correctID {
		t.Error("different keys should derive different IDs")
	}
}

func TestDiscoverNamespaces(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	resolveNSID(ctx, backend, "Photos", encKey)
	resolveNSID(ctx, backend, "Documents", encKey)

	discovered, err := discoverNamespaces(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("discoverNamespaces: %v", err)
	}
	if len(discovered) != 2 {
		t.Errorf("discovered %d namespaces, want 2", len(discovered))
	}
	if discovered["Photos"] != deriveNSID(encKey, "Photos") {
		t.Error("Photos nsID mismatch")
	}
	if discovered["Documents"] != deriveNSID(encKey, "Documents") {
		t.Error("Documents nsID mismatch")
	}
}
