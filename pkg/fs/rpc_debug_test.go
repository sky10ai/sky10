package fs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestRPCDebugScreenshotUploadsImageAndContext(t *testing.T) {
	t.Setenv("SKY10_HOME", t.TempDir())
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

	raw, err, handled := handler.Dispatch(context.Background(), "debug.screenshot", params)
	if err != nil {
		t.Fatalf("debugScreenshot: %v", err)
	}
	if !handled {
		t.Fatal("debugScreenshot handled = false, want true")
	}
	result := raw.(debugScreenshotResult)
	if result.Status != "saved" {
		t.Fatalf("status = %q, want saved", result.Status)
	}
	if result.Key == "" || result.MetadataKey == "" || result.ImageKey == "" {
		t.Fatalf("result missing keys: %#v", result)
	}
	if !result.S3Synced || result.S3Error != "" {
		t.Fatalf("S3 sync = %t, error = %q; want synced without error", result.S3Synced, result.S3Error)
	}
	if result.LocalImagePath == "" || result.LocalMetadataPath == "" {
		t.Fatalf("result missing local paths: %#v", result)
	}
	if result.Size != int64(len(image)) {
		t.Fatalf("size = %d, want %d", result.Size, len(image))
	}
	localImage, err := os.ReadFile(result.LocalImagePath)
	if err != nil {
		t.Fatalf("read local screenshot image: %v", err)
	}
	if !bytes.Equal(localImage, image) {
		t.Fatalf("local image = %v, want %v", localImage, image)
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

func TestRPCDebugScreenshotSavesLocallyWithoutStorage(t *testing.T) {
	t.Setenv("SKY10_HOME", t.TempDir())
	id, _ := GenerateDeviceKey()
	store := New(nil, id)
	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test-version", nil)
	handler := NewFSHandler(store, server, filepath.Join(t.TempDir(), "drives.json"), nil, nil)

	image := []byte{0x89, 'P', 'N', 'G'}
	params, err := json.Marshal(map[string]interface{}{
		"captured_at":  "2026-05-09T12:00:00Z",
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(image),
		"filename":     "screen.png",
		"height":       720,
		"page_context": map[string]interface{}{"route": "/settings"},
		"size_bytes":   len(image),
		"width":        1280,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	raw, err, handled := handler.Dispatch(context.Background(), "debug.screenshot", params)
	if err != nil {
		t.Fatalf("debugScreenshot without storage: %v", err)
	}
	if !handled {
		t.Fatal("debugScreenshot handled = false, want true")
	}
	result := raw.(debugScreenshotResult)
	if result.S3Synced {
		t.Fatal("S3Synced = true without storage, want false")
	}
	if result.LocalImagePath == "" {
		t.Fatalf("missing local image path: %#v", result)
	}

	listRaw, err, handled := handler.Dispatch(context.Background(), "debug.list", nil)
	if err != nil {
		t.Fatalf("debugList without storage: %v", err)
	}
	if !handled {
		t.Fatal("debugList handled = false, want true")
	}
	listed := listRaw.(map[string]interface{})
	keys := listed["keys"].([]string)
	if !containsString(keys, result.ImageKey) || !containsString(keys, result.MetadataKey) {
		t.Fatalf("keys = %#v, want image %q and metadata %q", keys, result.ImageKey, result.MetadataKey)
	}

	getParams, err := json.Marshal(map[string]string{"key": result.ImageKey})
	if err != nil {
		t.Fatalf("marshal get params: %v", err)
	}
	getRaw, err, handled := handler.Dispatch(context.Background(), "debug.get", getParams)
	if err != nil {
		t.Fatalf("debugGet local screenshot: %v", err)
	}
	if !handled {
		t.Fatal("debugGet handled = false, want true")
	}
	got := getRaw.(map[string]interface{})
	if got["source"] != "local" {
		t.Fatalf("source = %v, want local", got["source"])
	}
	if got["data_base64"] != base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("data_base64 = %v, want encoded image", got["data_base64"])
	}
}

func TestRPCDebugDumpSavesLocallyWithoutStorage(t *testing.T) {
	t.Setenv("SKY10_HOME", t.TempDir())
	id, _ := GenerateDeviceKey()
	store := New(nil, id)
	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test-version", nil)
	handler := NewFSHandler(store, server, filepath.Join(t.TempDir(), "drives.json"), nil, nil)

	raw, err, handled := handler.Dispatch(context.Background(), "debug.dump", nil)
	if err != nil {
		t.Fatalf("debugDump without storage: %v", err)
	}
	if !handled {
		t.Fatal("debugDump handled = false, want true")
	}
	result := raw.(map[string]interface{})
	if result["status"] != "saved" {
		t.Fatalf("status = %v, want saved", result["status"])
	}
	if result["s3_synced"] != false {
		t.Fatalf("s3_synced = %v, want false", result["s3_synced"])
	}
	localPath, ok := result["local_path"].(string)
	if !ok || localPath == "" {
		t.Fatalf("local_path = %v, want non-empty string", result["local_path"])
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local debug dump: %v", err)
	}
	var dump map[string]interface{}
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("unmarshal local debug dump: %v", err)
	}
	if dump["remote_storage_configured"] != false {
		t.Fatalf("remote_storage_configured = %v, want false", dump["remote_storage_configured"])
	}
}

func TestRPCDebugGetReturnsBase64ForBinaryObjects(t *testing.T) {
	t.Setenv("SKY10_HOME", t.TempDir())
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

	raw, err, handled := handler.Dispatch(context.Background(), "debug.get", params)
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
	if result["source"] != "s3" {
		t.Fatalf("source = %v, want s3", result["source"])
	}
}

func TestRPCDebugLegacySkyFSAliasesRemainSupported(t *testing.T) {
	t.Setenv("SKY10_HOME", t.TempDir())
	id, _ := GenerateDeviceKey()
	store := New(nil, id)
	server := skyrpc.NewServer(filepath.Join(t.TempDir(), "test.sock"), "test-version", nil)
	handler := NewFSHandler(store, server, filepath.Join(t.TempDir(), "drives.json"), nil, nil)

	for _, method := range []string{
		"skyfs.debugDump",
		"skyfs.debugList",
	} {
		if _, err, handled := handler.Dispatch(context.Background(), method, nil); err != nil {
			t.Fatalf("%s returned error: %v", method, err)
		} else if !handled {
			t.Fatalf("%s handled = false, want true", method)
		}
	}

	image := []byte{0x89, 'P', 'N', 'G'}
	params, err := json.Marshal(map[string]interface{}{
		"content_type": "image/png",
		"data_base64":  base64.StdEncoding.EncodeToString(image),
		"filename":     "legacy.png",
		"height":       1,
		"size_bytes":   len(image),
		"width":        1,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	raw, err, handled := handler.Dispatch(context.Background(), "skyfs.debugScreenshot", params)
	if err != nil {
		t.Fatalf("legacy debugScreenshot returned error: %v", err)
	}
	if !handled {
		t.Fatal("legacy debugScreenshot handled = false, want true")
	}
	result := raw.(debugScreenshotResult)

	getParams, err := json.Marshal(map[string]string{"key": result.ImageKey})
	if err != nil {
		t.Fatalf("marshal get params: %v", err)
	}
	if _, err, handled := handler.Dispatch(context.Background(), "skyfs.debugGet", getParams); err != nil {
		t.Fatalf("legacy debugGet returned error: %v", err)
	} else if !handled {
		t.Fatal("legacy debugGet handled = false, want true")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
