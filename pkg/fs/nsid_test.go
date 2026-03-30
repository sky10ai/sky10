package fs

import (
	"context"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/key"
)

func TestResolveNSID_GeneratesAndPersists(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	// First call generates a new ID
	nsID1, err := resolveNSID(ctx, backend, "TestDrive", encKey)
	if err != nil {
		t.Fatalf("resolveNSID: %v", err)
	}
	if len(nsID1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("nsID length = %d, want 32", len(nsID1))
	}

	// Second call returns the same ID
	nsID2, err := resolveNSID(ctx, backend, "TestDrive", encKey)
	if err != nil {
		t.Fatalf("resolveNSID second call: %v", err)
	}
	if nsID1 != nsID2 {
		t.Errorf("nsID changed: %q vs %q", nsID1, nsID2)
	}
}

func TestResolveNSID_DifferentNamespaces(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	id1, _ := resolveNSID(ctx, backend, "Drive1", encKey)
	id2, _ := resolveNSID(ctx, backend, "Drive2", encKey)

	if id1 == id2 {
		t.Error("different namespaces should have different IDs")
	}
}

func TestResolveNSID_EncryptedInS3(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	wrongKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	resolveNSID(ctx, backend, "Secret", encKey)

	// Wrong key can't read the meta
	_, err := resolveNSID(ctx, backend, "Secret", wrongKey)
	if err == nil {
		t.Error("wrong key should fail to decrypt meta")
	}
}

func TestResolveNSID_NotHumanReadableInPath(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	ctx := context.Background()

	nsID, _ := resolveNSID(ctx, backend, "MySecretDrive", encKey)

	// The nsID should NOT contain the human name
	if nsID == "MySecretDrive" {
		t.Error("nsID should be opaque, not the human name")
	}
}
