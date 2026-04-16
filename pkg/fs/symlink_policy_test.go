package fs

import (
	"path/filepath"
	"testing"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

func withSymlinkCapabilityDetector(t *testing.T, detector func() symlinkCapability) {
	t.Helper()
	prev := symlinkCapabilityDetector
	symlinkCapabilityDetector = detector
	t.Cleanup(func() {
		symlinkCapabilityDetector = prev
	})
}

func TestUnsupportedWindowsSymlinkIssueForCapability(t *testing.T) {
	localLog := opslog.NewLocalOpsLog(filepath.Join(t.TempDir(), "ops.jsonl"), "dev-a")
	if err := localLog.Append(opslog.Entry{
		Type:       opslog.Symlink,
		Path:       "link.txt",
		Checksum:   symlinkChecksum("target.txt"),
		LinkTarget: "target.txt",
		Device:     "dev-a",
		Timestamp:  1,
		Seq:        1,
	}); err != nil {
		t.Fatalf("append symlink: %v", err)
	}

	snap, err := localLog.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	issue := unsupportedWindowsSymlinkIssueForCapability(snap, symlinkCapability{
		Supported: false,
		Reason:    "developer mode is disabled",
	})
	if issue == nil {
		t.Fatal("issue = nil, want symlink capability issue")
	}
	if issue.Kind != pathPolicyIssueWindowsSymlinkUnsupported {
		t.Fatalf("issue kind = %q, want %q", issue.Kind, pathPolicyIssueWindowsSymlinkUnsupported)
	}
	if len(issue.Paths) != 1 || issue.Paths[0] != "link.txt" {
		t.Fatalf("issue paths = %v, want [link.txt]", issue.Paths)
	}
}

func TestInferSymlinkTargetKindDirectoryFromSnapshot(t *testing.T) {
	kind := inferSymlinkTargetKind("/unused", "links/docs", "../docs", nil, map[string]opslog.DirInfo{
		"docs": {},
	})
	if kind != symlinkTargetDirectory {
		t.Fatalf("kind = %q, want %q", kind, symlinkTargetDirectory)
	}
}

func TestInferSymlinkTargetKindFileFromSnapshot(t *testing.T) {
	kind := inferSymlinkTargetKind("/unused", "links/readme", "../README.md", map[string]opslog.FileInfo{
		"README.md": {},
	}, nil)
	if kind != symlinkTargetFile {
		t.Fatalf("kind = %q, want %q", kind, symlinkTargetFile)
	}
}

func TestInferSymlinkTargetKindUnknownForExternalDanglingTarget(t *testing.T) {
	kind := inferSymlinkTargetKind("/unused", "links/external", "../../missing-dir", nil, nil)
	if kind != symlinkTargetUnknown {
		t.Fatalf("kind = %q, want %q", kind, symlinkTargetUnknown)
	}
}
