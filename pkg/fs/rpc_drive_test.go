package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sky10/sky10/pkg/adapter"
	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func shortSockPath(prefix string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("sky10-%s-%d.sock", prefix, time.Now().UnixNano()))
}

// newTestServerWithHandler creates an rpc.Server with FSHandler registered for tests.
func newTestServerWithHandler(store *Store, sockPath, driveCfgPath string) (*skyrpc.Server, *FSHandler) {
	server := skyrpc.NewServer(sockPath, "test", nil)
	handler := NewFSHandler(store, server, driveCfgPath, nil, nil)
	server.RegisterHandler(handler)
	server.OnServe(func() {
		handler.StartDrives()
	})
	return server, handler
}

// newTestServer creates an rpc.Server with FSHandler registered for tests.
func newTestServer(store *Store, sockPath, driveCfgPath string) *skyrpc.Server {
	server, _ := newTestServerWithHandler(store, sockPath, driveCfgPath)
	return server
}

func installReadSourceRuntime(handler *FSHandler, driveID string, sources ...chunkSourceKind) {
	installReadSourceRuntimeWithStore(handler, driveID, nil, sources...)
}

func installReadSourceRuntimeWithStore(handler *FSHandler, driveID string, configureStore func(*Store), sources ...chunkSourceKind) {
	stats := newReadSourceStats()
	for _, source := range sources {
		stats.Record(source)
	}
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	if configureStore != nil {
		configureStore(store)
	}

	handler.driveManager.wLock("test:installReadSourceRuntime")
	handler.driveManager.daemons[driveID] = &driveRuntime{
		daemon: &DaemonV2_5{
			readSources: stats,
			store:       store,
			driveDir:    driveDataDir(driveID),
		},
	}
	handler.driveManager.wUnlock()
}

type stubFSP2PNode struct {
	peers []peer.ID
}

func (n *stubFSP2PNode) Host() host.Host { return nil }
func (n *stubFSP2PNode) PeerID() peer.ID { return "" }
func (n *stubFSP2PNode) ConnectedPrivateNetworkPeers() []peer.ID {
	return append([]peer.ID(nil), n.peers...)
}

func installSyncHealthRuntime(t *testing.T, handler *FSHandler, driveID, nsID string, backend adapter.Backend, peerIDs []string, state fsReplicaSyncState) {
	t.Helper()

	driveDir := driveDataDir(driveID)
	if err := saveFSPeerSyncState(driveDir, state); err != nil {
		t.Fatalf("save fs peer sync state: %v", err)
	}

	id, _ := GenerateDeviceKey()
	var store *Store
	if backend != nil {
		store = New(backend, id)
	} else {
		store = New(nil, id)
	}
	store.SetNamespaceID(nsID)

	peers := make([]peer.ID, 0, len(peerIDs))
	for _, pid := range peerIDs {
		peers = append(peers, peer.ID(pid))
	}

	handler.driveManager.wLock("test:installSyncHealthRuntime")
	handler.driveManager.daemons[driveID] = &driveRuntime{
		daemon: &DaemonV2_5{
			store:    store,
			driveDir: driveDir,
		},
	}
	handler.driveManager.p2pSync = &P2PSync{node: &stubFSP2PNode{peers: peers}}
	handler.driveManager.wUnlock()
}

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
	server := newTestServer(store, sockPath, driveCfgPath)

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

	sockPath := shortSockPath("activity")
	defer os.Remove(sockPath)
	server := newTestServer(store, sockPath, driveCfgPath)
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

func TestSyncActivityRPCIncludesTransferSessions(t *testing.T) {
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
			ID:        "drive_activity_transfer",
			Name:      "TransferDrive",
			LocalPath: localDir,
			Namespace: "actns",
			Enabled:   false,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_activity_transfer")
	if err := ensureTransferWorkspace(driveDir); err != nil {
		t.Fatalf("ensure transfer workspace: %v", err)
	}
	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(driveDir), "rpc-activity-*")
	if err != nil {
		t.Fatalf("create staging file: %v", err)
	}
	if _, err := tmpFile.WriteString("incoming"); err != nil {
		t.Fatalf("write staging file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close staging file: %v", err)
	}
	session, err := newTransferSession(
		transferSessionsDir(driveDir),
		"download",
		tmpPath,
		filepath.Join(localDir, "incoming.txt"),
	)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}
	if err := session.updateProgress(7, 10, "peer"); err != nil {
		t.Fatalf("update progress: %v", err)
	}
	if err := session.markStaged(); err != nil {
		t.Fatalf("mark staged: %v", err)
	}

	sockPath := shortSockPath("health")
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
			Pending []struct {
				Direction    string `json:"direction"`
				Op           string `json:"op"`
				Phase        string `json:"phase"`
				Path         string `json:"path"`
				DriveName    string `json:"drive_name"`
				BytesDone    int64  `json:"bytes_done"`
				BytesTotal   int64  `json:"bytes_total"`
				ActiveSource string `json:"active_source"`
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

	found := false
	for _, entry := range resp.Result.Pending {
		if entry.Op == "download" && entry.Phase == transferPhaseStaged && entry.Path == "incoming.txt" {
			found = true
			if entry.Direction != "down" {
				t.Fatalf("direction = %q, want down", entry.Direction)
			}
			if entry.DriveName != "TransferDrive" {
				t.Fatalf("drive_name = %q", entry.DriveName)
			}
			if entry.BytesDone != 10 {
				t.Fatalf("bytes_done = %d, want 10", entry.BytesDone)
			}
			if entry.BytesTotal != 10 {
				t.Fatalf("bytes_total = %d, want 10", entry.BytesTotal)
			}
			if entry.ActiveSource != "peer" {
				t.Fatalf("active_source = %q, want peer", entry.ActiveSource)
			}
		}
	}
	if !found {
		t.Fatalf("expected download transfer session in pending activity, got %+v", resp.Result.Pending)
	}
}

func TestSyncActivityRPCIncludesReadSources(t *testing.T) {
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
		ID:        "drive_activity_reads",
		Name:      "ReadDrive",
		LocalPath: localDir,
		Namespace: "readns",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := shortSockPath("activity-reads")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	now := time.Unix(1700, 0)
	installReadSourceRuntimeWithStore(handler, "drive_activity_reads", func(store *Store) {
		store.planner.now = func() time.Time { return now }
		store.planner.retryBase = time.Minute
		store.planner.retryMax = time.Minute
		store.planner.recordSuccess(chunkSourcePeer)
		store.planner.recordFailure(chunkSourceS3Blob, errors.New("s3 timed out"))
	}, chunkSourceLocal, chunkSourcePeer, chunkSourceS3Blob)
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
			Reads []struct {
				DriveID        string `json:"drive_id"`
				ReadLocalHits  int    `json:"read_local_hits"`
				ReadPeerHits   int    `json:"read_peer_hits"`
				ReadS3Hits     int    `json:"read_s3_hits"`
				LastReadSource string `json:"last_read_source"`
				PeerHealth     struct {
					Degraded bool `json:"degraded"`
				} `json:"peer_source_health"`
				S3Health struct {
					Degraded  bool   `json:"degraded"`
					LastError string `json:"last_error"`
				} `json:"s3_source_health"`
			} `json:"reads"`
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
	if len(resp.Result.Reads) != 1 {
		t.Fatalf("expected 1 read source entry, got %d", len(resp.Result.Reads))
	}
	read := resp.Result.Reads[0]
	if read.DriveID != "drive_activity_reads" {
		t.Fatalf("drive_id = %q", read.DriveID)
	}
	if read.ReadLocalHits != 1 || read.ReadPeerHits != 1 || read.ReadS3Hits != 1 {
		t.Fatalf("unexpected read counts: %+v", read)
	}
	if read.LastReadSource != "s3" {
		t.Fatalf("last_read_source = %q, want s3", read.LastReadSource)
	}
	if read.PeerHealth.Degraded {
		t.Fatal("peer_source_health.degraded = true, want false")
	}
	if !read.S3Health.Degraded {
		t.Fatal("s3_source_health.degraded = false, want true")
	}
	if read.S3Health.LastError != "s3 timed out" {
		t.Fatalf("s3_source_health.last_error = %q, want %q", read.S3Health.LastError, "s3 timed out")
	}
}

func TestHealthRPCIncludesTransferCounts(t *testing.T) {
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
			ID:        "drive_health_transfer",
			Name:      "HealthDrive",
			LocalPath: localDir,
			Namespace: "healthns",
			Enabled:   false,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_health_transfer")
	if err := ensureTransferWorkspace(driveDir); err != nil {
		t.Fatalf("ensure transfer workspace: %v", err)
	}
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	if err := outbox.Append(NewOutboxPut("queued.txt", "abc", "healthns", filepath.Join(localDir, "queued.txt"))); err != nil {
		t.Fatalf("append outbox: %v", err)
	}
	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(driveDir), "rpc-health-*")
	if err != nil {
		t.Fatalf("create staging file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close staging file: %v", err)
	}
	session, err := newTransferSession(
		transferSessionsDir(driveDir),
		"upload",
		tmpPath,
		filepath.Join(localDir, "profile.txt"),
	)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}
	if err := session.markStaged(); err != nil {
		t.Fatalf("mark staged: %v", err)
	}

	sockPath := shortSockPath("drivelist")
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
			OutboxPending   int `json:"outbox_pending"`
			TransferPending int `json:"transfer_pending"`
			TransferStaged  int `json:"transfer_staged"`
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
	if resp.Result.OutboxPending != 1 {
		t.Fatalf("outbox_pending = %d, want 1", resp.Result.OutboxPending)
	}
	if resp.Result.TransferPending != 1 {
		t.Fatalf("transfer_pending = %d, want 1", resp.Result.TransferPending)
	}
	if resp.Result.TransferStaged != 1 {
		t.Fatalf("transfer_staged = %d, want 1", resp.Result.TransferStaged)
	}
}

func TestHealthRPCIncludesReadSourceCounts(t *testing.T) {
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
		ID:        "drive_health_reads",
		Name:      "HealthReadDrive",
		LocalPath: localDir,
		Namespace: "healthreads",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := shortSockPath("health-reads")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	now := time.Unix(1800, 0)
	installReadSourceRuntimeWithStore(handler, "drive_health_reads", func(store *Store) {
		store.planner.now = func() time.Time { return now }
		store.planner.retryBase = time.Minute
		store.planner.retryMax = time.Minute
		store.planner.recordFailure(chunkSourcePeer, errors.New("peer unavailable"))
		store.planner.recordFailure(chunkSourceS3Blob, errors.New("s3 timed out"))
	}, chunkSourceLocal, chunkSourcePeer, chunkSourceS3Blob, chunkSourceS3Pack)
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
			ReadLocalHits      int `json:"read_local_hits"`
			ReadPeerHits       int `json:"read_peer_hits"`
			ReadS3Hits         int `json:"read_s3_hits"`
			PeerDegradedDrives int `json:"peer_degraded_drives"`
			S3DegradedDrives   int `json:"s3_degraded_drives"`
			PeerSourceFailures int `json:"peer_source_failures"`
			S3SourceFailures   int `json:"s3_source_failures"`
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
	if resp.Result.ReadLocalHits != 1 {
		t.Fatalf("read_local_hits = %d, want 1", resp.Result.ReadLocalHits)
	}
	if resp.Result.ReadPeerHits != 1 {
		t.Fatalf("read_peer_hits = %d, want 1", resp.Result.ReadPeerHits)
	}
	if resp.Result.ReadS3Hits != 2 {
		t.Fatalf("read_s3_hits = %d, want 2", resp.Result.ReadS3Hits)
	}
	if resp.Result.PeerDegradedDrives != 1 {
		t.Fatalf("peer_degraded_drives = %d, want 1", resp.Result.PeerDegradedDrives)
	}
	if resp.Result.S3DegradedDrives != 1 {
		t.Fatalf("s3_degraded_drives = %d, want 1", resp.Result.S3DegradedDrives)
	}
	if resp.Result.PeerSourceFailures != 1 {
		t.Fatalf("peer_source_failures = %d, want 1", resp.Result.PeerSourceFailures)
	}
	if resp.Result.S3SourceFailures != 1 {
		t.Fatalf("s3_source_failures = %d, want 1", resp.Result.S3SourceFailures)
	}
}

func TestHealthRPCIncludesFSSyncHealthCounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	driveCfgPath := filepath.Join(tmpDir, "drives.json")

	now := time.Unix(2200, 0).UTC()
	drives := []Drive{
		{ID: "drive_sync_ok", Name: "SyncOK", LocalPath: filepath.Join(tmpDir, "ok"), Namespace: "syncok", Enabled: false},
		{ID: "drive_sync_wait", Name: "SyncWait", LocalPath: filepath.Join(tmpDir, "wait"), Namespace: "syncwait", Enabled: false},
		{ID: "drive_sync_err", Name: "SyncErr", LocalPath: filepath.Join(tmpDir, "err"), Namespace: "syncerr", Enabled: false},
	}
	for _, drive := range drives {
		if err := os.MkdirAll(drive.LocalPath, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", drive.LocalPath, err)
		}
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := shortSockPath("health-sync")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	installSyncHealthRuntime(t, handler, "drive_sync_ok", "syncok", nil, []string{"peer-a"}, fsReplicaSyncState{
		NSID: "syncok",
		Peers: map[string]fsPeerSyncState{
			"peer-a": {LastSuccessAt: now.Add(-2 * time.Minute)},
		},
	})
	installSyncHealthRuntime(t, handler, "drive_sync_wait", "syncwait", nil, []string{"peer-a"}, fsReplicaSyncState{
		NSID:  "syncwait",
		Peers: map[string]fsPeerSyncState{},
	})
	installSyncHealthRuntime(t, handler, "drive_sync_err", "syncerr", nil, []string{"peer-a"}, fsReplicaSyncState{
		NSID: "syncerr",
		Peers: map[string]fsPeerSyncState{
			"peer-a": {
				LastSuccessAt: now.Add(-3 * time.Minute),
				LastErrorAt:   now.Add(-1 * time.Minute),
				LastError:     "anti-entropy failed",
			},
		},
	})
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
			FSPeerCount     int `json:"fs_peer_count"`
			SyncReadyDrives int `json:"sync_ready_drives"`
			SyncWaiting     int `json:"sync_waiting_drives"`
			SyncErrors      int `json:"sync_error_drives"`
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
	if resp.Result.FSPeerCount != 1 {
		t.Fatalf("fs_peer_count = %d, want 1", resp.Result.FSPeerCount)
	}
	if resp.Result.SyncReadyDrives != 3 {
		t.Fatalf("sync_ready_drives = %d, want 3", resp.Result.SyncReadyDrives)
	}
	if resp.Result.SyncWaiting != 1 {
		t.Fatalf("sync_waiting_drives = %d, want 1", resp.Result.SyncWaiting)
	}
	if resp.Result.SyncErrors != 1 {
		t.Fatalf("sync_error_drives = %d, want 1", resp.Result.SyncErrors)
	}
}

func TestDriveListIncludesTransferCounts(t *testing.T) {
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
			ID:        "drive_list_transfer",
			Name:      "ListDrive",
			LocalPath: localDir,
			Namespace: "listns",
			Enabled:   false,
		},
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	driveDir := driveDataDir("drive_list_transfer")
	if err := ensureTransferWorkspace(driveDir); err != nil {
		t.Fatalf("ensure transfer workspace: %v", err)
	}
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	if err := outbox.Append(NewOutboxPut("queued.txt", "abc", "listns", filepath.Join(localDir, "queued.txt"))); err != nil {
		t.Fatalf("append outbox: %v", err)
	}
	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(driveDir), "rpc-drive-list-*")
	if err != nil {
		t.Fatalf("create staging file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close staging file: %v", err)
	}
	session, err := newTransferSession(
		transferSessionsDir(driveDir),
		"upload",
		tmpPath,
		filepath.Join(localDir, "profile.txt"),
	)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}
	if err := session.markStaged(); err != nil {
		t.Fatalf("mark staged: %v", err)
	}

	sockPath := shortSockPath("drivelist")
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
				ID              string `json:"id"`
				OutboxPending   int    `json:"outbox_pending"`
				TransferPending int    `json:"transfer_pending"`
				TransferStaged  int    `json:"transfer_staged"`
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
	drive := resp.Result.Drives[0]
	if drive.ID != "drive_list_transfer" {
		t.Fatalf("drive id = %q", drive.ID)
	}
	if drive.OutboxPending != 1 {
		t.Fatalf("outbox_pending = %d, want 1", drive.OutboxPending)
	}
	if drive.TransferPending != 1 {
		t.Fatalf("transfer_pending = %d, want 1", drive.TransferPending)
	}
	if drive.TransferStaged != 1 {
		t.Fatalf("transfer_staged = %d, want 1", drive.TransferStaged)
	}
}

func TestDriveListIncludesReadSourceCounts(t *testing.T) {
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
		ID:        "drive_list_reads",
		Name:      "ListReadDrive",
		LocalPath: localDir,
		Namespace: "listreads",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := shortSockPath("drivelist-reads")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	now := time.Unix(1900, 0)
	installReadSourceRuntimeWithStore(handler, "drive_list_reads", func(store *Store) {
		store.planner.now = func() time.Time { return now }
		store.planner.retryBase = time.Minute
		store.planner.retryMax = time.Minute
		store.planner.recordSuccess(chunkSourcePeer)
		store.planner.recordFailure(chunkSourceS3Blob, errors.New("s3 timed out"))
	}, chunkSourcePeer, chunkSourceLocal)
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
				ReadLocalHits  int    `json:"read_local_hits"`
				ReadPeerHits   int    `json:"read_peer_hits"`
				ReadS3Hits     int    `json:"read_s3_hits"`
				LastReadSource string `json:"last_read_source"`
				PeerHealth     struct {
					Degraded bool `json:"degraded"`
				} `json:"peer_source_health"`
				S3Health struct {
					Degraded  bool   `json:"degraded"`
					LastError string `json:"last_error"`
				} `json:"s3_source_health"`
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
	drive := resp.Result.Drives[0]
	if drive.ID != "drive_list_reads" {
		t.Fatalf("drive id = %q", drive.ID)
	}
	if drive.ReadLocalHits != 1 || drive.ReadPeerHits != 1 || drive.ReadS3Hits != 0 {
		t.Fatalf("unexpected read counts: %+v", drive)
	}
	if drive.LastReadSource != "local" {
		t.Fatalf("last_read_source = %q, want local", drive.LastReadSource)
	}
	if drive.PeerHealth.Degraded {
		t.Fatal("peer_source_health.degraded = true, want false")
	}
	if !drive.S3Health.Degraded {
		t.Fatal("s3_source_health.degraded = false, want true")
	}
	if drive.S3Health.LastError != "s3 timed out" {
		t.Fatalf("s3_source_health.last_error = %q, want %q", drive.S3Health.LastError, "s3 timed out")
	}
}

func TestDriveListIncludesFSSyncHealth(t *testing.T) {
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
		ID:        "drive_list_sync",
		Name:      "ListSyncDrive",
		LocalPath: localDir,
		Namespace: "listsync",
		Enabled:   false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	now := time.Unix(2300, 0).UTC()
	sockPath := shortSockPath("drivelist-sync")
	defer os.Remove(sockPath)
	server, handler := newTestServerWithHandler(store, sockPath, driveCfgPath)
	installSyncHealthRuntime(t, handler, "drive_list_sync", "listsync", nil, []string{"peer-a", "peer-b"}, fsReplicaSyncState{
		NSID: "listsync",
		Peers: map[string]fsPeerSyncState{
			"peer-b": {
				LastSuccessAt: now.Add(-1 * time.Minute),
			},
		},
	})
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
				ID              string `json:"id"`
				SyncReady       bool   `json:"sync_ready"`
				PeerCount       int    `json:"peer_count"`
				SyncState       string `json:"sync_state"`
				LastSyncOK      int64  `json:"last_sync_ok"`
				LastSyncPeer    string `json:"last_sync_peer"`
				LastSyncError   string `json:"last_sync_error"`
				LastSyncErrorAt int64  `json:"last_sync_error_at"`
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
	drive := resp.Result.Drives[0]
	if drive.ID != "drive_list_sync" {
		t.Fatalf("drive id = %q", drive.ID)
	}
	if !drive.SyncReady {
		t.Fatal("sync_ready = false, want true")
	}
	if drive.PeerCount != 2 {
		t.Fatalf("peer_count = %d, want 2", drive.PeerCount)
	}
	if drive.SyncState != "ok" {
		t.Fatalf("sync_state = %q, want ok", drive.SyncState)
	}
	if drive.LastSyncOK == 0 || drive.LastSyncPeer != "peer-b" {
		t.Fatalf("unexpected last sync info: %+v", drive)
	}
	if drive.LastSyncError != "" || drive.LastSyncErrorAt != 0 {
		t.Fatalf("unexpected sync error info: %+v", drive)
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
	server := newTestServer(store, sockPath, driveCfgPath)
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

func TestDriveStateRPC(t *testing.T) {
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

	// Pre-existing file on disk
	os.WriteFile(filepath.Join(localDir, "hello.txt"), []byte("hello"), 0644)

	// Pre-populate ops log with a known entry
	dir := driveDataDir("drive_state")
	os.MkdirAll(dir, 0700)
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), store.deviceID)
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "hello.txt", Checksum: "abc",
		Chunks: []string{"c1"}, Size: 5, Namespace: "statens",
	})
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Symlink, Path: "link.txt", Checksum: "def",
		LinkTarget: "hello.txt", Namespace: "statens",
	})

	// Create symlink on disk too
	os.Symlink("hello.txt", filepath.Join(localDir, "link.txt"))

	drives := []Drive{{
		ID: "drive_state", Name: "StateDrive",
		LocalPath: localDir, Namespace: "statens", Enabled: false,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)

	sockPath := filepath.Join(tmpDir, "test.sock")
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

	req := `{"jsonrpc":"2.0","method":"skyfs.driveState","params":{"id":"drive_state"},"id":1}` + "\n"
	conn.Write([]byte(req))

	buf := make([]byte, 16384)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Result map[string]interface{} `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("parse: %v (raw: %s)", err, string(buf[:n]))
	}
	if resp.Error != nil {
		t.Fatalf("RPC error: %s", resp.Error.Message)
	}

	// Should have crdt_files with hello.txt
	crdtFiles, ok := resp.Result["crdt_files"].(map[string]interface{})
	if !ok {
		t.Fatalf("crdt_files missing or wrong type: %v", resp.Result["crdt_files"])
	}
	if _, ok := crdtFiles["hello.txt"]; !ok {
		t.Error("hello.txt not in crdt_files")
	}

	// Symlink should have link_target
	linkEntry, ok := crdtFiles["link.txt"].(map[string]interface{})
	if !ok {
		t.Fatal("link.txt not in crdt_files")
	}
	if linkEntry["link_target"] != "hello.txt" {
		t.Errorf("link_target = %v, want hello.txt", linkEntry["link_target"])
	}

	// Should have disk_files with hello.txt
	diskFiles, ok := resp.Result["disk_files"].(map[string]interface{})
	if !ok {
		t.Fatalf("disk_files missing or wrong type: %v", resp.Result["disk_files"])
	}
	if _, ok := diskFiles["hello.txt"]; !ok {
		t.Error("hello.txt not in disk_files")
	}

	// Should have outbox (possibly empty array)
	if _, ok := resp.Result["outbox"]; !ok {
		t.Error("outbox missing from response")
	}

	// Should have baselines (possibly empty)
	if _, ok := resp.Result["baselines"]; !ok {
		t.Error("baselines missing from response")
	}
}
