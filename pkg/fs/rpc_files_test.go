package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestRPCMkdirNormalizesBackslashPath(t *testing.T) {
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

	params, err := json.Marshal(mkdirParams{Drive: drive.ID, Path: `docs\nested`})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	if _, err := handler.rpcMkdir(context.Background(), params); err != nil {
		t.Fatalf("rpcMkdir() error = %v", err)
	}

	target := filepath.Join(localDir, "docs", "nested")
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("expected normalized directory %q: %v", target, err)
	}
}

func TestRPCRemoveRejectsEscapingPath(t *testing.T) {
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

	params, err := json.Marshal(removeParams{Drive: drive.ID, Path: `..\escape.txt`})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	if _, err := handler.rpcRemove(context.Background(), params); err == nil {
		t.Fatal("rpcRemove() unexpectedly accepted escaping path")
	}
}
