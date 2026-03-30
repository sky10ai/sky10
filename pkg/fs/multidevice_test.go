package fs

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/key"
)

// simulateApprove simulates the invite approve flow: unwrap A's namespace
// keys and wrap them for B. Used by integration tests across the package.
func simulateApprove(t *testing.T, ctx context.Context, backend adapter.Backend, idA, idB *DeviceKey) {
	t.Helper()
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	bID := shortPubkeyID(idB.Address())

	for _, nsKeyPath := range nsKeys {
		rc, err := backend.Get(ctx, nsKeyPath)
		if err != nil {
			continue
		}
		wrapped, _ := io.ReadAll(rc)
		rc.Close()

		nsKey, err := UnwrapNamespaceKey(wrapped, idA.PrivateKey)
		if err != nil {
			continue
		}

		wrappedForB, err := WrapNamespaceKey(nsKey, idB.PublicKey)
		if err != nil {
			t.Fatalf("wrapping for B: %v", err)
		}

		nsName := extractNamespaceName(nsKeyPath)
		bKeyPath := "keys/namespaces/" + nsName + "." + bID + ".ns.enc"
		r := bytes.NewReader(wrappedForB)
		backend.Put(ctx, bKeyPath, r, int64(len(wrappedForB)))
	}
}

// TestMultiDeviceCrossReadWrite verifies that Device A puts a file,
// uploads a snapshot, and Device B polls and gets the entry in its CRDT.
func TestMultiDeviceCrossReadWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	nsID := "shared-ns"

	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")

	// Device A: put a file and upload snapshot
	idA, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "dev-a")
	storeA.SetNamespaceID(nsID)
	if err := storeA.Put(ctx, "file1.md", strings.NewReader("from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}
	prA := storeA.LastPutResult()

	dirA := t.TempDir()
	localLogA := opslog.NewLocalOpsLog(filepath.Join(dirA, "ops.jsonl"), "dev-a")
	localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "file1.md", Checksum: prA.Checksum,
		Chunks: prA.Chunks, Size: prA.Size, Namespace: nsID,
	})
	uploaderA := NewSnapshotUploader(backend, localLogA, "dev-a", nsID, encKey, nil)
	if err := uploaderA.Upload(ctx); err != nil {
		t.Fatalf("A upload snapshot: %v", err)
	}

	// Device B: poll to discover A's file
	dirB := t.TempDir()
	localLogB := opslog.NewLocalOpsLog(filepath.Join(dirB, "ops.jsonl"), "dev-b")
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))
	pollerB := NewSnapshotPoller(backend, localLogB, "dev-b", nsID, encKey, 30*time.Second, baselinesB, nil)
	pollerB.pollOnce(ctx)

	// B should have file1.md in its CRDT
	fi, ok := localLogB.Lookup("file1.md")
	if !ok {
		t.Fatal("Device B: file1.md not in CRDT after poll")
	}
	if fi.Checksum != prA.Checksum {
		t.Errorf("Device B: checksum = %q, want %q", fi.Checksum, prA.Checksum)
	}
	if len(fi.Chunks) == 0 {
		t.Error("Device B: expected non-empty chunks for file1.md")
	}
}

// TestMultiDeviceBidirectionalSync verifies that both devices can put
// files and see each other's changes via snapshot exchange.
func TestMultiDeviceBidirectionalSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	nsID := "shared-ns"

	idA, _ := GenerateDeviceKey()

	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")

	storeA := NewWithDevice(backend, idA, "dev-a")
	storeA.SetNamespaceID(nsID)
	storeA.Put(ctx, "from-a.md", strings.NewReader("hello from A"))
	prA := storeA.LastPutResult()

	dirA := t.TempDir()
	localLogA := opslog.NewLocalOpsLog(filepath.Join(dirA, "ops.jsonl"), "dev-a")
	localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "from-a.md", Checksum: prA.Checksum,
		Chunks: prA.Chunks, Size: prA.Size, Namespace: nsID,
	})
	uploaderA := NewSnapshotUploader(backend, localLogA, "dev-a", nsID, encKey, nil)
	uploaderA.Upload(ctx)

	dirB := t.TempDir()
	localLogB := opslog.NewLocalOpsLog(filepath.Join(dirB, "ops.jsonl"), "dev-b")
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))
	pollerB := NewSnapshotPoller(backend, localLogB, "dev-b", nsID, encKey, 30*time.Second, baselinesB, nil)
	pollerB.pollOnce(ctx)

	localLogB.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "from-b.md", Checksum: "b-hash",
		Chunks: []string{"b-chunk"}, Size: 50, Namespace: nsID,
	})
	uploaderB := NewSnapshotUploader(backend, localLogB, "dev-b", nsID, encKey, nil)
	uploaderB.Upload(ctx)

	baselinesA := NewBaselineStore(filepath.Join(dirA, "baselines"))
	pollerA := NewSnapshotPoller(backend, localLogA, "dev-a", nsID, encKey, 30*time.Second, baselinesA, nil)
	pollerA.pollOnce(ctx)

	snapA, _ := localLogA.Snapshot()
	if snapA.Len() != 2 {
		t.Errorf("Device A sees %d files, want 2", snapA.Len())
	}
	if _, ok := snapA.Lookup("from-b.md"); !ok {
		t.Error("Device A missing from-b.md")
	}

	snapB, _ := localLogB.Snapshot()
	if snapB.Len() != 2 {
		t.Errorf("Device B sees %d files, want 2", snapB.Len())
	}
	if _, ok := snapB.Lookup("from-a.md"); !ok {
		t.Error("Device B missing from-a.md")
	}
}

// TestMultiDeviceThreeWay verifies three devices can all see each other's files.
func TestMultiDeviceThreeWay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	nsID := "shared-ns"

	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")
	registerTestDevice(t, backend, "dev-c")

	devices := []string{"dev-a", "dev-b", "dev-c"}
	logs := make(map[string]*opslog.LocalOpsLog)

	for _, devID := range devices {
		dir := t.TempDir()
		log := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), devID)
		log.AppendLocal(opslog.Entry{
			Type: opslog.Put, Path: devID + ".md", Checksum: devID + "-hash",
			Chunks: []string{devID + "-chunk"}, Size: 10, Namespace: nsID,
		})
		u := NewSnapshotUploader(backend, log, devID, nsID, encKey, nil)
		u.Upload(ctx)
		logs[devID] = log
	}

	for _, devID := range devices {
		dir := t.TempDir()
		baselines := NewBaselineStore(filepath.Join(dir, "baselines"))
		poller := NewSnapshotPoller(backend, logs[devID], devID, nsID, encKey, 30*time.Second, baselines, nil)
		poller.pollOnce(ctx)
	}

	for _, devID := range devices {
		snap, _ := logs[devID].Snapshot()
		if snap.Len() != 3 {
			t.Errorf("Device %s sees %d files, want 3", devID, snap.Len())
		}
	}
}

// TestMultiDeviceDeletePropagates verifies that a delete on one device
// propagates to another via snapshot exchange.
func TestMultiDeviceDeletePropagates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := key.GenerateSymmetricKey()
	nsID := "shared-ns"

	registerTestDevice(t, backend, "dev-a")
	registerTestDevice(t, backend, "dev-b")

	dirA := t.TempDir()
	localLogA := opslog.NewLocalOpsLog(filepath.Join(dirA, "ops.jsonl"), "dev-a")
	localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "temp.md", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 10, Namespace: nsID,
	})
	uploaderA := NewSnapshotUploader(backend, localLogA, "dev-a", nsID, encKey, nil)
	uploaderA.Upload(ctx)

	dirB := t.TempDir()
	localLogB := opslog.NewLocalOpsLog(filepath.Join(dirB, "ops.jsonl"), "dev-b")
	baselinesB := NewBaselineStore(filepath.Join(dirB, "baselines"))
	pollerB := NewSnapshotPoller(backend, localLogB, "dev-b", nsID, encKey, 30*time.Second, baselinesB, nil)
	pollerB.pollOnce(ctx)

	if _, ok := localLogB.Lookup("temp.md"); !ok {
		t.Fatal("B should have temp.md after first poll")
	}

	localLogA.AppendLocal(opslog.Entry{
		Type: opslog.Delete, Path: "temp.md", Namespace: nsID,
	})
	uploaderA.Upload(ctx)

	pollerB.pollOnce(ctx)

	if _, ok := localLogB.Lookup("temp.md"); ok {
		t.Error("B should not have temp.md after second poll (deleted)")
	}
}
