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

// Device A deletes a file, Device B should see it removed after sync.
func TestIntegrationDeleteSyncsAcrossDevices(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "delete-sync")

	// A uploads a file
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "to-delete.txt", strings.NewReader("will be deleted"))
	storeA.Put(ctx, "keep.txt", strings.NewReader("stays"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B syncs — gets both files
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	dirB := t.TempDir()
	manifestB := filepath.Join(t.TempDir(), "manifest.json")

	daemonCfgB := DaemonConfig{
		SyncConfig: SyncConfig{
			LocalRoot:  dirB,
			Namespaces: []string{"shared"},
		},
		DriveID:      "test-delete",
		ManifestPath: manifestB,
		PollSeconds:  30,
	}
	dB, _ := NewDaemon(storeB, nil, daemonCfgB, nil)
	dB.threeWaySync(ctx)

	// Verify both files downloaded
	if _, err := os.Stat(filepath.Join(dirB, "to-delete.txt")); err != nil {
		t.Fatal("to-delete.txt should exist after first sync")
	}

	// A deletes the file (ensure later timestamp)
	time.Sleep(time.Second)
	storeA.Remove(ctx, "to-delete.txt")

	// B syncs again — should remove the deleted file
	dB.threeWaySync(ctx)

	if _, err := os.Stat(filepath.Join(dirB, "to-delete.txt")); !os.IsNotExist(err) {
		t.Error("to-delete.txt should be removed after remote delete")
	}
	if _, err := os.Stat(filepath.Join(dirB, "keep.txt")); err != nil {
		t.Error("keep.txt should still exist")
	}
}

// B creates a file while A is offline. A gets it on restart.
func TestIntegrationOfflineFileSync(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "offline-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("docs")
	storeA.Put(ctx, "existing.txt", strings.NewReader("was here"))

	simulateApprove(t, ctx, backend, idA, idB)

	// A does initial sync
	dirA := t.TempDir()
	manifestA := filepath.Join(t.TempDir(), "manifest.json")
	dA, _ := NewDaemon(storeA, nil, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"docs"}},
		DriveID:      "test-offline",
		ManifestPath: manifestA,
		PollSeconds:  30,
	}, nil)
	dA.threeWaySync(ctx)

	// B uploads while A is "offline" (not syncing)
	time.Sleep(time.Second) // ensure later timestamp than A's last sync
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("docs")
	storeB.Put(ctx, "from-b.txt", strings.NewReader("B wrote this while A was offline"))

	// A comes back and syncs
	dA.threeWaySync(ctx)

	data, err := os.ReadFile(filepath.Join(dirA, "from-b.txt"))
	if err != nil {
		t.Fatalf("from-b.txt not downloaded: %v", err)
	}
	if string(data) != "B wrote this while A was offline" {
		t.Errorf("got %q", string(data))
	}
}

// Daemon restart preserves manifest — doesn't re-download deleted files.
func TestIntegrationManifestPersistsAcrossRestart(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "manifest-persist")

	// A uploads files
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("ns")
	storeA.Put(ctx, "keep.txt", strings.NewReader("keep me"))
	storeA.Put(ctx, "delete-me.txt", strings.NewReader("remove later"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B syncs — downloads both
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("ns")
	dir := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	d1, _ := NewDaemon(storeB, nil, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dir, Namespaces: []string{"ns"}},
		DriveID:      "test-persist",
		ManifestPath: manifestPath,
		PollSeconds:  30,
	}, nil)
	d1.threeWaySync(ctx)

	if _, err := os.Stat(filepath.Join(dir, "delete-me.txt")); err != nil {
		t.Fatal("delete-me.txt should exist after first sync")
	}

	// B deletes locally
	os.Remove(filepath.Join(dir, "delete-me.txt"))
	d1.threeWaySync(ctx)

	// Simulate B restart — new daemon, same manifest
	storeB2 := NewWithDevice(backend, idB, "device-b")
	storeB2.SetNamespace("ns")
	d2, _ := NewDaemon(storeB2, nil, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dir, Namespaces: []string{"ns"}},
		DriveID:      "test-persist",
		ManifestPath: manifestPath,
		PollSeconds:  30,
	}, nil)
	d2.threeWaySync(ctx)

	// delete-me.txt should NOT be re-downloaded
	if _, err := os.Stat(filepath.Join(dir, "delete-me.txt")); !os.IsNotExist(err) {
		t.Error("delete-me.txt should stay deleted after restart")
	}
}

// First sync on a fresh device downloads everything.
func TestIntegrationFirstSyncDownloadsAll(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "first-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("drive")
	storeA.Put(ctx, "one.txt", strings.NewReader("1"))
	storeA.Put(ctx, "two.txt", strings.NewReader("22"))
	storeA.Put(ctx, "sub/three.txt", strings.NewReader("333"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B: fresh device, empty local dir, empty manifest
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("drive")
	dirB := t.TempDir()
	dB, _ := NewDaemon(storeB, nil, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"drive"}},
		DriveID:      "test-first",
		ManifestPath: filepath.Join(t.TempDir(), "manifest.json"),
		PollSeconds:  30,
	}, nil)
	result := dB.threeWaySync(ctx)

	if result.Downloaded != 3 {
		t.Errorf("downloaded %d, want 3", result.Downloaded)
	}

	for _, name := range []string{"one.txt", "two.txt", "sub/three.txt"} {
		p := filepath.Join(dirB, filepath.FromSlash(name))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s", name)
		}
	}
}

// Both devices edit same file → conflict file created.
func TestIntegrationConflictCreatesConflictFile(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "conflict-test")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "doc.txt", strings.NewReader("original"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B syncs — gets doc.txt
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	dirB := t.TempDir()
	dB, _ := NewDaemon(storeB, nil, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-conflict-b",
		ManifestPath: filepath.Join(t.TempDir(), "manifest-b.json"),
		PollSeconds:  30,
	}, nil)
	dB.threeWaySync(ctx)

	// Verify B has the file
	if _, err := os.Stat(filepath.Join(dirB, "doc.txt")); err != nil {
		t.Fatal("doc.txt should exist on B")
	}

	// A modifies and uploads (directly via store, not daemon)
	time.Sleep(time.Second) // ensure different timestamp
	storeA.Put(ctx, "doc.txt", strings.NewReader("A's edit"))

	// B modifies locally
	os.WriteFile(filepath.Join(dirB, "doc.txt"), []byte("B's edit"), 0644)

	// B syncs — should detect conflict (B modified locally, A modified remotely)
	result := dB.threeWaySync(ctx)

	if result.Conflicts != 1 {
		t.Errorf("expected 1 conflict, got %d", result.Conflicts)
	}

	// B should have a conflict file
	conflictFiles, _ := filepath.Glob(filepath.Join(dirB, "doc.conflict.*.txt"))
	if len(conflictFiles) == 0 {
		files, _ := os.ReadDir(dirB)
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Name()
		}
		t.Errorf("no conflict file found, files: %v", names)
	}
}
