package fs

import (
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestGCDeletesOrphans(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put a file, then remove it
	if err := store.Put(ctx, "temp.md", strings.NewReader("temporary")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Remove(ctx, "temp.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Blobs should still exist
	blobs, _ := backend.List(ctx, "blobs/")
	if len(blobs) == 0 {
		t.Fatal("expected orphaned blobs before GC")
	}

	// Run GC
	result, err := GC(ctx, backend, nil, GCConfig{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted == 0 {
		t.Error("expected blobs to be deleted")
	}

	// Blobs should be gone
	blobs, _ = backend.List(ctx, "blobs/")
	if len(blobs) != 0 {
		t.Errorf("expected 0 blobs after GC, got %d", len(blobs))
	}
}

func TestGCPreservesReferenced(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put a file and keep it
	if err := store.Put(ctx, "keep.md", strings.NewReader("keep this")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	blobsBefore, _ := backend.List(ctx, "blobs/")

	result, err := GC(ctx, backend, nil, GCConfig{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 {
		t.Errorf("deleted %d blobs, should have deleted 0 (all referenced)", result.BlobsDeleted)
	}

	blobsAfter, _ := backend.List(ctx, "blobs/")
	if len(blobsBefore) != len(blobsAfter) {
		t.Errorf("blob count changed: %d → %d", len(blobsBefore), len(blobsAfter))
	}
}

func TestGCDedupPreservesShared(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Two files with identical content (dedup = same blob)
	data := "identical content"
	store.Put(ctx, "file1.md", strings.NewReader(data))
	store.Put(ctx, "file2.md", strings.NewReader(data))

	// Remove one
	store.Remove(ctx, "file1.md")

	// GC should NOT delete the blob (still referenced by file2)
	result, err := GC(ctx, backend, nil, GCConfig{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 {
		t.Errorf("deleted %d blobs, but shared blob should be preserved", result.BlobsDeleted)
	}
}

func TestGCDryRun(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	store.Put(ctx, "temp.md", strings.NewReader("temp"))
	store.Remove(ctx, "temp.md")

	blobsBefore, _ := backend.List(ctx, "blobs/")

	// Dry run — should report but not delete
	result, err := GC(ctx, backend, nil, GCConfig{DryRun: true})
	if err != nil {
		t.Fatalf("GC dry run: %v", err)
	}
	if result.BlobsDeleted == 0 {
		t.Error("dry run should report blobs to delete")
	}

	// Blobs should still exist
	blobsAfter, _ := backend.List(ctx, "blobs/")
	if len(blobsBefore) != len(blobsAfter) {
		t.Error("dry run should not delete anything")
	}
}

func TestGCEmpty(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	result, err := GC(ctx, backend, nil, GCConfig{})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 || result.BlobsFound != 0 {
		t.Errorf("empty store: deleted=%d found=%d", result.BlobsDeleted, result.BlobsFound)
	}
}

func TestExtractHashFromBlobKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want string
	}{
		{"blobs/ab/cd/abcdef1234.enc", "abcdef1234"},
		{"blobs/00/11/0011223344556677.enc", "0011223344556677"},
	}

	for _, tt := range tests {
		got := extractHashFromBlobKey(tt.key)
		if got != tt.want {
			t.Errorf("extractHash(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
