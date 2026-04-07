package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sky10/sky10/pkg/config"
)

// SyncedFile tracks a file's state at last successful sync.
type SyncedFile struct {
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// DriveManifest is the "last known agreed state" for a drive.
// Used for three-way diff: compare local filesystem and remote ops
// against this to determine what changed on each side.
type DriveManifest struct {
	LastRemoteOp int64                 `json:"last_remote_op"`
	Files        map[string]SyncedFile `json:"files"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
}

// LoadDriveManifest reads a manifest from disk.
// Returns an empty manifest if the file doesn't exist.
func LoadDriveManifest(driveID string) *DriveManifest {
	p := manifestPath(driveID)
	data, err := os.ReadFile(p)
	if err != nil {
		return newDriveManifest(p)
	}
	var m DriveManifest
	if json.Unmarshal(data, &m) != nil {
		return newDriveManifest(p)
	}
	m.path = p
	if m.Files == nil {
		m.Files = make(map[string]SyncedFile)
	}
	return &m
}

// LoadDriveManifestFromPath reads a manifest from a specific path.
func LoadDriveManifestFromPath(path string) *DriveManifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return newDriveManifest(path)
	}
	var m DriveManifest
	if json.Unmarshal(data, &m) != nil {
		return newDriveManifest(path)
	}
	m.path = path
	if m.Files == nil {
		m.Files = make(map[string]SyncedFile)
	}
	return &m
}

func newDriveManifest(path string) *DriveManifest {
	return &DriveManifest{
		Files: make(map[string]SyncedFile),
		path:  path,
	}
}

func manifestPath(driveID string) string {
	drivesDir, err := config.DrivesDir()
	if err != nil {
		return ""
	}
	return filepath.Join(drivesDir, driveID, "manifest.json")
}

// Save persists the manifest to disk.
func (m *DriveManifest) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.path == "" {
		return nil
	}

	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating manifest dir: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	return os.WriteFile(m.path, data, 0600)
}

// SetFile updates or adds a file entry.
func (m *DriveManifest) SetFile(path string, entry SyncedFile) {
	m.mu.Lock()
	m.Files[path] = entry
	m.mu.Unlock()
}

// RemoveFile removes a file entry.
func (m *DriveManifest) RemoveFile(path string) {
	m.mu.Lock()
	delete(m.Files, path)
	m.mu.Unlock()
}

// GetFile returns a file entry and whether it exists.
func (m *DriveManifest) GetFile(path string) (SyncedFile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.Files[path]
	return e, ok
}

// SetLastRemoteOp updates the remote ops cursor.
func (m *DriveManifest) SetLastRemoteOp(ts int64) {
	m.mu.Lock()
	m.LastRemoteOp = ts
	m.mu.Unlock()
}
