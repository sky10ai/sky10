package fs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func withWindowsPathPolicy(t *testing.T, enabled bool) {
	t.Helper()
	prev := windowsPathPolicyEnabled
	windowsPathPolicyEnabled = enabled
	t.Cleanup(func() {
		windowsPathPolicyEnabled = prev
	})
}

func TestDetectWindowsPathIssuesInvalidAndCollision(t *testing.T) {
	t.Parallel()

	issues := detectWindowsPathIssues([]string{
		"Docs/Readme.md",
		"docs/readme.md",
		"CON.txt",
	})

	if len(issues) != 2 {
		t.Fatalf("issue count = %d, want 2", len(issues))
	}
	if issues[0].Kind != pathPolicyIssueCaseCollision {
		t.Fatalf("first issue kind = %q, want %q", issues[0].Kind, pathPolicyIssueCaseCollision)
	}
	if len(issues[0].Paths) != 2 {
		t.Fatalf("collision paths = %v, want 2 entries", issues[0].Paths)
	}
	if issues[1].Kind != pathPolicyIssueWindowsInvalid {
		t.Fatalf("second issue kind = %q, want %q", issues[1].Kind, pathPolicyIssueWindowsInvalid)
	}
}

func TestReconcilerSkipsWindowsCaseCollisionPaths(t *testing.T) {
	withWindowsPathPolicy(t, true)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatal(err)
	}

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	putAndLog(t, store, localLog, "Docs/Readme.md", "upper", 1)
	putAndLog(t, store, localLog, "docs/readme.md", "lower", 2)

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(context.Background())

	if _, err := os.Stat(filepath.Join(localDir, "Docs", "Readme.md")); !os.IsNotExist(err) {
		t.Fatalf("Docs/Readme.md should not be materialized under Windows policy")
	}
	if _, err := os.Stat(filepath.Join(localDir, "docs", "readme.md")); !os.IsNotExist(err) {
		t.Fatalf("docs/readme.md should not be materialized under Windows policy")
	}
}

func TestReconcilerSkipsInvalidWindowsPath(t *testing.T) {
	withWindowsPathPolicy(t, true)

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := store.Put(ctx, "CON.txt", bytes.NewReader([]byte("bad"))); err != nil {
		t.Fatalf("store.Put(CON.txt): %v", err)
	}
	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult() = nil")
	}

	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), "different-device")
	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "CON.txt",
		Chunks:    res.Chunks,
		Checksum:  res.Checksum,
		Size:      res.Size,
		Namespace: "default",
		Device:    "remote-device",
		Timestamp: 1001,
		Seq:       1,
	}); err != nil {
		t.Fatalf("localLog.Append(CON.txt): %v", err)
	}

	r := NewReconciler(store, localLog, NewSyncLog[OutboxEntry](filepath.Join(tmpDir, "outbox.jsonl")), localDir, nil, nil)
	r.reconcile(ctx)

	if _, err := os.Stat(filepath.Join(localDir, "CON.txt")); !os.IsNotExist(err) {
		t.Fatalf("CON.txt should not be materialized under Windows policy")
	}
}
