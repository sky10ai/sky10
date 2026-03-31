package kv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaselineStore manages per-remote-device baseline snapshots on local disk.
// A baseline is the last-downloaded snapshot for a remote device, used by
// the poller to detect adds, modifications, and deletes via diff.
type BaselineStore struct {
	dir string
}

// NewBaselineStore creates a baseline store at the given directory.
func NewBaselineStore(dir string) *BaselineStore {
	os.MkdirAll(dir, 0700)
	return &BaselineStore{dir: dir}
}

// Load returns the stored baseline snapshot for a remote device.
// Returns nil, nil if no baseline exists (new device).
func (b *BaselineStore) Load(deviceID string) (*Snapshot, error) {
	path := filepath.Join(b.dir, deviceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading kv baseline for %s: %w", deviceID, err)
	}
	return UnmarshalSnapshot(data)
}

// Save stores a remote device's snapshot as the new baseline.
func (b *BaselineStore) Save(deviceID string, snap *Snapshot) error {
	data, err := MarshalSnapshot(snap)
	if err != nil {
		return fmt.Errorf("marshaling kv baseline for %s: %w", deviceID, err)
	}
	path := filepath.Join(b.dir, deviceID+".json")
	return os.WriteFile(path, data, 0600)
}

// DeviceIDs returns the IDs of all devices with stored baselines.
func (b *BaselineStore) DeviceIDs() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return ids, nil
}
