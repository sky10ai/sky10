package x402

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RegistrySnapshot is the on-disk shape of the registry. Loaders
// restore from this; persistence writes this back on every change.
type RegistrySnapshot struct {
	Manifests   []ServiceManifest  `json:"manifests,omitempty"`
	Policy      []PolicyEntry      `json:"policy,omitempty"`
	Approvals   []Approval         `json:"approvals,omitempty"`
	Pins        []Pin              `json:"pins,omitempty"`
	UserEnabled []UserEnableRecord `json:"user_enabled,omitempty"`
}

// RegistryStore persists registry snapshots. Implementations include
// FileRegistryStore (default, atomic JSON file) and MemoryRegistryStore
// (test-only, in-memory).
type RegistryStore interface {
	Load() (RegistrySnapshot, error)
	Save(RegistrySnapshot) error
}

// FileRegistryStore stores the snapshot as JSON at a single path.
// Saves are atomic via temp-file-and-rename so a crash mid-write
// cannot corrupt the file. Concurrent saves serialize on an internal
// mutex.
type FileRegistryStore struct {
	mu   sync.Mutex
	path string
}

// NewFileRegistryStore constructs a file-backed store. The directory
// containing path is created on first save if it does not exist.
func NewFileRegistryStore(path string) *FileRegistryStore {
	return &FileRegistryStore{path: path}
}

// Load reads the snapshot from disk. A missing file returns an empty
// snapshot rather than an error, since a fresh installation has no
// registry to load.
func (s *FileRegistryStore) Load() (RegistrySnapshot, error) {
	if s == nil {
		return RegistrySnapshot{}, errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	buf, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return RegistrySnapshot{}, nil
		}
		return RegistrySnapshot{}, fmt.Errorf("read %s: %w", s.path, err)
	}
	var snap RegistrySnapshot
	if err := json.Unmarshal(buf, &snap); err != nil {
		return RegistrySnapshot{}, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return snap, nil
}

// Save writes the snapshot atomically: write to a temp file, fsync,
// rename over the target. The directory is created if missing.
func (s *FileRegistryStore) Save(snap RegistrySnapshot) error {
	if s == nil {
		return errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", s.path, err)
	}
	buf, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	buf = append(buf, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleaned = true
	return nil
}

// MemoryRegistryStore is a test-only in-memory store. Useful when a
// test wants to verify Save was called or to seed Load with a fixture
// without writing to disk.
type MemoryRegistryStore struct {
	mu   sync.Mutex
	snap RegistrySnapshot
}

// NewMemoryRegistryStore constructs an empty memory store.
func NewMemoryRegistryStore() *MemoryRegistryStore {
	return &MemoryRegistryStore{}
}

// Load returns the in-memory snapshot.
func (s *MemoryRegistryStore) Load() (RegistrySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap, nil
}

// Save replaces the in-memory snapshot.
func (s *MemoryRegistryStore) Save(snap RegistrySnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap = snap
	return nil
}
