package fs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestRPCDebugScreenshotUploadsImageAndContext(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test-version", nil)
	handler := NewFSHandler(store, server, filepath.Join(t.TempDir(), "drives.json"), nil, nil)

	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	params, err := json.Marshal(map[string]interface{}{
		"captured_at":  "2026-05-09T12:00:00.123456789Z",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(image),
		"filename":     "../sky10-context.png",
		"height":       900,
		"page_context": map[string]interface{}{
			"pageLabel": "Agents",
			"route":     "/agents",
			"viewport":  "1440x900",
		},
		"size_bytes": len(image),
		"width":      1440,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	raw, err, handled := handler.Dispatch(context.Background(), "skyfs.debugScreenshot", params)
	if err != nil {
		t.Fatalf("debugScreenshot: %v", err)
	}
	if !handled {
		t.Fatal("debugScreenshot handled = false, want true")
	}
	result := raw.(debugScreenshotResult)
	if result.Status != "uploaded" {
		t.Fatalf("status = %q, want uploaded", result.Status)
	}
	if result.Key == "" || result.MetadataKey == "" || result.ImageKey == "" {
		t.Fatalf("result missing keys: %#v", result)
	}
	if result.Size != int64(len(image)) {
		t.Fatalf("size = %d, want %d", result.Size, len(image))
	}

	rc, err := backend.Get(context.Background(), result.ImageKey)
	if err != nil {
		t.Fatalf("get screenshot image: %v", err)
	}
	gotImage, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read screenshot image: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close screenshot image: %v", err)
	}
	if !bytes.Equal(gotImage, image) {
		t.Fatalf("uploaded image = %v, want %v", gotImage, image)
	}

	rc, err = backend.Get(context.Background(), result.MetadataKey)
	if err != nil {
		t.Fatalf("get screenshot metadata: %v", err)
	}
	metadataData, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read screenshot metadata: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close screenshot metadata: %v", err)
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal(metadataData, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metadata["type"] != "ui_screenshot" {
		t.Fatalf("metadata type = %v, want ui_screenshot", metadata["type"])
	}
	pageContext := metadata["page_context"].(map[string]interface{})
	if pageContext["route"] != "/agents" {
		t.Fatalf("route = %v, want /agents", pageContext["route"])
	}
	screenshot := metadata["screenshot"].(map[string]interface{})
	if screenshot["key"] != result.ImageKey {
		t.Fatalf("screenshot key = %v, want %s", screenshot["key"], result.ImageKey)
	}
}

func TestRPCDebugGetReturnsBase64ForBinaryObjects(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)
	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test-version", nil)
	handler := NewFSHandler(store, server, filepath.Join(t.TempDir(), "drives.json"), nil, nil)

	image := []byte{0x89, 'P', 'N', 'G'}
	key := "debug/device/screen.png"
	if err := backend.Put(context.Background(), key, bytes.NewReader(image), int64(len(image))); err != nil {
		t.Fatalf("put image: %v", err)
	}
	params, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	raw, err, handled := handler.Dispatch(context.Background(), "skyfs.debugGet", params)
	if err != nil {
		t.Fatalf("debugGet: %v", err)
	}
	if !handled {
		t.Fatal("debugGet handled = false, want true")
	}
	result := raw.(map[string]interface{})
	if result["content_type"] != "image/png" {
		t.Fatalf("content_type = %v, want image/png", result["content_type"])
	}
	if result["data_base64"] != base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("data_base64 = %v, want encoded image", result["data_base64"])
	}
}
