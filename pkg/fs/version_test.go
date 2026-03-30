package fs

import (
	"context"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// TestListVersions, TestRestoreVersion, TestListSnapshots deleted:
// Old version system is dead. Will be rewritten when history is implemented
// on top of snapshot-exchange.

func TestListVersionsNoVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	versions, err := ListVersions(ctx, store, "nonexistent.md")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %d versions, want 0", len(versions))
	}
}
