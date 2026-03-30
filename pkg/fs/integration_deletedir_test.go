//go:build integration

package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Device A deletes a directory. Device B should see files AND directory removed.
func TestIntegrationDeleteDirSyncsAcrossDevices(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "deletedir-sync")

	// A uploads a directory of files
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "photos/a.jpg", strings.NewReader("image-a"))
	storeA.Put(ctx, "photos/sub/b.jpg", strings.NewReader("image-b"))
	storeA.Put(ctx, "photos/sub/c.jpg", strings.NewReader("image-c"))
	storeA.Put(ctx, "keep.txt", strings.NewReader("keep me"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device B: initial sync ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-deletedir-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	// Verify all files downloaded
	for _, name := range []string{"photos/a.jpg", "photos/sub/b.jpg", "photos/sub/c.jpg", "keep.txt"} {
		p := filepath.Join(dirB, filepath.FromSlash(name))
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should exist after initial sync: %v", name, err)
		}
	}

	// --- Device A: set up daemon, seed, then delete the directory ---
	tmpA := t.TempDir()
	dirA := filepath.Join(tmpA, "sync")
	os.MkdirAll(filepath.Join(dirA, "photos", "sub"), 0755)
	os.WriteFile(filepath.Join(dirA, "photos", "a.jpg"), []byte("image-a"), 0644)
	os.WriteFile(filepath.Join(dirA, "photos", "sub", "b.jpg"), []byte("image-b"), 0644)
	os.WriteFile(filepath.Join(dirA, "photos", "sub", "c.jpg"), []byte("image-c"), 0644)
	os.WriteFile(filepath.Join(dirA, "keep.txt"), []byte("keep me"), 0644)

	storeA2 := NewWithDevice(backend, idA, "device-a")
	storeA2.SetNamespace("shared")
	cfgA := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"shared"}},
		DriveID:      "test-deletedir-a",
		ManifestPath: filepath.Join(tmpA, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonA, err := NewDaemonV2_5(storeA2, cfgA, nil)
	if err != nil {
		t.Fatalf("creating daemon A: %v", err)
	}
	// Seed picks up existing files into local log
	daemonA.SyncOnce(ctx)

	// User deletes the photos/ directory
	time.Sleep(time.Second) // ensure later timestamp for delete
	os.RemoveAll(filepath.Join(dirA, "photos"))

	// This is what the watcher would fire — one delete_dir op
	daemonA.watcherHandler.HandleDirectoryTrash("photos")
	daemonA.outboxWorker.drain(ctx)

	// --- Device B: sync again — should get the delete_dir ---
	daemonB.SyncOnce(ctx)

	// All photos/* should be gone — files AND directories
	if _, err := os.Stat(filepath.Join(dirB, "photos")); !os.IsNotExist(err) {
		t.Error("photos/ directory should be removed on device B")
	}
	if _, err := os.Stat(filepath.Join(dirB, "photos", "a.jpg")); !os.IsNotExist(err) {
		t.Error("photos/a.jpg should be removed on device B")
	}
	if _, err := os.Stat(filepath.Join(dirB, "photos", "sub")); !os.IsNotExist(err) {
		t.Error("photos/sub/ directory should be removed on device B")
	}

	// keep.txt should still exist
	if _, err := os.Stat(filepath.Join(dirB, "keep.txt")); err != nil {
		t.Error("keep.txt should still exist on device B")
	}
}

// A delete_dir followed by a new file under the same prefix — new file should sync.
func TestIntegrationDeleteDirThenRecreate(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "deletedir-recreate")

	// A creates initial files
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "dir/old.txt", strings.NewReader("old content"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device B: initial sync ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-recreate-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	if _, err := os.Stat(filepath.Join(dirB, "dir", "old.txt")); err != nil {
		t.Fatal("dir/old.txt should exist after initial sync")
	}

	// --- Device A: daemon deletes the directory ---
	tmpA := t.TempDir()
	dirA := filepath.Join(tmpA, "sync")
	os.MkdirAll(filepath.Join(dirA, "dir"), 0755)
	os.WriteFile(filepath.Join(dirA, "dir", "old.txt"), []byte("old content"), 0644)

	storeA2 := NewWithDevice(backend, idA, "device-a")
	storeA2.SetNamespace("shared")
	cfgA := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"shared"}},
		DriveID:      "test-recreate-a",
		ManifestPath: filepath.Join(tmpA, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonA, err := NewDaemonV2_5(storeA2, cfgA, nil)
	if err != nil {
		t.Fatalf("creating daemon A: %v", err)
	}
	daemonA.SyncOnce(ctx)

	time.Sleep(time.Second)
	os.RemoveAll(filepath.Join(dirA, "dir"))
	daemonA.watcherHandler.HandleDirectoryTrash("dir")
	daemonA.outboxWorker.drain(ctx)

	// B syncs — dir should be gone
	daemonB.SyncOnce(ctx)
	if _, err := os.Stat(filepath.Join(dirB, "dir")); !os.IsNotExist(err) {
		t.Error("dir/ should be removed after delete_dir sync")
	}

	// A creates a new file under the same prefix (later timestamp wins)
	time.Sleep(time.Second)
	storeA.Put(ctx, "dir/new.txt", strings.NewReader("recreated"))

	// B syncs — should get the new file
	daemonB.SyncOnce(ctx)
	data, err := os.ReadFile(filepath.Join(dirB, "dir", "new.txt"))
	if err != nil {
		t.Fatal("dir/new.txt should exist after recreation")
	}
	if string(data) != "recreated" {
		t.Errorf("content = %q, want 'recreated'", string(data))
	}
}

// Device A creates an empty directory. Device B should see it after sync.
func TestIntegrationCreateDirSyncsAcrossDevices(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "createdir-sync")

	// A must upload something first to create the namespace key
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "root.txt", strings.NewReader("hi"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device A: create empty directories, seed picks them up ---
	tmpA := t.TempDir()
	dirA := filepath.Join(tmpA, "sync")
	os.MkdirAll(filepath.Join(dirA, "empty"), 0755)
	os.MkdirAll(filepath.Join(dirA, "nested", "deep"), 0755)
	os.WriteFile(filepath.Join(dirA, "root.txt"), []byte("hi"), 0644)
	cfgA := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"shared"}},
		DriveID:      "test-createdir-a",
		ManifestPath: filepath.Join(tmpA, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonA, err := NewDaemonV2_5(storeA, cfgA, nil)
	if err != nil {
		t.Fatalf("creating daemon A: %v", err)
	}
	daemonA.SyncOnce(ctx)
	// Drain outbox to push create_dir ops to S3
	daemonA.outboxWorker.drain(ctx)

	// --- Device B: sync should create the empty directories ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-createdir-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	// root.txt should exist
	if _, err := os.Stat(filepath.Join(dirB, "root.txt")); err != nil {
		t.Error("root.txt should exist on device B")
	}
	// empty/ should exist (empty directory)
	if _, err := os.Stat(filepath.Join(dirB, "empty")); err != nil {
		t.Error("empty/ should exist on device B")
	}
	// nested/deep/ should exist
	if _, err := os.Stat(filepath.Join(dirB, "nested", "deep")); err != nil {
		t.Error("nested/deep/ should exist on device B")
	}
}

// Regression: create empty dir on A, sync to B, delete empty dir on A, sync to B.
// Previously HandleDirectoryTrash only checked files, so deleting an empty
// dir with only a create_dir entry silently did nothing.
func TestIntegrationDeleteEmptyDirSyncsAcrossDevices(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "delemptydir-sync")

	// A creates namespace key
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "anchor.txt", strings.NewReader("anchor"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device A: create an empty directory ---
	tmpA := t.TempDir()
	dirA := filepath.Join(tmpA, "sync")
	os.MkdirAll(filepath.Join(dirA, "emptydir"), 0755)
	os.WriteFile(filepath.Join(dirA, "anchor.txt"), []byte("anchor"), 0644)

	cfgA := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"shared"}},
		DriveID:      "test-delempty-a",
		ManifestPath: filepath.Join(tmpA, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonA, err := NewDaemonV2_5(storeA, cfgA, nil)
	if err != nil {
		t.Fatalf("creating daemon A: %v", err)
	}
	daemonA.SyncOnce(ctx)
	daemonA.outboxWorker.drain(ctx)

	// --- Device B: initial sync, should get emptydir/ ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-delempty-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	if _, err := os.Stat(filepath.Join(dirB, "emptydir")); err != nil {
		t.Fatal("emptydir/ should exist on B after initial sync")
	}

	// --- Device A: delete the empty directory ---
	time.Sleep(time.Second)
	os.RemoveAll(filepath.Join(dirA, "emptydir"))
	daemonA.watcherHandler.HandleDirectoryTrash("emptydir")
	daemonA.outboxWorker.drain(ctx)

	// --- Device B: sync again, emptydir/ should be gone ---
	daemonB.SyncOnce(ctx)

	if _, err := os.Stat(filepath.Join(dirB, "emptydir")); !os.IsNotExist(err) {
		t.Error("emptydir/ should be removed on B after delete")
	}
	// anchor.txt should still exist
	if _, err := os.Stat(filepath.Join(dirB, "anchor.txt")); err != nil {
		t.Error("anchor.txt should still exist on B")
	}
}
