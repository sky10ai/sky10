package fs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SyncState tracks what this device has synced, enabling fast startup diffs.
type SyncState struct {
	// LastRemoteOp is the timestamp of the last S3 op this device has seen.
	// On startup, only fetch ops after this timestamp.
	LastRemoteOp int64 `json:"last_remote_op"`

	// Pending ops that haven't been pushed to S3 yet (for offline support).
	Pending []PendingOp `json:"pending,omitempty"`

	// LocalChecksums maps relative paths to their last-known local checksums.
	// Prevents full rescan on startup — only check files that changed.
	LocalChecksums map[string]string `json:"local_checksums,omitempty"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
}

// PendingOp is a local change not yet pushed to S3.
type PendingOp struct {
	Type      OpType `json:"op"`
	Path      string `json:"path"`
	LocalPath string `json:"local_path,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// LoadSyncState reads sync state from ~/.sky10/fs/sync-state.json.
// Returns empty state if the file doesn't exist.
func LoadSyncState() *SyncState {
	home, err := os.UserHomeDir()
	if err != nil {
		return newSyncState("")
	}
	p := filepath.Join(home, ".sky10", "fs", "sync-state.json")

	data, err := os.ReadFile(p)
	if err != nil {
		return newSyncState(p)
	}

	var s SyncState
	if json.Unmarshal(data, &s) != nil {
		return newSyncState(p)
	}
	s.path = p
	if s.LocalChecksums == nil {
		s.LocalChecksums = make(map[string]string)
	}
	return &s
}

func newSyncState(path string) *SyncState {
	return &SyncState{
		LocalChecksums: make(map[string]string),
		path:           path,
	}
}

// Save persists the sync state to disk.
func (s *SyncState) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return nil
	}

	dir := filepath.Dir(s.path)
	os.MkdirAll(dir, 0700)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

// SetLastRemoteOp updates the cursor and saves.
func (s *SyncState) SetLastRemoteOp(ts int64) {
	s.mu.Lock()
	s.LastRemoteOp = ts
	s.mu.Unlock()
	s.Save()
}

// SetLocalChecksum records a file's checksum after sync.
func (s *SyncState) SetLocalChecksum(path, checksum string) {
	s.mu.Lock()
	s.LocalChecksums[path] = checksum
	s.mu.Unlock()
}

// RemoveLocalChecksum removes a file's tracked checksum (file was deleted).
func (s *SyncState) RemoveLocalChecksum(path string) {
	s.mu.Lock()
	delete(s.LocalChecksums, path)
	s.mu.Unlock()
}

// AddPending queues a local change for later push to S3.
func (s *SyncState) AddPending(op PendingOp) {
	s.mu.Lock()
	op.Timestamp = time.Now().Unix()
	s.Pending = append(s.Pending, op)
	s.mu.Unlock()
	s.Save()
}

// ClearPending removes all pending ops (after successful push).
func (s *SyncState) ClearPending() {
	s.mu.Lock()
	s.Pending = nil
	s.mu.Unlock()
	s.Save()
}
