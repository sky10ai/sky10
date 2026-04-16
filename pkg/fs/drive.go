package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
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
	daemons        map[string]*driveRuntime
	pollSeconds    int
	mu             sync.RWMutex
	muHolder       string // debug: last write-lock caller
	cfgPath        string
	Logger         *slog.Logger                 // shared logger (with log buffer)
	OnActivity     func()                       // called when any drive does sync I/O
	OnStateChanged func(string, map[string]any) // called when manifest changes
	p2pSync        *P2PSync
}

type driveRuntime struct {
	cancel    context.CancelFunc
	replicaID string
	daemon    *DaemonV2_5
}

// wLock acquires write lock and records the caller for debugging.
func (dm *DriveManager) wLock(caller string) {
	dm.mu.Lock()
	dm.muHolder = caller
}

// wUnlock releases write lock and clears the holder.
func (dm *DriveManager) wUnlock() {
	dm.muHolder = ""
	dm.mu.Unlock()
}

// MuHolder returns who last acquired the write lock (for diagnostics).
func (dm *DriveManager) MuHolder() string {
	return dm.muHolder
}

// NewDriveManager creates a drive manager that persists config to cfgPath.
func NewDriveManager(store *Store, cfgPath string) *DriveManager {
	dm := &DriveManager{
		store:       store,
		drives:      make(map[string]*Drive),
		daemons:     make(map[string]*driveRuntime),
		pollSeconds: 30,
		cfgPath:     cfgPath,
	}
	dm.load()
	return dm
}

// SetP2PSync attaches the shared FS P2P sync manager used by all drives.
func (dm *DriveManager) SetP2PSync(sync *P2PSync) {
	dm.wLock("SetP2PSync")
	dm.p2pSync = sync
	dm.wUnlock()
}

// CreateDrive adds a new drive. Creates the local directory if needed.
// The caller provides the full local path — no default assumed.
func (dm *DriveManager) CreateDrive(name, localPath, namespace string) (*Drive, error) {
	dm.wLock("CreateDrive")
	defer dm.wUnlock()

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
	dm.wLock("RemoveDrive")
	defer dm.wUnlock()

	if runtime, ok := dm.daemons[id]; ok {
		if dm.p2pSync != nil && runtime.replicaID != "" {
			dm.p2pSync.RemoveReplica(runtime.replicaID)
		}
		runtime.cancel()
		delete(dm.daemons, id)
	}
	delete(dm.drives, id)
	dm.save()
	return nil
}

// ListDrives returns all configured drives, sorted by name.
func (dm *DriveManager) ListDrives() []*Drive {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	result := make([]*Drive, 0, len(dm.drives))
	for _, d := range dm.drives {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// GetDrive returns a drive by ID.
func (dm *DriveManager) GetDrive(id string) *Drive {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.drives[id]
}

// StartDrive starts syncing a drive.
func (dm *DriveManager) StartDrive(id string, logger interface{ Info(string, ...any) }) error {
	dm.wLock("StartDrive:entry")
	drive, ok := dm.drives[id]
	if !ok {
		dm.wUnlock()
		return fmt.Errorf("drive not found: %s", id)
	}

	// Stop if already running
	if runtime, running := dm.daemons[id]; running {
		if dm.p2pSync != nil && runtime.replicaID != "" {
			dm.p2pSync.RemoveReplica(runtime.replicaID)
		}
		runtime.cancel()
	}
	dm.wUnlock()

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
		DriveName:   drive.Name,
		PollSeconds: dm.pollSeconds,
	}

	// Each drive gets its own store with the drive's namespace set,
	// so all files uploaded through this drive use one namespace key.
	driveStore := NewWithDevice(dm.store.backend, dm.store.identity, dm.store.deviceID)
	driveStore.SetClient(dm.store.clientID)
	if drive.Namespace != "" {
		driveStore.SetNamespace(drive.Namespace)
	}
	if dm.p2pSync != nil {
		driveStore.SetPeerChunkFetcher(dm.p2pSync)
	}

	daemon, err := NewDaemonV2_5(driveStore, daemonCfg, dm.Logger)
	if err != nil {
		cancel()
		return fmt.Errorf("creating daemon for %s: %w", drive.Name, err)
	}
	if dm.OnStateChanged != nil {
		daemon.onEvent = dm.OnStateChanged
	}

	replicaID := ""
	if dm.p2pSync != nil {
		replicaID = fmt.Sprintf("%s:%d", id, time.Now().UnixNano())
		dm.p2pSync.AddReplica(daemon.peerReplica(replicaID))
		daemon.peerSyncPoke = func() {
			dm.p2pSync.PushToAll(context.Background())
		}
	}

	runtime := &driveRuntime{
		cancel:    cancel,
		replicaID: replicaID,
		daemon:    daemon,
	}
	dm.wLock("StartDrive:register")
	dm.daemons[id] = runtime
	dm.wUnlock()

	go func() {
		defer func() {
			if dm.p2pSync != nil && runtime.replicaID != "" {
				dm.p2pSync.RemoveReplica(runtime.replicaID)
			}
			dm.wLock("StartDrive:cleanup")
			if current, ok := dm.daemons[id]; ok && current == runtime {
				delete(dm.daemons, id)
			}
			dm.wUnlock()
		}()
		daemon.Run(ctx)
	}()

	return nil
}

// StopDrive stops syncing a drive.
func (dm *DriveManager) StopDrive(id string) {
	dm.wLock("StopDrive")
	defer dm.wUnlock()

	if runtime, ok := dm.daemons[id]; ok {
		if dm.p2pSync != nil && runtime.replicaID != "" {
			dm.p2pSync.RemoveReplica(runtime.replicaID)
		}
		runtime.cancel()
		delete(dm.daemons, id)
	}
}

// StopAll stops all running daemons.
func (dm *DriveManager) StopAll() {
	dm.wLock("StopAll")
	defer dm.wUnlock()

	for id, runtime := range dm.daemons {
		if dm.p2pSync != nil && runtime.replicaID != "" {
			dm.p2pSync.RemoveReplica(runtime.replicaID)
		}
		runtime.cancel()
		delete(dm.daemons, id)
	}
}

// IsRunning returns whether a drive's daemon is active.
func (dm *DriveManager) IsRunning(id string) bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	_, ok := dm.daemons[id]
	return ok
}

func (dm *DriveManager) readSourceSnapshot(id string) readSourceStatsSnapshot {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	runtime, ok := dm.daemons[id]
	if !ok || runtime == nil || runtime.daemon == nil {
		return readSourceStatsSnapshot{}
	}
	return runtime.daemon.readSourceSnapshot()
}

func (dm *DriveManager) readSourceSnapshots() map[string]readSourceStatsSnapshot {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	out := make(map[string]readSourceStatsSnapshot, len(dm.daemons))
	for id, runtime := range dm.daemons {
		if runtime == nil || runtime.daemon == nil {
			continue
		}
		out[id] = runtime.daemon.readSourceSnapshot()
	}
	return out
}

func (dm *DriveManager) sourceHealthSnapshot(id string) chunkSourceHealthSnapshots {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	runtime, ok := dm.daemons[id]
	if !ok || runtime == nil || runtime.daemon == nil {
		return chunkSourceHealthSnapshots{}
	}
	return runtime.daemon.sourceHealthSnapshot()
}

func (dm *DriveManager) pathPolicyIssuesSnapshot(id string) []pathPolicyIssue {
	dir := driveDataDir(id)
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), dm.store.deviceID)
	snap, err := localLog.Snapshot()
	if err != nil {
		return nil
	}
	return activeSnapshotPathIssues(snap)
}

// StartAll starts all enabled drives.
func (dm *DriveManager) StartAll(logger interface{ Info(string, ...any) }) {
	dm.wLock("StartAll")
	drives := make([]*Drive, 0, len(dm.drives))
	for _, d := range dm.drives {
		if d.Enabled {
			drives = append(drives, d)
		}
	}
	dm.wUnlock()

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
