package fs

import (
	"context"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/key"
)

func TestSnapshotUploader_RoundTrip(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()

	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "hello.txt", Checksum: "abc",
		Chunks: []string{"c1"}, Size: 100, Namespace: "test",
	})

	encKey, _ := key.GenerateSymmetricKey()
	u := NewSnapshotUploader(backend, localLog, "dev-a", "ns-123", encKey, nil)

	ctx := context.Background()
	if err := u.Upload(ctx); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Verify latest.enc exists
	latestKey := snapshotLatestKey("ns-123", "dev-a")
	rc, err := backend.Get(ctx, latestKey)
	if err != nil {
		t.Fatalf("Get latest: %v", err)
	}
	defer rc.Close()

	// Decrypt and unmarshal
	data, _ := readAll(rc)
	plain, err := Decrypt(data, encKey)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	snap, err := opslog.UnmarshalSnapshot(plain)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}

	if snap.Len() != 1 {
		t.Errorf("snapshot has %d files, want 1", snap.Len())
	}
	fi, ok := snap.Lookup("hello.txt")
	if !ok {
		t.Fatal("hello.txt not in snapshot")
	}
	if fi.Checksum != "abc" {
		t.Errorf("checksum = %q, want abc", fi.Checksum)
	}
}

func TestSnapshotUploader_History(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()

	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "v1.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 10, Namespace: "test",
	})

	encKey, _ := key.GenerateSymmetricKey()
	u := NewSnapshotUploader(backend, localLog, "dev-a", "ns-123", encKey, nil)

	ctx := context.Background()
	u.Upload(ctx)

	// Add another file and upload again
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "v2.txt", Checksum: "h2",
		Chunks: []string{"c2"}, Size: 20, Namespace: "test",
	})
	u.Upload(ctx)

	// Should have latest + at least 1 history snapshot
	keys, _ := backend.List(ctx, "fs/ns-123/snapshots/dev-a/")
	if len(keys) < 2 {
		t.Errorf("expected at least 2 snapshot keys, got %d: %v", len(keys), keys)
	}
}

func TestSnapshotUploader_OnlyLiveFiles(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()

	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "alive.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 10, Namespace: "test",
	})
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "dead.txt", Checksum: "h2",
		Chunks: []string{"c2"}, Size: 10, Namespace: "test",
	})
	// Delete dead.txt
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Delete, Path: "dead.txt", Namespace: "test",
	})

	encKey, _ := key.GenerateSymmetricKey()
	u := NewSnapshotUploader(backend, localLog, "dev-a", "ns-123", encKey, nil)

	ctx := context.Background()
	u.Upload(ctx)

	// Download and verify only alive.txt is in the snapshot
	rc, _ := backend.Get(ctx, snapshotLatestKey("ns-123", "dev-a"))
	data, _ := readAll(rc)
	rc.Close()
	plain, _ := Decrypt(data, encKey)
	snap, _ := opslog.UnmarshalSnapshot(plain)

	if snap.Len() != 1 {
		t.Errorf("snapshot has %d files, want 1 (only alive.txt)", snap.Len())
	}
	if _, ok := snap.Lookup("dead.txt"); ok {
		t.Error("dead.txt should not be in uploaded snapshot")
	}
}

func readAll(rc interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := rc.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
