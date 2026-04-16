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

func TestHealthRPCIncludesPathIssueCounts(t *testing.T) {
	withWindowsPathPolicy(t, true)

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

	drives := []Drive{{
		ID:        "drive_path_issues",
		Name:      "PathIssues",
		LocalPath: localDir,
		Namespace: "pathissues",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_path_issues")
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), store.deviceID)
	if err := localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "Docs/Readme.md", Namespace: "pathissues"}); err != nil {
		t.Fatalf("append upper: %v", err)
	}
	if err := localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "docs/readme.md", Namespace: "pathissues"}); err != nil {
		t.Fatalf("append lower: %v", err)
	}

	sockPath := shortSockPath("path-health")
	defer os.Remove(sockPath)
	server := newTestServer(store, sockPath, driveCfgPath)
	go server.Serve(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"skyfs.health","id":1}` + "\n"
	conn.Write([]byte(req))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Result struct {
			PathIssueDrives int `json:"path_issue_drives"`
			PathIssueCount  int `json:"path_issue_count"`
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
	if resp.Result.PathIssueDrives != 1 {
		t.Fatalf("path_issue_drives = %d, want 1", resp.Result.PathIssueDrives)
	}
	if resp.Result.PathIssueCount != 1 {
		t.Fatalf("path_issue_count = %d, want 1", resp.Result.PathIssueCount)
	}
}

func TestDriveListIncludesPathIssueState(t *testing.T) {
	withWindowsPathPolicy(t, true)

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

	drives := []Drive{{
		ID:        "drive_list_path_issues",
		Name:      "ListPathIssues",
		LocalPath: localDir,
		Namespace: "listpathissues",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_list_path_issues")
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), store.deviceID)
	if err := localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "CON.txt", Namespace: "listpathissues"}); err != nil {
		t.Fatalf("append invalid path: %v", err)
	}

	sockPath := shortSockPath("path-list")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	installSyncHealthRuntime(t, handler, "drive_list_path_issues", "nsid", nil, nil, fsReplicaSyncState{})
	go server.Serve(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

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
				ID             string `json:"id"`
				SyncState      string `json:"sync_state"`
				PathIssueCount int    `json:"path_issue_count"`
			} `json:"drives"`
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
	if len(resp.Result.Drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(resp.Result.Drives))
	}
	if resp.Result.Drives[0].PathIssueCount != 1 {
		t.Fatalf("path_issue_count = %d, want 1", resp.Result.Drives[0].PathIssueCount)
	}
	if resp.Result.Drives[0].SyncState != "error" {
		t.Fatalf("sync_state = %q, want error", resp.Result.Drives[0].SyncState)
	}
}

func TestSyncActivityRPCIncludesPathIssues(t *testing.T) {
	withWindowsPathPolicy(t, true)

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

	drives := []Drive{{
		ID:        "drive_activity_path_issues",
		Name:      "ActivityPathIssues",
		LocalPath: localDir,
		Namespace: "activitypathissues",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_activity_path_issues")
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), store.deviceID)
	if err := localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "Docs/Readme.md", Namespace: "activitypathissues"}); err != nil {
		t.Fatalf("append upper: %v", err)
	}
	if err := localLog.AppendLocal(opslog.Entry{Type: opslog.Put, Path: "docs/readme.md", Namespace: "activitypathissues"}); err != nil {
		t.Fatalf("append lower: %v", err)
	}

	sockPath := shortSockPath("path-activity")
	defer os.Remove(sockPath)
	server := newTestServer(store, sockPath, driveCfgPath)
	go server.Serve(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

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
			PathIssues []struct {
				DriveID string   `json:"drive_id"`
				Kind    string   `json:"kind"`
				Paths   []string `json:"paths"`
				Reason  string   `json:"reason"`
			} `json:"path_issues"`
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
	if len(resp.Result.PathIssues) != 1 {
		t.Fatalf("path issue count = %d, want 1", len(resp.Result.PathIssues))
	}
	if resp.Result.PathIssues[0].Kind != string(pathPolicyIssueCaseCollision) {
		t.Fatalf("kind = %q, want %q", resp.Result.PathIssues[0].Kind, pathPolicyIssueCaseCollision)
	}
}

func TestDriveListIncludesWindowsSymlinkUnsupportedState(t *testing.T) {
	withWindowsPathPolicy(t, true)
	withSymlinkCapabilityDetector(t, func() symlinkCapability {
		return symlinkCapability{
			Supported: false,
			Reason:    "developer mode is disabled",
		}
	})

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

	drives := []Drive{{
		ID:        "drive_symlink_unsupported",
		Name:      "SymlinkUnsupported",
		LocalPath: localDir,
		Namespace: "symlinkunsupported",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_symlink_unsupported")
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), store.deviceID)
	if err := localLog.AppendLocal(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Namespace:  "symlinkunsupported",
	}); err != nil {
		t.Fatalf("append symlink: %v", err)
	}

	sockPath := shortSockPath("path-symlink")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	installSyncHealthRuntime(t, handler, "drive_symlink_unsupported", "nsid", nil, nil, fsReplicaSyncState{})
	go server.Serve(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

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
				ID             string `json:"id"`
				SyncState      string `json:"sync_state"`
				PathIssueCount int    `json:"path_issue_count"`
				PathIssueMsg   string `json:"path_issue_message"`
			} `json:"drives"`
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
	if len(resp.Result.Drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(resp.Result.Drives))
	}
	if resp.Result.Drives[0].PathIssueCount != 1 {
		t.Fatalf("path_issue_count = %d, want 1", resp.Result.Drives[0].PathIssueCount)
	}
	if resp.Result.Drives[0].SyncState != "error" {
		t.Fatalf("sync_state = %q, want error", resp.Result.Drives[0].SyncState)
	}
	if resp.Result.Drives[0].PathIssueMsg == "" {
		t.Fatal("path_issue_message is empty, want symlink capability reason")
	}
}
