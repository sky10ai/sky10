package fs

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/logging"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func startTestRPC(t *testing.T) (*skyrpc.Server, net.Conn, context.CancelFunc) {
	t.Helper()
	server, _, conn, cancel := startTestRPCWithHandler(t, nil)
	return server, conn, cancel
}

func startTestRPCWithHandler(t *testing.T, loggerRuntime *logging.Runtime) (*skyrpc.Server, *FSHandler, net.Conn, context.CancelFunc) {
	t.Helper()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	server := skyrpc.NewServer(sockPath, "test", nil)
	driveCfgPath := filepath.Join(t.TempDir(), "drives.json")
	handler := NewFSHandler(store, server, driveCfgPath, nil, nil)
	if loggerRuntime != nil {
		handler = NewFSHandler(store, server, driveCfgPath, loggerRuntime.Logger, loggerRuntime.Buffer)
	}
	server.RegisterHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())

	go server.Serve(ctx)
	time.Sleep(50 * time.Millisecond) // wait for listener

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
		cancel()
		if loggerRuntime != nil {
			_ = loggerRuntime.Close()
		}
	})

	return server, handler, conn, cancel
}

// rpcRaw is a response where Result stays as raw JSON for proper unmarshaling.
type rpcRaw struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *skyrpc.Error   `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

func rpcCall(t *testing.T, conn net.Conn, method string, params interface{}) json.RawMessage {
	t.Helper()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, _ = json.Marshal(params)
	}

	req := skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp rpcRaw
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("RPC error: %s", resp.Error.Message)
	}

	return resp.Result
}

func TestRPCListEmpty(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	result := rpcCall(t, conn, "skyfs.list", listParams{Prefix: ""})

	var lr listResult
	json.Unmarshal(result, &lr)

	if len(lr.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(lr.Files))
	}
}

func TestRPCListWorksWithoutStorage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	id, _ := GenerateDeviceKey()
	store := New(nil, id)
	server := skyrpc.NewServer(filepath.Join(tmpDir, "test.sock"), "test", nil)
	handler := NewFSHandler(store, server, filepath.Join(tmpDir, "drives.json"), nil, nil)

	if _, err := handler.driveManager.CreateDrive("Agents", filepath.Join(tmpDir, "Sky10", "Drives", "Agents"), "Agents"); err != nil {
		t.Fatalf("create drive: %v", err)
	}

	params, err := json.Marshal(listParams{Prefix: ""})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	result, err, handled := handler.Dispatch(context.Background(), "skyfs.list", params)
	if err != nil {
		t.Fatalf("skyfs.list without storage returned error: %v", err)
	}
	if !handled {
		t.Fatal("skyfs.list was not handled")
	}
	if _, ok := result.(listResult); !ok {
		t.Fatalf("skyfs.list result type = %T, want listResult", result)
	}
}

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	result := rpcCall(t, conn, "skyfs.status", nil)
	var status statusResult
	json.Unmarshal(result, &status)

	if status.Syncing {
		t.Error("should not be syncing")
	}
}

func TestRPCHealthAliases(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	systemResult := rpcCall(t, conn, "system.health", nil)
	var systemHealth map[string]interface{}
	if err := json.Unmarshal(systemResult, &systemHealth); err != nil {
		t.Fatalf("unmarshal system.health: %v", err)
	}

	legacyResult := rpcCall(t, conn, "skyfs.health", nil)
	var legacyHealth map[string]interface{}
	if err := json.Unmarshal(legacyResult, &legacyHealth); err != nil {
		t.Fatalf("unmarshal skyfs.health: %v", err)
	}

	if systemHealth["status"] != "ok" {
		t.Fatalf("system.health status = %v, want ok", systemHealth["status"])
	}
	if systemHealth["version"] != "test" {
		t.Fatalf("system.health version = %v, want test", systemHealth["version"])
	}
	if legacyHealth["status"] != systemHealth["status"] {
		t.Fatalf("skyfs.health status = %v, want %v", legacyHealth["status"], systemHealth["status"])
	}
	if legacyHealth["version"] != systemHealth["version"] {
		t.Fatalf("skyfs.health version = %v, want %v", legacyHealth["version"], systemHealth["version"])
	}
}

func TestRPCMethodNotFound(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	req := skyrpc.Request{JSONRPC: "2.0", Method: "skyfs.nonexistent", ID: 1}
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	encoder.Encode(req)

	var resp skyrpc.Response
	decoder.Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestRPCLogsUsesBufferedRuntime(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("open-file unlink semantics differ on Windows")
	}

	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logRuntime, err := logging.New(logging.Config{
		FilePath:    logPath,
		BufferLines: 4,
	})
	if err != nil {
		t.Fatalf("logging.New() error = %v", err)
	}

	_, handler, conn, _ := startTestRPCWithHandler(t, logRuntime)
	handler.logger.Info("peer connected", "peer", "alpha")
	handler.logger.Warn("sync stalled", "peer", "beta")
	handler.logger.Error("sync failed", "peer", "gamma")

	if err := os.Remove(logPath); err != nil {
		t.Fatalf("Remove(%q) error = %v", logPath, err)
	}

	result := rpcCall(t, conn, "skyfs.logs", map[string]any{
		"lines":  2,
		"filter": "sync",
	})

	var got struct {
		Lines []string `json:"lines"`
		Total int      `json:"total"`
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("total = %d, want 2", got.Total)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(got.Lines))
	}
	if !strings.Contains(got.Lines[0], "sync stalled") {
		t.Fatalf("first line = %q, want sync stalled", got.Lines[0])
	}
	if !strings.Contains(got.Lines[1], "sync failed") {
		t.Fatalf("second line = %q, want sync failed", got.Lines[1])
	}
}
