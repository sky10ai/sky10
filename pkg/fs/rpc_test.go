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
)

func startTestRPC(t *testing.T) (*RPCServer, net.Conn, context.CancelFunc) {
	t.Helper()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	server := NewRPCServer(store, sockPath, filepath.Join(t.TempDir(), "drives.json"), "test", nil)

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
	})

	return server, conn, cancel
}

// rpcRaw is a response where Result stays as raw JSON for proper unmarshaling.
type rpcRaw struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

func rpcCall(t *testing.T, conn net.Conn, method string, params interface{}) json.RawMessage {
	t.Helper()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, _ = json.Marshal(params)
	}

	req := RPCRequest{
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

func TestRPCPutGetRoundTrip(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	dir := t.TempDir()
	inFile := filepath.Join(dir, "input.md")
	outFile := filepath.Join(dir, "output.md")

	os.WriteFile(inFile, []byte("encrypted content"), 0644)

	rpcCall(t, conn, "skyfs.put", putParams{Path: "file.md", LocalPath: inFile})
	rpcCall(t, conn, "skyfs.get", getParams{Path: "file.md", OutPath: outFile})

	got, _ := os.ReadFile(outFile)
	if string(got) != "encrypted content" {
		t.Errorf("got %q, want %q", got, "encrypted content")
	}
}

func TestRPCStatus(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	result := rpcCall(t, conn, "skyfs.status", nil)
	var status statusResult
	json.Unmarshal(result, &status)

	// Should not be syncing by default
	if status.Syncing {
		t.Error("should not be syncing")
	}
}

func TestRPCMethodNotFound(t *testing.T) {
	t.Parallel()
	_, conn, _ := startTestRPC(t)

	req := RPCRequest{JSONRPC: "2.0", Method: "skyfs.nonexistent", ID: 1}
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	encoder.Encode(req)

	var resp RPCResponse
	decoder.Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}
