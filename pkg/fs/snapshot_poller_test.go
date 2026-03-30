package fs

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/key"
)

func TestBaselineStore_SaveLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewBaselineStore(filepath.Join(dir, "baselines"))

	// No baseline initially
	snap, err := store.Load("dev-b")
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Error("expected nil baseline for unknown device")
	}

	// Create and save a snapshot
	tmpLog := opslog.NewLocalOpsLog(filepath.Join(dir, "tmp.jsonl"), "dev-b")
	tmpLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 50, Namespace: "ns",
	})
	s, _ := tmpLog.Snapshot()
	if err := store.Save("dev-b", s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load it back
	loaded, err := store.Load("dev-b")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Len() != 1 {
		t.Errorf("loaded snapshot has %d files, want 1", loaded.Len())
	}

	// DeviceIDs
	ids, _ := store.DeviceIDs()
	if len(ids) != 1 || ids[0] != "dev-b" {
		t.Errorf("DeviceIDs = %v, want [dev-b]", ids)
	}
}

func TestSnapshotPoller_NewFile(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()
	encKey, _ := key.GenerateSymmetricKey()

	// Register devices
	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")

	// Device A uploads a snapshot with one file
	uploadTestSnapshot(t, backend, encKey, "ns-1", "dev-a", map[string]testFile{
		"hello.txt": {checksum: "h1", chunks: []string{"c1"}, size: 100},
	})

	// Device B polls
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-b")
	baselines := NewBaselineStore(filepath.Join(dir, "baselines"))
	poller := NewSnapshotPoller(backend, localLog, "dev-b", "ns-1", encKey, 30*time.Second, baselines, nil)

	ctx := context.Background()
	poller.pollOnce(ctx)

	// Device B should now have hello.txt in its CRDT
	fi, ok := localLog.Lookup("hello.txt")
	if !ok {
		t.Fatal("hello.txt not in local CRDT after poll")
	}
	if fi.Checksum != "h1" {
		t.Errorf("checksum = %q, want h1", fi.Checksum)
	}
}

func TestSnapshotPoller_Delete(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()
	encKey, _ := key.GenerateSymmetricKey()

	// Register devices
	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")

	// Device A has file.txt in first snapshot
	uploadTestSnapshot(t, backend, encKey, "ns-1", "dev-a", map[string]testFile{
		"file.txt": {checksum: "h1", chunks: []string{"c1"}, size: 50},
	})

	// Device B polls (first sync — gets file.txt)
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-b")
	baselines := NewBaselineStore(filepath.Join(dir, "baselines"))
	poller := NewSnapshotPoller(backend, localLog, "dev-b", "ns-1", encKey, 30*time.Second, baselines, nil)

	ctx := context.Background()
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("file.txt"); !ok {
		t.Fatal("file.txt should be in CRDT after first poll")
	}

	// Device A deletes file.txt (uploads snapshot without it)
	uploadTestSnapshot(t, backend, encKey, "ns-1", "dev-a", map[string]testFile{})

	// Device B polls again — baseline diff detects the delete
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("file.txt"); ok {
		t.Error("file.txt should be deleted after second poll")
	}
}

func TestSnapshotPoller_NoBaseline(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()
	encKey, _ := key.GenerateSymmetricKey()

	// Device A has 3 files
	uploadTestSnapshot(t, backend, encKey, "ns-1", "dev-a", map[string]testFile{
		"a.txt": {checksum: "h1", chunks: []string{"c1"}, size: 10},
		"b.txt": {checksum: "h2", chunks: []string{"c2"}, size: 20},
		"c.txt": {checksum: "h3", chunks: []string{"c3"}, size: 30},
	})

	// Device C joins fresh — no baselines
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-c")
	baselines := NewBaselineStore(filepath.Join(dir, "baselines"))
	poller := NewSnapshotPoller(backend, localLog, "dev-c", "ns-1", encKey, 30*time.Second, baselines, nil)

	// Register devices so poller finds them
	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-c")

	ctx := context.Background()
	poller.pollOnce(ctx)

	// All 3 files should be merged
	snap, _ := localLog.Snapshot()
	if snap.Len() != 3 {
		t.Errorf("snapshot has %d files, want 3", snap.Len())
	}
}

func TestSnapshotPoller_SkipsSelf(t *testing.T) {
	t.Parallel()
	backend := s3adapter.NewMemory()
	dir := t.TempDir()
	encKey, _ := key.GenerateSymmetricKey()

	// Only self registered
	registerTestDevice(t, backend, "dev-a")

	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	baselines := NewBaselineStore(filepath.Join(dir, "baselines"))
	poller := NewSnapshotPoller(backend, localLog, "dev-a", "ns-1", encKey, 30*time.Second, baselines, nil)

	ctx := context.Background()
	poller.pollOnce(ctx) // should not crash or merge anything

	snap, _ := localLog.Snapshot()
	if snap.Len() != 0 {
		t.Errorf("expected empty snapshot, got %d files", snap.Len())
	}
}

// --- helpers ---

type testFile struct {
	checksum string
	chunks   []string
	size     int64
}

func uploadTestSnapshot(t *testing.T, backend *s3adapter.MemoryBackend, encKey []byte, nsID, deviceID string, files map[string]testFile) {
	t.Helper()
	dir := t.TempDir()
	log := opslog.NewLocalOpsLog(filepath.Join(dir, deviceID+".jsonl"), deviceID)
	for path, f := range files {
		log.Append(opslog.Entry{
			Type: opslog.Put, Path: path, Checksum: f.checksum,
			Chunks: f.chunks, Size: f.size, Namespace: "ns",
			Device: deviceID, Timestamp: time.Now().Unix(), Seq: 1,
		})
	}
	snap, _ := log.Snapshot()
	data, _ := opslog.MarshalSnapshot(snap)
	encrypted, _ := Encrypt(data, encKey)

	ctx := context.Background()
	key := snapshotLatestKey(nsID, deviceID)
	backend.Put(ctx, key, bytes.NewReader(encrypted), int64(len(encrypted)))
}

func registerTestDevice(t *testing.T, backend *s3adapter.MemoryBackend, deviceID string) {
	t.Helper()
	ctx := context.Background()
	data := []byte(`{"pubkey":"sky10` + deviceID + `placeholder"}`)
	backend.Put(ctx, "devices/"+deviceID+".json", bytes.NewReader(data), int64(len(data)))
}
