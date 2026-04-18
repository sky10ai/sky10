//go:build integration && skyfs_daemon

package integration

import (
	"context"
	"crypto/sha3"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestIntegrationFSDeleteRootKeepsOneShotCreatedFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, err := skyfs.GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateDeviceKey: %v", err)
	}
	store := skyfs.NewWithDevice(backend, id, "device-a")

	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, tmpDir)

	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", localDir, err)
	}

	driveID := "integration_delete_root_one_shot"
	opsPath := driveOpsLogPath(tmpDir, driveID)
	localLog := opslog.NewLocalOpsLog(opsPath, "device-a")
	if err := localLog.Append(opslog.Entry{
		Type:      opslog.DeleteRoot,
		Path:      "",
		Namespace: "default",
		Device:    "remote-device",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatalf("append delete_root: %v", err)
	}

	daemon, err := skyfs.NewDaemonV2_5(store, skyfs.DaemonConfig{
		SyncConfig:        skyfs.SyncConfig{LocalRoot: localDir},
		DriveID:           driveID,
		PollSeconds:       300,
		ScanSeconds:       300,
		ReconcileSeconds:  2,
		StableWriteWindow: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("NewDaemonV2_5: %v", err)
	}

	stop := runIntegrationDaemon(ctx, cancel, daemon)
	defer stop()
	waitForDaemonStartupMarker(t, driveStateDir(tmpDir, driveID))

	targetPath := filepath.Join(localDir, "TOOLS.md")
	if err := os.WriteFile(targetPath, nil, 0644); err != nil {
		t.Fatalf("write TOOLS.md: %v", err)
	}

	fi := waitForTrackedPath(t, opsPath, "TOOLS.md", 5*time.Second)
	if fi.Checksum != checksumOf("") {
		t.Fatalf("TOOLS.md checksum = %q, want %q", fi.Checksum, checksumOf(""))
	}

	time.Sleep(3 * time.Second)

	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("stat TOOLS.md after reconcile: %v", err)
	}

	snap := snapshotFromOpsLog(t, opsPath)
	if !snap.RootDeleted() {
		t.Fatal("root tombstone should remain present")
	}
	if _, ok := snap.Lookup("TOOLS.md"); !ok {
		t.Fatal("TOOLS.md should remain present in the snapshot after reconcile")
	}
}

func TestIntegrationFSDeleteTombstoneKeepsSameChecksumRecreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, err := skyfs.GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateDeviceKey: %v", err)
	}
	store := skyfs.NewWithDevice(backend, id, "device-a")

	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, tmpDir)

	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", localDir, err)
	}

	driveID := "integration_same_checksum_recreate"
	opsPath := driveOpsLogPath(tmpDir, driveID)
	localLog := opslog.NewLocalOpsLog(opsPath, "device-a")
	keepChecksum := checksumOf("keep")
	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "soul.md",
		Checksum:  keepChecksum,
		Namespace: "default",
		Device:    "device-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatalf("append historical put: %v", err)
	}
	if err := localLog.Append(opslog.Entry{
		Type:         opslog.Delete,
		Path:         "soul.md",
		PrevChecksum: keepChecksum,
		Namespace:    "default",
		Device:       "device-a",
		Timestamp:    200,
		Seq:          2,
	}); err != nil {
		t.Fatalf("append tombstone: %v", err)
	}

	daemon, err := skyfs.NewDaemonV2_5(store, skyfs.DaemonConfig{
		SyncConfig:        skyfs.SyncConfig{LocalRoot: localDir},
		DriveID:           driveID,
		PollSeconds:       300,
		ScanSeconds:       300,
		ReconcileSeconds:  2,
		StableWriteWindow: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("NewDaemonV2_5: %v", err)
	}

	stop := runIntegrationDaemon(ctx, cancel, daemon)
	defer stop()
	waitForDaemonStartupMarker(t, driveStateDir(tmpDir, driveID))

	targetPath := filepath.Join(localDir, "soul.md")
	if err := os.WriteFile(targetPath, []byte("keep"), 0644); err != nil {
		t.Fatalf("write soul.md: %v", err)
	}

	fi := waitForTrackedPath(t, opsPath, "soul.md", 5*time.Second)
	if fi.Checksum != keepChecksum {
		t.Fatalf("soul.md checksum = %q, want %q", fi.Checksum, keepChecksum)
	}

	time.Sleep(3 * time.Second)

	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("stat soul.md after reconcile: %v", err)
	}

	snap := snapshotFromOpsLog(t, opsPath)
	if snap.DeletedFiles()["soul.md"] {
		t.Fatal("soul.md tombstone should be cleared by the later recreate")
	}
	if _, ok := snap.Lookup("soul.md"); !ok {
		t.Fatal("soul.md should remain present in the snapshot after recreate")
	}
}

func runIntegrationDaemon(ctx context.Context, cancel context.CancelFunc, daemon *skyfs.DaemonV2_5) func() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = daemon.Run(ctx)
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func waitForDaemonStartupMarker(t *testing.T, driveDir string) {
	t.Helper()

	marker := filepath.Join(driveDir, "local-disk-state.json")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon startup marker %s not created in time", marker)
}

func waitForTrackedPath(t *testing.T, opsPath, path string, timeout time.Duration) opslog.FileInfo {
	t.Helper()

	log := opslog.NewLocalOpsLog(opsPath, "reader")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		log.InvalidateCache()
		if fi, ok := log.Lookup(path); ok {
			return fi
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s not tracked after %v", path, timeout)
	return opslog.FileInfo{}
}

func snapshotFromOpsLog(t *testing.T, opsPath string) *opslog.Snapshot {
	t.Helper()

	log := opslog.NewLocalOpsLog(opsPath, "reader")
	log.InvalidateCache()
	snap, err := log.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", opsPath, err)
	}
	return snap
}

func driveStateDir(home, driveID string) string {
	return filepath.Join(home, "fs", "drives", driveID)
}

func driveOpsLogPath(home, driveID string) string {
	return filepath.Join(driveStateDir(home, driveID), "ops.jsonl")
}

func checksumOf(content string) string {
	h := sha3.New256()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}
