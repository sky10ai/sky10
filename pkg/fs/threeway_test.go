package fs

import (
	"testing"
)

func TestThreeWayDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		localFiles map[string]string // path → checksum
		manifest   map[string]SyncedFile
		remoteOps  []Op
		wantCount  int
		wantType   SyncActionType
		wantPath   string
		wantReason string
	}{
		{
			name:       "no changes anywhere",
			localFiles: map[string]string{"a.txt": "aaa"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "aaa"}},
			remoteOps:  nil,
			wantCount:  0,
		},
		{
			name:       "local add",
			localFiles: map[string]string{"new.txt": "nnn"},
			manifest:   map[string]SyncedFile{},
			remoteOps:  nil,
			wantCount:  1,
			wantType:   ActionUpload,
			wantPath:   "new.txt",
			wantReason: "added locally",
		},
		{
			name:       "local modify",
			localFiles: map[string]string{"a.txt": "modified"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "original"}},
			remoteOps:  nil,
			wantCount:  1,
			wantType:   ActionUpload,
			wantPath:   "a.txt",
			wantReason: "modified locally",
		},
		{
			name:       "local delete",
			localFiles: map[string]string{},
			manifest:   map[string]SyncedFile{"gone.txt": {Checksum: "xxx"}},
			remoteOps:  nil,
			wantCount:  1,
			wantType:   ActionDeleteRemote,
			wantPath:   "gone.txt",
			wantReason: "deleted locally",
		},
		{
			name:       "remote add",
			localFiles: map[string]string{},
			manifest:   map[string]SyncedFile{},
			remoteOps:  []Op{{Type: OpPut, Path: "from-b.txt", Checksum: "bbb", Size: 10}},
			wantCount:  1,
			wantType:   ActionDownload,
			wantPath:   "from-b.txt",
			wantReason: "added on remote",
		},
		{
			name:       "remote modify",
			localFiles: map[string]string{"a.txt": "old"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "old"}},
			remoteOps:  []Op{{Type: OpPut, Path: "a.txt", Checksum: "new", Size: 20}},
			wantCount:  1,
			wantType:   ActionDownload,
			wantPath:   "a.txt",
			wantReason: "modified on remote",
		},
		{
			name:       "remote delete",
			localFiles: map[string]string{"a.txt": "aaa"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "aaa"}},
			remoteOps:  []Op{{Type: OpDelete, Path: "a.txt"}},
			wantCount:  1,
			wantType:   ActionDeleteLocal,
			wantPath:   "a.txt",
			wantReason: "deleted on remote",
		},
		{
			name:       "conflict: both modified",
			localFiles: map[string]string{"a.txt": "local-edit"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "original"}},
			remoteOps:  []Op{{Type: OpPut, Path: "a.txt", Checksum: "remote-edit", Size: 30}},
			wantCount:  1,
			wantType:   ActionConflict,
			wantPath:   "a.txt",
			wantReason: "modified on both sides",
		},
		{
			name:       "conflict: local delete + remote modify → download",
			localFiles: map[string]string{},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "original"}},
			remoteOps:  []Op{{Type: OpPut, Path: "a.txt", Checksum: "remote-edit", Size: 30}},
			wantCount:  1,
			wantType:   ActionDownload,
			wantPath:   "a.txt",
			wantReason: "deleted locally but modified on remote",
		},
		{
			name:       "conflict: local modify + remote delete → upload",
			localFiles: map[string]string{"a.txt": "local-edit"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "original"}},
			remoteOps:  []Op{{Type: OpDelete, Path: "a.txt"}},
			wantCount:  1,
			wantType:   ActionUpload,
			wantPath:   "a.txt",
			wantReason: "modified locally but deleted on remote",
		},
		{
			name:       "both deleted → no action",
			localFiles: map[string]string{},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "original"}},
			remoteOps:  []Op{{Type: OpDelete, Path: "a.txt"}},
			wantCount:  0,
		},
		{
			name:       "conflict: both added same path",
			localFiles: map[string]string{"new.txt": "local-version"},
			manifest:   map[string]SyncedFile{},
			remoteOps:  []Op{{Type: OpPut, Path: "new.txt", Checksum: "remote-version", Size: 10}},
			wantCount:  1,
			wantType:   ActionConflict,
			wantPath:   "new.txt",
		},
		{
			name:       "file unchanged locally, no remote change",
			localFiles: map[string]string{"a.txt": "same"},
			manifest:   map[string]SyncedFile{"a.txt": {Checksum: "same"}},
			remoteOps:  nil,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manifest := newDriveManifest("")
			manifest.Files = tt.manifest

			actions := ThreeWayDiff(tt.localFiles, manifest, tt.remoteOps)

			if len(actions) != tt.wantCount {
				t.Fatalf("got %d actions, want %d: %+v", len(actions), tt.wantCount, actions)
			}

			if tt.wantCount == 0 {
				return
			}

			a := actions[0]
			if a.Type != tt.wantType {
				t.Errorf("type = %d, want %d", a.Type, tt.wantType)
			}
			if tt.wantPath != "" && a.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", a.Path, tt.wantPath)
			}
			if tt.wantReason != "" && a.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", a.Reason, tt.wantReason)
			}
		})
	}
}

func TestThreeWayDiffMultipleFiles(t *testing.T) {
	t.Parallel()

	localFiles := map[string]string{
		"unchanged.txt": "aaa",
		"modified.txt":  "new-content",
		"new-local.txt": "brand-new",
		// "deleted.txt" is missing — was deleted locally
	}

	manifest := newDriveManifest("")
	manifest.Files = map[string]SyncedFile{
		"unchanged.txt": {Checksum: "aaa"},
		"modified.txt":  {Checksum: "old-content"},
		"deleted.txt":   {Checksum: "ddd"},
	}

	remoteOps := []Op{
		{Type: OpPut, Path: "from-remote.txt", Checksum: "rrr", Size: 50},
	}

	actions := ThreeWayDiff(localFiles, manifest, remoteOps)

	// Expect: upload modified.txt, upload new-local.txt, delete-remote deleted.txt, download from-remote.txt
	actionsByPath := make(map[string]SyncAction)
	for _, a := range actions {
		actionsByPath[a.Path] = a
	}

	if len(actions) != 4 {
		t.Fatalf("got %d actions, want 4: %+v", len(actions), actions)
	}

	if a, ok := actionsByPath["modified.txt"]; !ok || a.Type != ActionUpload {
		t.Errorf("modified.txt: want upload, got %+v", a)
	}
	if a, ok := actionsByPath["new-local.txt"]; !ok || a.Type != ActionUpload {
		t.Errorf("new-local.txt: want upload, got %+v", a)
	}
	if a, ok := actionsByPath["deleted.txt"]; !ok || a.Type != ActionDeleteRemote {
		t.Errorf("deleted.txt: want delete-remote, got %+v", a)
	}
	if a, ok := actionsByPath["from-remote.txt"]; !ok || a.Type != ActionDownload {
		t.Errorf("from-remote.txt: want download, got %+v", a)
	}

	// unchanged.txt should NOT appear
	if _, ok := actionsByPath["unchanged.txt"]; ok {
		t.Error("unchanged.txt should not produce an action")
	}
}

func TestThreeWayDiffRemoteOpsLatestWins(t *testing.T) {
	t.Parallel()

	// Multiple ops for same path — latest should win
	localFiles := map[string]string{}
	manifest := newDriveManifest("")

	remoteOps := []Op{
		{Type: OpPut, Path: "a.txt", Checksum: "v1", Size: 10, Timestamp: 1000},
		{Type: OpPut, Path: "a.txt", Checksum: "v2", Size: 20, Timestamp: 2000},
		{Type: OpPut, Path: "a.txt", Checksum: "v3", Size: 30, Timestamp: 3000},
	}

	actions := ThreeWayDiff(localFiles, manifest, remoteOps)

	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].RemoteOp.Checksum != "v3" {
		t.Errorf("should use latest op, got checksum %q", actions[0].RemoteOp.Checksum)
	}
}
