//go:build integration

package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration test for the HTTP RPC server: starts a real MinIO backend,
// an RPC server with HTTP, uploads files via the store, then exercises
// every HTTP endpoint.
func TestIntegrationHTTPRPC(t *testing.T) {
	h := StartMinIO(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, _ := GenerateDeviceKey()
	backend := h.Backend(t, NewTestBucket(t))
	store := New(backend, id)

	// Create RPC server with a temp socket and drives config
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "sky10.sock")
	driveCfgPath := filepath.Join(tmpDir, "drives.json")
	os.WriteFile(driveCfgPath, []byte("[]"), 0644)

	server := NewRPCServer(store, sockPath, driveCfgPath, "test/1.0.0", nil)

	// Start Unix socket server in background
	go server.Serve(ctx)

	// Start HTTP server on a free port
	httpPort := freePort(t)
	go server.ServeHTTP(ctx, httpPort)

	// Wait for HTTP to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	waitForHTTP(t, baseURL, 3*time.Second)

	// Upload some files through the store
	store.SetNamespace("test")
	store.Put(ctx, "hello.txt", strings.NewReader("hello world"))
	store.Put(ctx, "docs/readme.md", strings.NewReader("# README"))
	store.Put(ctx, "docs/notes.txt", strings.NewReader("some notes"))

	// --- GET / ---
	t.Run("root", func(t *testing.T) {
		resp := httpGet(t, baseURL+"/")
		var body map[string]string
		json.Unmarshal(resp, &body)

		if body["name"] != "sky10" {
			t.Errorf("name = %q, want sky10", body["name"])
		}
		if body["version"] != "test/1.0.0" {
			t.Errorf("version = %q, want test/1.0.0", body["version"])
		}
		if body["rpc"] != "POST /rpc" {
			t.Errorf("rpc = %q, want POST /rpc", body["rpc"])
		}
	})

	// --- GET /health ---
	t.Run("health", func(t *testing.T) {
		resp := httpGet(t, baseURL+"/health")
		var body map[string]string
		json.Unmarshal(resp, &body)

		if body["status"] != "ok" {
			t.Errorf("status = %q, want ok", body["status"])
		}
	})

	// --- POST /rpc: skyfs.health ---
	t.Run("rpc_health", func(t *testing.T) {
		result := httpRPCCall(t, baseURL, "skyfs.health", nil)
		status, _ := result["status"].(string)
		if status != "ok" {
			t.Errorf("status = %q, want ok", status)
		}
		httpAddr, _ := result["http_addr"].(string)
		if httpAddr == "" {
			t.Error("http_addr should be set")
		}
	})

	// --- POST /rpc: skyfs.s3List (verify uploads via S3 browser) ---
	t.Run("rpc_s3list_ops", func(t *testing.T) {
		result := httpRPCCall(t, baseURL, "skyfs.s3List", map[string]string{"prefix": "ops/"})
		total, _ := result["total"].(float64)
		if total < 3 {
			t.Errorf("S3 ops count = %v, want >= 3", total)
		}
	})

	t.Run("rpc_s3list_blobs", func(t *testing.T) {
		result := httpRPCCall(t, baseURL, "skyfs.s3List", map[string]string{"prefix": "blobs/"})
		total, _ := result["total"].(float64)
		if total < 3 {
			t.Errorf("S3 blobs count = %v, want >= 3", total)
		}
	})

	// --- POST /rpc: skyfs.deviceList ---
	t.Run("rpc_deviceList", func(t *testing.T) {
		result := httpRPCCall(t, baseURL, "skyfs.deviceList", nil)
		if result["this_device"] == nil {
			t.Error("this_device should be set")
		}
	})

	// --- POST /rpc: skyfs.driveList ---
	t.Run("rpc_driveList", func(t *testing.T) {
		result := httpRPCCall(t, baseURL, "skyfs.driveList", nil)
		drives, _ := result["drives"].([]interface{})
		// No drives configured in test — just verify it returns
		if drives == nil {
			t.Error("drives should be an array (even if empty)")
		}
	})

	// --- POST /rpc: unknown method ---
	t.Run("rpc_unknown_method", func(t *testing.T) {
		resp := httpRPCCallRaw(t, baseURL, "skyfs.doesNotExist", nil)
		if resp.Error == nil {
			t.Fatal("expected error for unknown method")
		}
		if resp.Error.Code != -32601 {
			t.Errorf("error code = %d, want -32601", resp.Error.Code)
		}
	})

	// --- POST /rpc: invalid JSON ---
	t.Run("rpc_bad_json", func(t *testing.T) {
		r, err := http.Post(baseURL+"/rpc", "application/json", strings.NewReader("not json"))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", r.StatusCode)
		}
	})

	// --- GET /rpc/events: SSE stream ---
	t.Run("rpc_events", func(t *testing.T) {
		// Connect to SSE
		req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/rpc/events", nil)
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()

		if ct := r.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}

		// Emit an event and read it from the stream
		server.Emit("test.ping", map[string]string{"msg": "hello"})

		// Read with timeout
		buf := make([]byte, 4096)
		done := make(chan string, 1)
		go func() {
			n, _ := r.Body.Read(buf)
			done <- string(buf[:n])
		}()

		select {
		case data := <-done:
			if !strings.Contains(data, "test.ping") {
				t.Errorf("SSE data should contain test.ping, got: %s", data)
			}
			if !strings.Contains(data, "hello") {
				t.Errorf("SSE data should contain hello, got: %s", data)
			}
		case <-time.After(2 * time.Second):
			t.Error("SSE event not received within 2s")
		}
	})
}

// --- helpers ---

func waitForHTTP(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("HTTP server did not start in time")
}

func httpGet(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d: %s", url, resp.StatusCode, body)
	}
	return body
}

func httpRPCCall(t *testing.T, baseURL, method string, params interface{}) map[string]interface{} {
	t.Helper()
	resp := httpRPCCallRaw(t, baseURL, method, params)
	if resp.Error != nil {
		t.Fatalf("RPC %s error: %s", method, resp.Error.Message)
	}
	b, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	json.Unmarshal(b, &result)
	return result
}

func httpRPCCallRaw(t *testing.T, baseURL, method string, params interface{}) RPCResponse {
	t.Helper()
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      1,
	}
	if params != nil {
		reqBody["params"] = params
	}
	data, _ := json.Marshal(reqBody)

	resp, err := http.Post(baseURL+"/rpc", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST /rpc: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp RPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	return rpcResp
}
