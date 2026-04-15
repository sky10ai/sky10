package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/join"
	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/rpc"
)

// FSHandler implements rpc.Handler for the skyfs.* method namespace.
type FSHandler struct {
	store        *Store
	server       *rpc.Server
	driveManager *DriveManager
	logBuf       *logging.Buffer
	logger       *slog.Logger
	version      string

	syncMu     sync.Mutex
	syncCancel context.CancelFunc
	syncDir    string
	syncing    bool

	activityMu   sync.Mutex
	lastActivity time.Time
	startTime    time.Time

	mu               sync.Mutex
	completedInvites map[string]bool
	peerDevices      func() []DeviceInfo // returns connected peer devices (P2P mode)
}

// NewFSHandler creates an FSHandler wired to the given store and rpc.Server.
func NewFSHandler(store *Store, server *rpc.Server, driveCfgPath string, logger *slog.Logger, logBuf *logging.Buffer) *FSHandler {
	logger = componentLogger(logger)
	if logBuf == nil {
		logBuf = logging.NewBuffer(logging.DefaultBufferLines)
	}

	dm := NewDriveManager(store, driveCfgPath)
	dm.Logger = logger

	h := &FSHandler{
		store:            store,
		server:           server,
		driveManager:     dm,
		logBuf:           logBuf,
		logger:           logger,
		version:          server.Version(),
		startTime:        time.Now(),
		completedInvites: make(map[string]bool),
	}

	dm.OnActivity = h.MarkActivity
	dm.OnStateChanged = func(event string, data map[string]any) {
		server.Emit(event, data)
	}

	return h
}

// SetPeerDevices sets a callback that returns connected peer device info.
// Used in P2P-only mode to include peers in the device list.
func (s *FSHandler) SetPeerDevices(fn func() []DeviceInfo) {
	s.peerDevices = fn
}

// SetP2PSync attaches the shared FS P2P sync manager for all drives.
func (s *FSHandler) SetP2PSync(sync *P2PSync) {
	s.driveManager.SetP2PSync(sync)
}

// NamespaceKeys returns the configured FS drive namespace keys for P2P join.
func (s *FSHandler) NamespaceKeys(ctx context.Context) []join.NSKey {
	drives := s.driveManager.ListDrives()
	seen := make(map[string]bool)
	out := make([]join.NSKey, 0, len(drives))
	for _, drive := range drives {
		if drive == nil || drive.Namespace == "" || seen[drive.Namespace] {
			continue
		}
		key, err := s.store.getOrCreateNamespaceKey(ctx, drive.Namespace)
		if err != nil {
			s.logger.Warn("fs namespace key unavailable", "namespace", drive.Namespace, "error", err)
			continue
		}
		out = append(out, join.NSKey{
			Namespace: drive.Namespace,
			Key:       key,
		})
		seen[drive.Namespace] = true
	}
	return out
}

// StartDrives auto-starts all enabled drives. Called from server.OnServe.
func (s *FSHandler) StartDrives() {
	s.driveManager.StartAll(s.logger)
}

// SetDrivePollSeconds updates the default remote poll interval for drives.
func (s *FSHandler) SetDrivePollSeconds(seconds int) {
	if seconds <= 0 {
		return
	}
	s.driveManager.pollSeconds = seconds
}

// StartAutoApprove kicks off the auto-approve loop goroutine.
func (s *FSHandler) StartAutoApprove(ctx context.Context) {
	go s.autoApproveLoop(ctx)
}

// Logger returns the handler's logger.
func (s *FSHandler) Logger() *slog.Logger { return s.logger }

// Dispatch routes skyfs.* methods to handler functions.
func (s *FSHandler) Dispatch(ctx context.Context, method string, params json.RawMessage) (interface{}, error, bool) {
	var result interface{}
	var err error

	switch method {
	case "skyfs.ping":
		return map[string]string{"status": "ok"}, nil, true
	case "skyfs.logs":
		result, err = s.rpcLogs(ctx, params)
	case "skyfs.health":
		result, err = s.rpcHealth(ctx)
	case "skyfs.list", "skyfs.info", "skyfs.put", "skyfs.get",
		"skyfs.remove", "skyfs.mkdir", "skyfs.versions",
		"skyfs.compact", "skyfs.gc", "skyfs.reset",
		"skyfs.syncStart", "skyfs.syncStop", "skyfs.syncStatus":
		if s.store.backend == nil {
			return nil, fmt.Errorf("requires storage — add S3 with 'sky10 storage add s3'"), true
		}
		switch method {
		case "skyfs.list":
			result, err = s.rpcList(ctx, params)
		case "skyfs.info":
			result, err = s.rpcInfo(ctx)
		case "skyfs.put":
			result, err = s.rpcPut(ctx, params)
		case "skyfs.get":
			result, err = s.rpcGet(ctx, params)
		case "skyfs.remove":
			result, err = s.rpcRemove(ctx, params)
		case "skyfs.mkdir":
			result, err = s.rpcMkdir(ctx, params)
		case "skyfs.versions":
			result, err = s.rpcVersions(ctx, params)
		case "skyfs.compact":
			result, err = s.rpcCompact(ctx, params)
		case "skyfs.gc":
			result, err = s.rpcGC(ctx, params)
		case "skyfs.reset":
			result, err = s.rpcReset(ctx)
		case "skyfs.syncStart":
			result, err = s.rpcSyncStart(ctx, params)
		case "skyfs.syncStop":
			result, err = s.rpcSyncStop(ctx)
		case "skyfs.syncStatus":
			result, err = s.rpcSyncStatus(ctx)
		}
	case "skyfs.status":
		result, err = s.rpcStatus(ctx)
	case "skyfs.driveCreate":
		result, err = s.rpcDriveCreate(ctx, params)
	case "skyfs.driveRemove":
		result, err = s.rpcDriveRemove(ctx, params)
	case "skyfs.driveList":
		result, err = s.rpcDriveList(ctx)
	case "skyfs.driveStart":
		result, err = s.rpcDriveStart(ctx, params)
	case "skyfs.driveStop":
		result, err = s.rpcDriveStop(ctx, params)
	case "skyfs.deviceList":
		result, err = s.rpcDeviceList(ctx)
	case "skyfs.deviceRemove":
		result, err = s.rpcDeviceRemove(ctx, params)
	case "skyfs.invite":
		result, err = s.rpcInvite(ctx)
	case "skyfs.join":
		result, err = s.rpcJoin(ctx, params)
	case "skyfs.approve":
		result, err = s.rpcApprove(ctx)
	case "skyfs.syncActivity":
		result, err = s.rpcSyncActivity(ctx)
	case "skyfs.driveState":
		result, err = s.rpcDriveState(ctx, params)
	case "skyfs.debugDump", "skyfs.debugList", "skyfs.debugGet",
		"skyfs.s3List", "skyfs.s3Delete":
		if s.store.backend == nil {
			return nil, fmt.Errorf("requires S3 storage"), true
		}
		switch method {
		case "skyfs.debugDump":
			result, err = s.rpcDebugDump(ctx)
		case "skyfs.debugList":
			result, err = s.rpcDebugList(ctx)
		case "skyfs.debugGet":
			result, err = s.rpcDebugGet(ctx, params)
		case "skyfs.s3List":
			result, err = s.rpcS3List(ctx, params)
		case "skyfs.s3Delete":
			result, err = s.rpcS3Delete(ctx, params)
		}
	default:
		return nil, nil, false
	}

	return result, err, true
}

// --- Types for rpcList ---

type listParams struct {
	Prefix string `json:"prefix"`
}

type listResult struct {
	Files []fileInfo `json:"files"`
	Dirs  []dirEntry `json:"dirs,omitempty"`
}

type dirEntry struct {
	Path      string `json:"path"`
	Namespace string `json:"namespace,omitempty"`
}

type fileInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	Checksum  string `json:"checksum"`
	Namespace string `json:"namespace"`
	Chunks    int    `json:"chunks"`
}

func (s *FSHandler) rpcList(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p listParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	// Copy drives under read lock, then release before doing file I/O
	s.driveManager.mu.RLock()
	drivesCopy := make([]*Drive, 0, len(s.driveManager.drives))
	for _, d := range s.driveManager.drives {
		drivesCopy = append(drivesCopy, d)
	}
	s.driveManager.mu.RUnlock()

	var files []fileInfo
	var dirs []dirEntry
	for _, drive := range drivesCopy {
		localLog := opslog.NewLocalOpsLog(
			filepath.Join(driveDataDir(drive.ID), "ops.jsonl"),
			s.store.deviceID,
		)
		snap, err := localLog.Snapshot()
		if err != nil {
			continue
		}
		for path, fi := range snap.Files() {
			if p.Prefix != "" && (len(path) < len(p.Prefix) || path[:len(p.Prefix)] != p.Prefix) {
				continue
			}
			localPath := filepath.Join(drive.LocalPath, filepath.FromSlash(path))
			var size int64
			var mod string
			if info, err := os.Stat(localPath); err == nil {
				size = info.Size()
				mod = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}
			files = append(files, fileInfo{
				Path:      path,
				Size:      size,
				Modified:  mod,
				Checksum:  fi.Checksum,
				Namespace: fi.Namespace,
				Chunks:    len(fi.Chunks),
			})
		}
		for path, di := range snap.Dirs() {
			if p.Prefix != "" && (len(path) < len(p.Prefix) || path[:len(p.Prefix)] != p.Prefix) {
				continue
			}
			dirs = append(dirs, dirEntry{
				Path:      path,
				Namespace: di.Namespace,
			})
		}
	}

	return listResult{Files: files, Dirs: dirs}, nil
}

func (s *FSHandler) rpcInfo(_ context.Context) (interface{}, error) {
	info := &StoreInfo{
		ID: s.store.identity.Address(),
	}

	// Copy drives under read lock, then release before doing file I/O
	s.driveManager.mu.RLock()
	infoDrives := make([]*Drive, 0, len(s.driveManager.drives))
	for _, d := range s.driveManager.drives {
		infoDrives = append(infoDrives, d)
	}
	s.driveManager.mu.RUnlock()

	namespaces := make(map[string]bool)
	for _, drive := range infoDrives {
		localLog := opslog.NewLocalOpsLog(
			filepath.Join(driveDataDir(drive.ID), "ops.jsonl"),
			s.store.deviceID,
		)
		snap, err := localLog.Snapshot()
		if err != nil {
			continue
		}
		snapFiles := snap.Files()
		info.FileCount += len(snapFiles)
		for path := range snapFiles {
			localPath := filepath.Join(drive.LocalPath, filepath.FromSlash(path))
			if fi, err := os.Stat(localPath); err == nil {
				info.TotalSize += fi.Size()
			}
		}
		if drive.Namespace != "" {
			namespaces[drive.Namespace] = true
		}
	}

	for ns := range namespaces {
		info.Namespaces = append(info.Namespaces, ns)
	}
	return info, nil
}

func (s *FSHandler) rpcHealth(_ context.Context) (interface{}, error) {
	uptime := time.Since(s.startTime)

	s.activityMu.Lock()
	lastActivity := s.lastActivity
	s.activityMu.Unlock()

	// Per-drive health — use RLock, check daemons map directly
	// (don't call IsRunning which also takes the lock)
	s.driveManager.mu.RLock()
	driveCount := len(s.driveManager.drives)
	runningCount := len(s.driveManager.daemons)
	var driveIDs []string
	for id := range s.driveManager.drives {
		driveIDs = append(driveIDs, id)
	}
	s.driveManager.mu.RUnlock()

	// Outbox pending — no lock needed, just reading files
	outboxTotal := 0
	transferTotal := 0
	transferStaged := 0
	for _, id := range driveIDs {
		dir := driveDataDir(id)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			outboxTotal += len(entries)
		}
		if counts, err := summarizeTransferSessions(dir); err == nil {
			transferTotal += counts.Pending
			transferStaged += counts.Staged
		}
	}

	subscribers := s.server.SubscriberCount()
	clients := s.server.ClientCount()

	var lastActivityAgo string
	if lastActivity.IsZero() {
		lastActivityAgo = "never"
	} else {
		lastActivityAgo = time.Since(lastActivity).Truncate(time.Second).String()
	}

	result := map[string]interface{}{
		"status":            "ok",
		"version":           s.version,
		"uptime":            uptime.Truncate(time.Second).String(),
		"drives":            driveCount,
		"drives_running":    runningCount,
		"outbox_pending":    outboxTotal,
		"transfer_pending":  transferTotal,
		"transfer_staged":   transferStaged,
		"last_activity_ago": lastActivityAgo,
		"rpc_clients":       clients,
		"rpc_subscribers":   subscribers,
	}
	if addr := s.server.HTTPAddr(); addr != "" {
		result["http_addr"] = addr
	}
	return result, nil
}

func (s *FSHandler) rpcStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return statusResult{Syncing: s.syncing, SyncDir: s.syncDir}, nil
}

// MarkActivity records that sync I/O is happening right now.
func (s *FSHandler) MarkActivity() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}
