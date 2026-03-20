package fs

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// Enabled drives must auto-start when the RPC server starts.
// Before fix: StartAll was defined but never called — drives
// required a manual driveStart RPC after every daemon restart.
func TestDrivesAutoStartOnServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Create drives config with an enabled drive
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	driveCfgPath := filepath.Join(tmpDir, "drives.json")
	localDir := filepath.Join(tmpDir, "sync-folder")
	os.MkdirAll(localDir, 0755)

	drives := []Drive{
		{
			ID:        "drive_test",
			Name:      "TestDrive",
			LocalPath: localDir,
			Namespace: "testns",
			Enabled:   true,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	// Start RPC server
	sockPath := filepath.Join(tmpDir, "test.sock")
	server := NewRPCServer(store, sockPath, driveCfgPath, "test", nil)

	go server.Serve(ctx)

	// Wait for server to be ready
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Query drive list — should be running
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"skyfs.driveList","id":1}` + "\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Result struct {
			Drives []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Running bool   `json:"running"`
			} `json:"drives"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("parse: %v (raw: %s)", err, string(buf[:n]))
	}

	if len(resp.Result.Drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(resp.Result.Drives))
	}
	if !resp.Result.Drives[0].Running {
		t.Error("drive should be auto-started on serve, but running=false")
	}
}

// skyfs.syncActivity should return pending outbox/inbox entries.
func TestSyncActivityRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	driveCfgPath := filepath.Join(tmpDir, "drives.json")
	localDir := filepath.Join(tmpDir, "sync-folder")
	os.MkdirAll(localDir, 0755)

	drives := []Drive{
		{
			ID:        "drive_activity",
			Name:      "ActivityDrive",
			LocalPath: localDir,
			Namespace: "actns",
			Enabled:   true,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := filepath.Join(tmpDir, "test.sock")
	server := NewRPCServer(store, sockPath, driveCfgPath, "test", nil)
	go server.Serve(ctx)

	// Wait for server to be ready
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Call syncActivity — should return empty pending
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"skyfs.syncActivity","id":1}` + "\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Result struct {
			Pending []struct {
				Direction string `json:"direction"`
				Path      string `json:"path"`
			} `json:"pending"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("parse: %v (raw: %s)", err, string(buf[:n]))
	}
	if resp.Error != nil {
		t.Fatalf("RPC error: %s", resp.Error.Message)
	}
	// Pending should be an array (possibly empty)
	if resp.Result.Pending == nil {
		t.Error("expected pending array, got nil")
	}
}

// Drive sync should upload local files to S3 after auto-start.
func TestDriveAutoStartSyncsFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir) // isolate manifest from real HOME
	driveCfgPath := filepath.Join(tmpDir, "drives.json")
	localDir := filepath.Join(tmpDir, "sync-folder")
	os.MkdirAll(localDir, 0755)

	// Create a file BEFORE the daemon starts
	os.WriteFile(filepath.Join(localDir, "pre-existing.txt"), []byte("was here before"), 0644)

	drives := []Drive{
		{
			ID:        "drive_sync",
			Name:      "SyncDrive",
			LocalPath: localDir,
			Namespace: "syncns",
			Enabled:   true,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := filepath.Join(tmpDir, "test.sock")
	server := NewRPCServer(store, sockPath, driveCfgPath, "test", nil)
	go server.Serve(ctx)

	// Wait for initial sync + outbox drain
	time.Sleep(6 * time.Second)

	// Check local ops log — file should be tracked
	opsLogPath := filepath.Join(driveDataDir("drive_sync"), "ops.jsonl")
	localLog := opslog.NewLocalOpsLog(opsLogPath, store.deviceID)
	if _, ok := localLog.Lookup("pre-existing.txt"); !ok {
		t.Errorf("pre-existing.txt not in local log after auto-start")
	}
}
