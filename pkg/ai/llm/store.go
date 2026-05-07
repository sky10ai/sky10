package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

// Snapshot is the on-disk AI connection settings shape.
type Snapshot struct {
	Connections []Connection `json:"connections,omitempty"`
}

// Store persists AI connection settings as one local JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

// NewStore constructs a Store at path.
func NewStore(path string) *Store {
	return &Store{path: path, now: time.Now}
}

// NewDefaultStore constructs the daemon's default AI connection store.
func NewDefaultStore() (*Store, error) {
	root, err := config.RootDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(root, "inference", "connections.json")), nil
}

// List returns all configured connections sorted by id.
func (s *Store) List() ([]Connection, error) {
	if s == nil {
		return nil, errors.New("nil AI connection store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := append([]Connection(nil), snap.Connections...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns one configured connection by id.
func (s *Store) Get(id string) (Connection, bool, error) {
	if s == nil {
		return Connection{}, false, errors.New("nil AI connection store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadLocked()
	if err != nil {
		return Connection{}, false, err
	}
	for _, c := range snap.Connections {
		if c.ID == id {
			return c, true, nil
		}
	}
	return Connection{}, false, nil
}

// Upsert validates and persists one connection.
func (s *Store) Upsert(ctx context.Context, connection Connection) (Connection, error) {
	if s == nil {
		return Connection{}, errors.New("nil AI connection store")
	}
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadLocked()
	if err != nil {
		return Connection{}, err
	}
	index := -1
	var existing *Connection
	for i := range snap.Connections {
		if snap.Connections[i].ID == connection.ID {
			index = i
			existing = &snap.Connections[i]
			break
		}
	}
	normalized, err := normalizeConnection(connection, existing, s.now())
	if err != nil {
		return Connection{}, err
	}
	if index >= 0 {
		snap.Connections[index] = normalized
	} else {
		snap.Connections = append(snap.Connections, normalized)
	}
	sort.Slice(snap.Connections, func(i, j int) bool {
		return snap.Connections[i].ID < snap.Connections[j].ID
	})
	if err := s.saveLocked(snap); err != nil {
		return Connection{}, err
	}
	return normalized, nil
}

// Delete removes one connection. Deleting a missing connection is a no-op.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil {
		return errors.New("nil AI connection store")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, err := s.loadLocked()
	if err != nil {
		return err
	}
	out := snap.Connections[:0]
	for _, c := range snap.Connections {
		if c.ID != id {
			out = append(out, c)
		}
	}
	snap.Connections = out
	return s.saveLocked(snap)
}

func (s *Store) loadLocked() (Snapshot, error) {
	if s.path == "" {
		return Snapshot{}, errors.New("AI connection store path is required")
	}
	buf, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, fmt.Errorf("read AI connections: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(buf, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("parse AI connections: %w", err)
	}
	normalized := snap.Connections[:0]
	for _, c := range snap.Connections {
		loadNow := c.UpdatedAt
		if loadNow.IsZero() {
			loadNow = s.now()
		}
		n, err := normalizeConnection(c, &c, loadNow)
		if err != nil {
			return Snapshot{}, fmt.Errorf("normalize connection %q: %w", c.ID, err)
		}
		normalized = append(normalized, n)
	}
	snap.Connections = normalized
	return snap, nil
}

func (s *Store) saveLocked(snap Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create AI connections config directory: %w", err)
	}
	buf, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode AI connections: %w", err)
	}
	buf = append(buf, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp AI connections: %w", err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp AI connections: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp AI connections: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp AI connections: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod temp AI connections: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename AI connections: %w", err)
	}
	renamed = true
	return nil
}
