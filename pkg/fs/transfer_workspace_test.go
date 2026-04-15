package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverTransferWorkspaceRepublishesStagedSession(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()

	if err := ensureTransferWorkspace(baseDir); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}

	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(baseDir), "recover-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString("republished content"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	targetPath := filepath.Join(targetDir, "recovered.txt")
	session, err := newTransferSession(transferSessionsDir(baseDir), "download", tmpPath, targetPath)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}
	if err := session.markStaged(); err != nil {
		t.Fatalf("mark staged: %v", err)
	}

	stats, err := recoverTransferWorkspace(baseDir, nil)
	if err != nil {
		t.Fatalf("recover workspace: %v", err)
	}
	if stats.Republished != 1 {
		t.Fatalf("republished = %d, want 1", stats.Republished)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "republished content" {
		t.Fatalf("content = %q", string(data))
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp path should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(session.path); !os.IsNotExist(err) {
		t.Fatalf("session path should be removed, stat err=%v", err)
	}
}

func TestRecoverTransferWorkspaceCleansWritingSession(t *testing.T) {
	baseDir := t.TempDir()
	targetDir := t.TempDir()

	if err := ensureTransferWorkspace(baseDir); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}

	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(baseDir), "recover-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString("incomplete"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	targetPath := filepath.Join(targetDir, "should-not-exist.txt")
	session, err := newTransferSession(transferSessionsDir(baseDir), "upload", tmpPath, targetPath)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}

	stats, err := recoverTransferWorkspace(baseDir, nil)
	if err != nil {
		t.Fatalf("recover workspace: %v", err)
	}
	if stats.CleanedSessions == 0 {
		t.Fatal("expected writing session to be cleaned")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp path should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(session.path); !os.IsNotExist(err) {
		t.Fatalf("session path should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("target should not exist, stat err=%v", err)
	}
}
