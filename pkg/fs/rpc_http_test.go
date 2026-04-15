package fs

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestHandleUploadStagesThenPublishes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	id, _ := GenerateDeviceKey()
	store := New(s3adapter.NewMemory(), id)
	server := skyrpc.NewServer(filepath.Join(tmpDir, "test.sock"), "test", nil)
	handler := NewFSHandler(store, server, filepath.Join(tmpDir, "drives.json"), nil, nil)

	localDir := filepath.Join(tmpDir, "sync")
	drive, err := handler.driveManager.CreateDrive("Uploads", localDir, "uploads")
	if err != nil {
		t.Fatalf("create drive: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("hello via http")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/upload?drive="+url.QueryEscape(drive.ID)+"&path="+url.QueryEscape("docs/hello.txt"),
		&body,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	handler.HandleUpload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	target := filepath.Join(localDir, "docs", "hello.txt")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "hello via http" {
		t.Fatalf("content = %q", string(data))
	}

	stagingDir := transferStagingDir(driveDataDir(drive.ID))
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("read staging dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging dir should be empty after publish, found %d entries", len(entries))
	}

	sessionEntries, err := os.ReadDir(transferSessionsDir(driveDataDir(drive.ID)))
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	if len(sessionEntries) != 0 {
		t.Fatalf("sessions dir should be empty after publish, found %d entries", len(sessionEntries))
	}
}
