package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileState tracks a file's checksum and namespace for diff detection
// and delete op construction. NOT used for UI.
type FileState struct {
	Checksum  string `json:"checksum"`
	Namespace string `json:"namespace"`
}

// DriveState is the minimal state needed for sync decisions.
// Maps file paths to their last-known checksum and namespace.
// Used to: detect what changed since last sync, provide info
// for delete ops, detect directory-trash missed deletes.
type DriveState struct {
	LastRemoteOp int64                `json:"last_remote_op"`
	Files        map[string]FileState `json:"files"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
}

// LoadDriveState reads state from disk. Returns empty state if missing.
func LoadDriveState(driveID string) *DriveState {
	home, err := os.UserHomeDir()
	if err != nil {
		return newDriveState("")
	}
	p := filepath.Join(home, ".sky10", "fs", "drives", driveID, "state.json")
	return LoadDriveStateFromPath(p)
}

// LoadDriveStateFromPath reads state from a specific path.
func LoadDriveStateFromPath(path string) *DriveState {
	data, err := os.ReadFile(path)
	if err != nil {
		return newDriveState(path)
	}
	var s DriveState
	if json.Unmarshal(data, &s) != nil {
		return newDriveState(path)
	}
	s.path = path
	if s.Files == nil {
		s.Files = make(map[string]FileState)
	}
	return &s
}

func newDriveState(path string) *DriveState {
	return &DriveState{
		Files: make(map[string]FileState),
		path:  path,
	}
}

// Save persists state to disk.
func (s *DriveState) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	os.MkdirAll(dir, 0700)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	return os.WriteFile(s.path, data, 0600)
}

// SetFile updates or adds a file entry.
func (s *DriveState) SetFile(path string, state FileState) {
	s.mu.Lock()
	s.Files[path] = state
	s.mu.Unlock()
}

// RemoveFile removes a file entry.
func (s *DriveState) RemoveFile(path string) {
	s.mu.Lock()
	delete(s.Files, path)
	s.mu.Unlock()
}

// GetFile returns a file's state.
func (s *DriveState) GetFile(path string) (FileState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.Files[path]
	return f, ok
}

// SetLastRemoteOp updates the poller cursor.
func (s *DriveState) SetLastRemoteOp(ts int64) {
	s.mu.Lock()
	s.LastRemoteOp = ts
	s.mu.Unlock()
}
