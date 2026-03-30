package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

// BaselineStore manages per-remote-device baseline snapshots on local disk.
// A baseline is the last-downloaded snapshot for a remote device, used by
// the snapshot poller to detect adds, modifications, and deletes via diff.
type BaselineStore struct {
	dir string // e.g. ~/.sky10/fs/drives/{driveID}/baselines/
}

// NewBaselineStore creates a baseline store at the given directory.
func NewBaselineStore(dir string) *BaselineStore {
	os.MkdirAll(dir, 0700)
	return &BaselineStore{dir: dir}
}

// Load returns the stored baseline snapshot for a remote device.
// Returns nil, nil if no baseline exists (new device).
func (b *BaselineStore) Load(deviceID string) (*opslog.Snapshot, error) {
	path := filepath.Join(b.dir, deviceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading baseline for %s: %w", deviceID, err)
	}

	snap, err := opslog.UnmarshalSnapshot(data)
	if err != nil {
		return nil, fmt.Errorf("parsing baseline for %s: %w", deviceID, err)
	}
	return snap, nil
}

// Save stores a remote device's snapshot as the new baseline.
func (b *BaselineStore) Save(deviceID string, snap *opslog.Snapshot) error {
	data, err := opslog.MarshalSnapshot(snap)
	if err != nil {
		return fmt.Errorf("marshaling baseline for %s: %w", deviceID, err)
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

// loadDeviceList reads device IDs from the S3 devices/ registry.
func loadDeviceList(backend interface {
	List(ctx interface{ Done() <-chan struct{} }, prefix string) ([]string, error)
}, ctx interface{ Done() <-chan struct{} }) ([]string, error) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, k := range keys {
		name := strings.TrimPrefix(k, "devices/")
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids, nil
}

// parseDeviceList is a simpler helper that parses device IDs from S3 keys.
func parseDeviceList(keys []string) []string {
	var ids []string
	for _, k := range keys {
		name := strings.TrimPrefix(k, "devices/")
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids
}

// loadDeviceRegistry reads the devices/ prefix and returns registered
// device IDs.
func loadDeviceRegistry(data []byte) (string, error) {
	var info struct {
		PubKey string `json:"pubkey"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", err
	}
	return shortPubkeyID(info.PubKey), nil
}
