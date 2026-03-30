package fs

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/key"
)

func TestGCDeletesOrphans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	id, _ := GenerateDeviceKey()

	nsID := "test-ns"
	store := New(backend, id)
	store.SetNamespaceID(nsID)

	// Put a file (creates blobs)
	if err := store.Put(ctx, "keep.md", strings.NewReader("keep this")); err != nil {
		t.Fatalf("Put keep: %v", err)
	}
	keepResult := store.LastPutResult()

	// Upload a snapshot that references keep.md
	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "keep.md", Checksum: keepResult.Checksum,
		Chunks: keepResult.Chunks, Size: keepResult.Size, Namespace: nsID,
	})
	u := NewSnapshotUploader(backend, localLog, "dev-a", nsID, encKey, nil)
	if err := u.Upload(ctx); err != nil {
		t.Fatalf("Upload snapshot: %v", err)
	}

	// Put an orphan blob manually (not referenced by any snapshot)
	orphanData := []byte("orphan blob data")
	orphanHash := "deadbeef00112233"
	blobKey := namespacedBlobKey(nsID, orphanHash)
	backend.Put(ctx, blobKey, bytes.NewReader(orphanData), int64(len(orphanData)))

	// Run GC
	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{nsID}})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 1 {
		t.Errorf("expected 1 blob deleted, got %d", result.BlobsDeleted)
	}

	// Orphan blob should be gone
	if _, err := backend.Head(ctx, blobKey); err == nil {
		t.Error("orphan blob should have been deleted")
	}

	// Referenced blobs should still exist
	for _, chunk := range keepResult.Chunks {
		if _, err := backend.Head(ctx, namespacedBlobKey(nsID, chunk)); err != nil {
			t.Errorf("referenced blob %s should still exist", chunk[:12])
		}
	}
}

func TestGCPreservesReferenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	id, _ := GenerateDeviceKey()

	nsID := "test-ns"
	store := New(backend, id)
	store.SetNamespaceID(nsID)

	// Put a file
	if err := store.Put(ctx, "keep.md", strings.NewReader("keep this")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	pr := store.LastPutResult()

	// Upload snapshot with the reference
	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "keep.md", Checksum: pr.Checksum,
		Chunks: pr.Chunks, Size: pr.Size, Namespace: nsID,
	})
	u := NewSnapshotUploader(backend, localLog, "dev-a", nsID, encKey, nil)
	if err := u.Upload(ctx); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	blobsBefore, _ := backend.List(ctx, "fs/"+nsID+"/blobs/")

	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{nsID}})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 {
		t.Errorf("deleted %d blobs, should have deleted 0 (all referenced)", result.BlobsDeleted)
	}

	blobsAfter, _ := backend.List(ctx, "fs/"+nsID+"/blobs/")
	if len(blobsBefore) != len(blobsAfter) {
		t.Errorf("blob count changed: %d -> %d", len(blobsBefore), len(blobsAfter))
	}
}

func TestGCDedupPreservesShared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	id, _ := GenerateDeviceKey()

	nsID := "test-ns"
	store := New(backend, id)
	store.SetNamespaceID(nsID)

	// Two files with identical content (dedup = same blob)
	data := "identical content"
	if err := store.Put(ctx, "file1.md", strings.NewReader(data)); err != nil {
		t.Fatalf("Put file1: %v", err)
	}
	pr1 := store.LastPutResult()

	if err := store.Put(ctx, "file2.md", strings.NewReader(data)); err != nil {
		t.Fatalf("Put file2: %v", err)
	}
	pr2 := store.LastPutResult()

	// Upload snapshot referencing only file2 (simulating file1 deleted)
	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file2.md", Checksum: pr2.Checksum,
		Chunks: pr2.Chunks, Size: pr2.Size, Namespace: nsID,
	})
	u := NewSnapshotUploader(backend, localLog, "dev-a", nsID, encKey, nil)
	if err := u.Upload(ctx); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// GC should NOT delete the blob since file2 still references it
	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{nsID}})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 {
		t.Errorf("deleted %d blobs, but shared blob should be preserved", result.BlobsDeleted)
	}

	// Both files have the same chunks (dedup)
	if pr1.Chunks[0] != pr2.Chunks[0] {
		t.Logf("note: chunks differ (no dedup), test still valid")
	}
}

func TestGCDryRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()

	nsID := "test-ns"

	// Upload empty snapshot (no references)
	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	u := NewSnapshotUploader(backend, localLog, "dev-a", nsID, encKey, nil)
	if err := u.Upload(ctx); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Put an orphan blob manually
	orphanData := []byte("orphan")
	orphanHash := "deadbeef00112233"
	blobKey := namespacedBlobKey(nsID, orphanHash)
	backend.Put(ctx, blobKey, bytes.NewReader(orphanData), int64(len(orphanData)))

	// Dry run should report but not delete
	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{nsID}, DryRun: true})
	if err != nil {
		t.Fatalf("GC dry run: %v", err)
	}
	if result.BlobsDeleted == 0 {
		t.Error("dry run should report blobs to delete")
	}

	// Blob should still exist
	if _, err := backend.Head(ctx, blobKey); err != nil {
		t.Error("dry run should not delete anything")
	}
}

func TestGCEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()

	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{"empty-ns"}})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.BlobsDeleted != 0 || result.BlobsFound != 0 {
		t.Errorf("empty store: deleted=%d found=%d", result.BlobsDeleted, result.BlobsFound)
	}
}

func TestGCExpiredHistorySnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	nsID := "test-ns"

	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 10, Namespace: nsID,
	})
	snap, _ := localLog.Snapshot()
	data, _ := opslog.MarshalSnapshot(snap)
	encrypted, _ := Encrypt(data, encKey)

	// Upload a history snapshot with an old timestamp (expired)
	oldTS := time.Now().AddDate(0, 0, -60).Unix()
	histKey := snapshotHistoryKey(nsID, "dev-a", oldTS)
	backend.Put(ctx, histKey, bytes.NewReader(encrypted), int64(len(encrypted)))

	// Also upload latest so GC has something to scan
	latestKey := snapshotLatestKey(nsID, "dev-a")
	backend.Put(ctx, latestKey, bytes.NewReader(encrypted), int64(len(encrypted)))

	result, err := GC(ctx, backend, encKey, GCConfig{NSIDs: []string{nsID}, RetentionDays: 30})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.SnapshotsDeleted != 1 {
		t.Errorf("expected 1 expired snapshot deleted, got %d", result.SnapshotsDeleted)
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
