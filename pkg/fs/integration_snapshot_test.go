package fs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// twoDeviceEnv sets up two simulated devices sharing one S3 backend.
type twoDeviceEnv struct {
	backend adapter.Backend
	nsKey   []byte // shared namespace encryption key
	nsID    string

	storeA, storeB         *Store
	devIDA, devIDB         string
	localLogA, localLogB   *opslog.LocalOpsLog
	syncA, syncB           string
	baselinesA, baselinesB *BaselineStore
	uploaderA, uploaderB   *SnapshotUploader
	pollerA, pollerB       *SnapshotPoller
}

func setupTwoDevices(t *testing.T) *twoDeviceEnv {
	t.Helper()
	h := StartMinIO(t)
	if h == nil {
		t.Skip("minio not available")
	}
	bucket := NewTestBucket(t)
	backend := h.Backend(t, bucket)
	ctx := context.Background()
	WriteSchema(ctx, backend)

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := New(backend, idA)
	storeB := New(backend, idB)
	devIDA := stableDeviceID(idA)
	devIDB := stableDeviceID(idB)

	nsID := "shared"
	storeA.SetNamespaceID(nsID)
	storeB.SetNamespaceID(nsID)
	storeA.namespace = nsID // all files use this namespace for key derivation
	storeB.namespace = nsID

	// Register devices
	regDev(t, backend, devIDA)
	regDev(t, backend, devIDB)

	// Shared namespace key — wrap for both devices
	nsKey, _ := GenerateNamespaceKey()
	wA, _ := WrapNamespaceKey(nsKey, idA.PublicKey)
	wB, _ := WrapNamespaceKey(nsKey, idB.PublicKey)
	backend.Put(ctx, "keys/namespaces/fs:"+nsID+"."+devIDA+".ns.enc",
		bytes.NewReader(wA), int64(len(wA)))
	backend.Put(ctx, "keys/namespaces/fs:"+nsID+"."+devIDB+".ns.enc",
		bytes.NewReader(wB), int64(len(wB)))
	backend.Put(ctx, "keys/namespaces/fs:"+nsID+".ns.enc",
		bytes.NewReader(wA), int64(len(wA)))

	dirA := t.TempDir()
	syncA := filepath.Join(dirA, "sync")
	os.MkdirAll(syncA, 0755)
	localLogA := opslog.NewLocalOpsLog(filepath.Join(dirA, "ops.jsonl"), devIDA)
	baselinesA := NewBaselineStore(filepath.Join(dirA, "baselines"))

	dirB := t.TempDir()
	syncB := filepath.Join(dirB, "sync")
	os.MkdirAll(syncB, 0755)
	localLogB := opslog.NewLocalOpsLog(filepath.Join(dirB, "ops.jsonl"), devIDB)
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))

	uploaderA := NewSnapshotUploader(backend, localLogA, devIDA, nsID, nsKey, nil)
	uploaderB := NewSnapshotUploader(backend, localLogB, devIDB, nsID, nsKey, nil)
	pollerA := NewSnapshotPoller(backend, localLogA, devIDA, nsID, nsKey, 30*time.Second, baselinesA, nil)
	pollerB := NewSnapshotPoller(backend, localLogB, devIDB, nsID, nsKey, 30*time.Second, baselinesB, nil)

	return &twoDeviceEnv{
		backend: backend, nsKey: nsKey, nsID: nsID,
		storeA: storeA, storeB: storeB,
		devIDA: devIDA, devIDB: devIDB,
		localLogA: localLogA, localLogB: localLogB,
		syncA: syncA, syncB: syncB,
		baselinesA: baselinesA, baselinesB: baselinesB,
		uploaderA: uploaderA, uploaderB: uploaderB,
		pollerA: pollerA, pollerB: pollerB,
	}
}

func regDev(t *testing.T, backend adapter.Backend, deviceID string) {
	t.Helper()
	data := []byte(`{"pubkey":"sky10` + deviceID + `placeholder"}`)
	backend.Put(context.Background(), "devices/"+deviceID+".json",
		bytes.NewReader(data), int64(len(data)))
}

func TestEndToEnd_TwoDeviceFileSync(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A creates a file
	env.storeA.Put(ctx, "hello.txt", strings.NewReader("hello from A"))
	pr := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "hello.txt", Checksum: pr.Checksum,
		Chunks: pr.Chunks, Size: pr.Size, Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	// B polls and reconciles
	env.pollerB.pollOnce(ctx)

	fi, ok := env.localLogB.Lookup("hello.txt")
	if !ok {
		t.Fatal("B: hello.txt not in CRDT after poll")
	}
	if fi.Checksum != pr.Checksum {
		t.Errorf("B: checksum mismatch")
	}

	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	reconcilerB := NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil)
	reconcilerB.reconcile(ctx)

	data, err := os.ReadFile(filepath.Join(env.syncB, "hello.txt"))
	if err != nil {
		t.Fatalf("B: file not on disk: %v", err)
	}
	if string(data) != "hello from A" {
		t.Errorf("B: got %q, want %q", string(data), "hello from A")
	}
}

func TestEndToEnd_DeletePropagation(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A creates file
	env.storeA.Put(ctx, "temp.txt", strings.NewReader("temporary"))
	pr := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "temp.txt", Checksum: pr.Checksum,
		Chunks: pr.Chunks, Size: pr.Size, Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	// B syncs — gets file
	env.pollerB.pollOnce(ctx)
	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	reconcilerB := NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil)
	reconcilerB.reconcile(ctx)

	if _, err := os.Stat(filepath.Join(env.syncB, "temp.txt")); err != nil {
		t.Fatal("B: temp.txt should exist after first sync")
	}

	// A deletes file
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Delete, Path: "temp.txt", Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	// B polls again — baseline diff detects delete
	env.pollerB.pollOnce(ctx)
	reconcilerB.reconcile(ctx)

	if _, err := os.Stat(filepath.Join(env.syncB, "temp.txt")); err == nil {
		t.Error("B: temp.txt should be deleted after second sync")
	}
}

func TestEndToEnd_DeleteRootPropagation(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	env.storeA.Put(ctx, "agents/lisa/memory.md", strings.NewReader("remember"))
	memory := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "agents/lisa/memory.md", Checksum: memory.Checksum,
		Chunks: memory.Chunks, Size: memory.Size, Namespace: env.nsID,
	})
	env.storeA.Put(ctx, "notes.txt", strings.NewReader("ephemeral"))
	note := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "notes.txt", Checksum: note.Checksum,
		Chunks: note.Chunks, Size: note.Size, Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	env.pollerB.pollOnce(ctx)
	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	reconcilerB := NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil)
	reconcilerB.reconcile(ctx)

	for _, path := range []string{"agents/lisa/memory.md", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(env.syncB, filepath.FromSlash(path))); err != nil {
			t.Fatalf("B: %s should exist after initial sync: %v", path, err)
		}
	}

	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.DeleteRoot, Path: "", Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	env.pollerB.pollOnce(ctx)
	reconcilerB.reconcile(ctx)

	if _, err := os.Stat(env.syncB); err != nil {
		t.Fatalf("B: drive root should remain after delete_root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.syncB, "agents")); !os.IsNotExist(err) {
		t.Error("B: agents/ should be removed after delete_root")
	}
	if _, err := os.Stat(filepath.Join(env.syncB, "notes.txt")); !os.IsNotExist(err) {
		t.Error("B: notes.txt should be removed after delete_root")
	}

	snapB, err := env.localLogB.Snapshot()
	if err != nil {
		t.Fatalf("B: snapshot after delete_root: %v", err)
	}
	if !snapB.RootDeleted() {
		t.Fatal("B: root tombstone should be present after delete_root sync")
	}
	if snapB.Len() != 0 {
		t.Fatalf("B: snapshot should be empty after delete_root, got %d files", snapB.Len())
	}
}

func TestEndToEnd_BidirectionalSync(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A creates file-a
	env.storeA.Put(ctx, "file-a.txt", strings.NewReader("from A"))
	prA := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file-a.txt", Checksum: prA.Checksum,
		Chunks: prA.Chunks, Size: prA.Size, Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	// B creates file-b
	env.storeB.Put(ctx, "file-b.txt", strings.NewReader("from B"))
	prB := env.storeB.LastPutResult()
	env.localLogB.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file-b.txt", Checksum: prB.Checksum,
		Chunks: prB.Chunks, Size: prB.Size, Namespace: env.nsID,
	})
	env.uploaderB.Upload(ctx)

	// Both poll
	env.pollerA.pollOnce(ctx)
	env.pollerB.pollOnce(ctx)

	// Write own files to disk
	os.WriteFile(filepath.Join(env.syncA, "file-a.txt"), []byte("from A"), 0644)
	os.WriteFile(filepath.Join(env.syncB, "file-b.txt"), []byte("from B"), 0644)

	// Both reconcile
	outboxA := NewSyncLog[OutboxEntry](filepath.Join(env.syncA, "..", "outbox.jsonl"))
	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	NewReconciler(env.storeA, env.localLogA, outboxA, env.syncA, nil, nil).reconcile(ctx)
	NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil).reconcile(ctx)

	// A has B's file
	if d, err := os.ReadFile(filepath.Join(env.syncA, "file-b.txt")); err != nil || string(d) != "from B" {
		t.Errorf("A missing file-b.txt or wrong content")
	}
	// B has A's file
	if d, err := os.ReadFile(filepath.Join(env.syncB, "file-a.txt")); err != nil || string(d) != "from A" {
		t.Errorf("B missing file-a.txt or wrong content")
	}
}

func TestEndToEnd_OfflineDeviceCatchesUp(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A creates 5 files while B is offline
	for i := 0; i < 5; i++ {
		name := filepath.Join("docs", strings.Replace(
			time.Now().Format("15-04-05.000"), ".", "-", 1)+".txt")
		env.storeA.Put(ctx, name, strings.NewReader(strings.Repeat("x", 100+i)))
		pr := env.storeA.LastPutResult()
		env.localLogA.AppendLocal(opslog.Entry{
			Type: opslog.Put, Path: name, Checksum: pr.Checksum,
			Chunks: pr.Chunks, Size: pr.Size, Namespace: env.nsID,
		})
		time.Sleep(2 * time.Millisecond) // unique filenames
	}
	env.uploaderA.Upload(ctx)

	// B comes online
	env.pollerB.pollOnce(ctx)

	snapB, _ := env.localLogB.Snapshot()
	if snapB.Len() != 5 {
		t.Errorf("B has %d files, want 5", snapB.Len())
	}

	// Reconcile downloads all files
	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil).reconcile(ctx)

	files, _ := filepath.Glob(filepath.Join(env.syncB, "docs", "*.txt"))
	if len(files) != 5 {
		t.Errorf("B has %d files on disk, want 5", len(files))
	}
}

// Regression: empty files (size=0, chunks=0) must sync across devices.
// The reconciler was skipping them because the chunkless-put guard
// treated all chunks==0 entries as "upload pending."
func TestEndToEnd_EmptyFileSyncs(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A creates an empty file — Put with empty reader
	env.storeA.Put(ctx, "empty.npmignore", strings.NewReader(""))
	pr := env.storeA.LastPutResult()
	env.localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "empty.npmignore", Checksum: pr.Checksum,
		Chunks: pr.Chunks, Size: pr.Size, Namespace: env.nsID,
	})
	env.uploaderA.Upload(ctx)

	// Verify A's snapshot is in S3
	keys, _ := env.backend.List(ctx, "fs/shared/snapshots/")
	t.Logf("S3 snapshots: %v", keys)

	// B polls and reconciles
	env.pollerB.pollOnce(ctx)

	outboxB := NewSyncLog[OutboxEntry](filepath.Join(env.syncB, "..", "outbox.jsonl"))
	NewReconciler(env.storeB, env.localLogB, outboxB, env.syncB, nil, nil).reconcile(ctx)

	// Empty file should exist on B's disk
	info, err := os.Stat(filepath.Join(env.syncB, "empty.npmignore"))
	if err != nil {
		t.Fatalf("B: empty file not created: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("B: size = %d, want 0", info.Size())
	}
}
