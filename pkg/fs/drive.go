package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Drive represents a named sync folder mapped to a remote namespace.
type Drive struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPath string `json:"local_path"`
	Namespace string `json:"namespace"` // remote namespace to sync
	Enabled   bool   `json:"enabled"`
}

// DriveManager manages multiple sync drives, each with its own daemon.
type DriveManager struct {
	store          *Store
	drives         map[string]*Drive
	daemons        map[string]context.CancelFunc
	mu             sync.Mutex
	cfgPath        string
	OnActivity     func()       // called when any drive does sync I/O
	OnStateChanged func(string) // called when manifest changes
}

// NewDriveManager creates a drive manager that persists config to cfgPath.
func NewDriveManager(store *Store, cfgPath string) *DriveManager {
	dm := &DriveManager{
		store:   store,
		drives:  make(map[string]*Drive),
		daemons: make(map[string]context.CancelFunc),
		cfgPath: cfgPath,
	}
	dm.load()
	return dm
}

// CreateDrive adds a new drive. Creates the local directory if needed.
// The caller provides the full local path — no default assumed.
func (dm *DriveManager) CreateDrive(name, localPath, namespace string) (*Drive, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if err := os.MkdirAll(localPath, 0755); err != nil {
		return nil, fmt.Errorf("creating drive directory: %w", err)
	}

	id := fmt.Sprintf("drive_%s", name)
	drive := &Drive{
		ID:        id,
		Name:      name,
		LocalPath: localPath,
		Namespace: namespace,
		Enabled:   true,
	}

	dm.drives[id] = drive
	dm.save()
	return drive, nil
}

// RemoveDrive stops and removes a drive. Does not delete local files.
func (dm *DriveManager) RemoveDrive(id string) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if cancel, ok := dm.daemons[id]; ok {
		cancel()
		delete(dm.daemons, id)
	}
	delete(dm.drives, id)
	dm.save()
	return nil
}

// ListDrives returns all configured drives.
func (dm *DriveManager) ListDrives() []*Drive {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var result []*Drive
	for _, d := range dm.drives {
		result = append(result, d)
	}
	return result
}

// GetDrive returns a drive by ID.
func (dm *DriveManager) GetDrive(id string) *Drive {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.drives[id]
}

// StartDrive starts syncing a drive.
func (dm *DriveManager) StartDrive(id string, logger interface{ Info(string, ...any) }) error {
	dm.mu.Lock()
	drive, ok := dm.drives[id]
	if !ok {
		dm.mu.Unlock()
		return fmt.Errorf("drive not found: %s", id)
	}

	// Stop if already running
	if cancel, running := dm.daemons[id]; running {
		cancel()
	}
	dm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	ignoreMatcher := NewIgnoreMatcher(drive.LocalPath)
	cfg := SyncConfig{
		LocalRoot:  drive.LocalPath,
		IgnoreFunc: ignoreMatcher.IgnoreFunc(),
	}
	if drive.Namespace != "" {
		cfg.Namespaces = []string{drive.Namespace}
	}

	daemonCfg := DaemonConfig{
		SyncConfig:  cfg,
		DriveID:     id,
		PollSeconds: 30,
	}

	// Each drive gets its own store with the drive's namespace set,
	// so all files uploaded through this drive use one namespace key.
	driveStore := NewWithDevice(dm.store.backend, dm.store.identity, dm.store.deviceID)
	driveStore.SetClient(dm.store.clientID)
	if drive.Namespace != "" {
		driveStore.SetNamespace(drive.Namespace)
	}

	daemon, err := NewDaemonV2_5(driveStore, daemonCfg, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("creating daemon for %s: %w", drive.Name, err)
	}
	if dm.OnStateChanged != nil {
		daemon.onEvent = dm.OnStateChanged
	}

	dm.mu.Lock()
	dm.daemons[id] = cancel
	dm.mu.Unlock()

	go func() {
		daemon.Run(ctx)
		dm.mu.Lock()
		delete(dm.daemons, id)
		dm.mu.Unlock()
	}()

	return nil
}

// StopDrive stops syncing a drive.
func (dm *DriveManager) StopDrive(id string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if cancel, ok := dm.daemons[id]; ok {
		cancel()
		delete(dm.daemons, id)
	}
}

// StopAll stops all running daemons.
func (dm *DriveManager) StopAll() {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	for id, cancel := range dm.daemons {
		cancel()
		delete(dm.daemons, id)
	}
}

// IsRunning returns whether a drive's daemon is active.
func (dm *DriveManager) IsRunning(id string) bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	_, ok := dm.daemons[id]
	return ok
}

// StartAll starts all enabled drives.
func (dm *DriveManager) StartAll(logger interface{ Info(string, ...any) }) {
	dm.mu.Lock()
	drives := make([]*Drive, 0, len(dm.drives))
	for _, d := range dm.drives {
		if d.Enabled {
			drives = append(drives, d)
		}
	}
	dm.mu.Unlock()

	for _, d := range drives {
		dm.StartDrive(d.ID, logger)
	}
}

func (dm *DriveManager) save() {
	drives := make([]*Drive, 0, len(dm.drives))
	for _, d := range dm.drives {
		drives = append(drives, d)
	}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(dm.cfgPath, data, 0600)
}

func (dm *DriveManager) load() {
	data, err := os.ReadFile(dm.cfgPath)
	if err != nil {
		return
	}
	var drives []*Drive
	if json.Unmarshal(data, &drives) == nil {
		for _, d := range drives {
			dm.drives[d.ID] = d
		}
	}
}
