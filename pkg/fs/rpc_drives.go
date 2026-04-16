package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

type driveCreateParams struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Namespace string `json:"namespace"`
}

type driveInfo struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	LocalPath       string                    `json:"local_path"`
	Namespace       string                    `json:"namespace"`
	Enabled         bool                      `json:"enabled"`
	Running         bool                      `json:"running"`
	Outbox          int                       `json:"outbox_pending,omitempty"`
	Transfer        int                       `json:"transfer_pending,omitempty"`
	Staged          int                       `json:"transfer_staged,omitempty"`
	ReadLocal       int                       `json:"read_local_hits,omitempty"`
	ReadPeer        int                       `json:"read_peer_hits,omitempty"`
	ReadS3          int                       `json:"read_s3_hits,omitempty"`
	LastRead        string                    `json:"last_read_source,omitempty"`
	LastReadAt      int64                     `json:"last_read_at,omitempty"`
	PeerHealth      chunkSourceHealthSnapshot `json:"peer_source_health"`
	S3Health        chunkSourceHealthSnapshot `json:"s3_source_health"`
	SyncReady       bool                      `json:"sync_ready,omitempty"`
	PeerCount       int                       `json:"peer_count,omitempty"`
	SyncState       string                    `json:"sync_state,omitempty"`
	SyncMsg         string                    `json:"sync_message,omitempty"`
	LastSyncOK      int64                     `json:"last_sync_ok,omitempty"`
	LastSyncPeer    string                    `json:"last_sync_peer,omitempty"`
	LastSyncError   string                    `json:"last_sync_error,omitempty"`
	LastSyncErrorAt int64                     `json:"last_sync_error_at,omitempty"`
}

type driveIDParams struct {
	ID string `json:"id"`
}

func (s *FSHandler) rpcDriveCreate(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" || p.Path == "" {
		return nil, fmt.Errorf("name and path are required")
	}
	if p.Namespace == "" {
		p.Namespace = p.Name
	}

	drive, err := s.driveManager.CreateDrive(p.Name, p.Path, p.Namespace)
	if err != nil {
		return nil, err
	}

	// Auto-start
	s.driveManager.StartDrive(drive.ID, s.logger)

	return driveInfo{
		ID: drive.ID, Name: drive.Name, LocalPath: drive.LocalPath,
		Namespace: drive.Namespace, Enabled: drive.Enabled, Running: true,
	}, nil
}

func (s *FSHandler) rpcDriveRemove(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "ok"}, s.driveManager.RemoveDrive(p.ID)
}

func (s *FSHandler) rpcDriveList(_ context.Context) (interface{}, error) {
	drives := s.driveManager.ListDrives()
	result := make([]map[string]interface{}, len(drives))
	for i, d := range drives {
		entry := map[string]interface{}{
			"id":         d.ID,
			"name":       d.Name,
			"local_path": d.LocalPath,
			"namespace":  d.Namespace,
			"enabled":    d.Enabled,
			"running":    s.driveManager.IsRunning(d.ID),
		}
		// Add cursor and snapshot info from local ops log.
		// Snapshot() must be called first — it triggers the rebuild that
		// populates lastRemote from the file.
		dir := driveDataDir(d.ID)
		localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), s.store.deviceID)
		if snap, err := localLog.Snapshot(); err == nil {
			entry["snapshot_files"] = snap.Len()
		}
		// last_remote_op removed — snapshot exchange has no cursors
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			entry["outbox_pending"] = len(entries)
		}
		if counts, err := summarizeTransferSessions(dir); err == nil {
			entry["transfer_pending"] = counts.Pending
			entry["transfer_staged"] = counts.Staged
		}
		readStats := s.driveManager.readSourceSnapshot(d.ID)
		entry["read_local_hits"] = readStats.LocalHits
		entry["read_peer_hits"] = readStats.PeerHits
		entry["read_s3_hits"] = readStats.S3Hits
		sourceHealth := s.driveManager.sourceHealthSnapshot(d.ID)
		entry["peer_source_health"] = sourceHealth.Peer
		entry["s3_source_health"] = sourceHealth.S3
		syncHealth := s.driveManager.syncHealthSnapshot(d.ID)
		entry["sync_ready"] = syncHealth.Ready
		entry["peer_count"] = syncHealth.PeerCount
		entry["sync_state"] = syncHealth.SyncState
		if syncHealth.SyncMessage != "" {
			entry["sync_message"] = syncHealth.SyncMessage
		}
		if syncHealth.LastSyncOK > 0 {
			entry["last_sync_ok"] = syncHealth.LastSyncOK
			entry["last_sync_peer"] = syncHealth.LastSyncPeer
		}
		if syncHealth.LastSyncError != "" {
			entry["last_sync_error"] = syncHealth.LastSyncError
			entry["last_sync_error_at"] = syncHealth.LastSyncErrorAt
		}
		if readStats.LastSource != "" {
			entry["last_read_source"] = readStats.LastSource
			entry["last_read_at"] = readStats.LastAt
		}
		result[i] = entry
	}
	return map[string]interface{}{"drives": result}, nil
}

// rpcDriveState returns the full state of a drive: CRDT snapshot, disk
// files, outbox entries, and baselines. Used for debugging sync issues.
func (s *FSHandler) rpcDriveState(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	drives := s.driveManager.ListDrives()
	var drive *Drive
	for _, d := range drives {
		if d.ID == p.ID {
			drive = d
			break
		}
	}
	if drive == nil {
		return nil, fmt.Errorf("drive %q not found", p.ID)
	}

	dir := driveDataDir(drive.ID)
	result := map[string]interface{}{
		"drive_id":   drive.ID,
		"name":       drive.Name,
		"namespace":  drive.Namespace,
		"local_path": drive.LocalPath,
	}

	// CRDT snapshot
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), s.store.deviceID)
	if snap, err := localLog.Snapshot(); err == nil {
		crdtFiles := make(map[string]interface{})
		for path, fi := range snap.Files() {
			entry := map[string]interface{}{
				"checksum":  fi.Checksum,
				"size":      fi.Size,
				"device":    fi.Device,
				"chunks":    len(fi.Chunks),
				"namespace": fi.Namespace,
				"modified":  fi.Modified.Unix(),
			}
			if fi.LinkTarget != "" {
				entry["link_target"] = fi.LinkTarget
			}
			crdtFiles[path] = entry
		}
		result["crdt_files"] = crdtFiles
		result["crdt_deleted"] = snap.DeletedFiles()
		result["crdt_dirs"] = snap.Dirs()
	}

	// Disk files
	if diskFiles, _, err := ScanDirectory(drive.LocalPath, nil); err == nil {
		diskList := make(map[string]string, len(diskFiles))
		for path, cksum := range diskFiles {
			diskList[path] = cksum
		}
		result["disk_files"] = diskList
	}

	// Outbox
	outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
	if entries, err := outbox.ReadAll(); err == nil {
		outboxList := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			outboxList[i] = map[string]interface{}{
				"op": string(e.Op), "path": e.Path, "timestamp": e.Timestamp,
			}
		}
		result["outbox"] = outboxList
	}

	// Baselines
	baselineDir := filepath.Join(dir, "baselines")
	bs := NewBaselineStore(baselineDir)
	if ids, err := bs.DeviceIDs(); err == nil {
		baselines := make(map[string]interface{})
		for _, id := range ids {
			if snap, err := bs.Load(id); err == nil && snap != nil {
				files := make(map[string]string, snap.Len())
				for path, fi := range snap.Files() {
					files[path] = fi.Checksum
				}
				baselines[id] = map[string]interface{}{
					"files": files,
					"count": snap.Len(),
				}
			}
		}
		result["baselines"] = baselines
	}

	return result, nil
}

func (s *FSHandler) rpcDriveStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "started"}, s.driveManager.StartDrive(p.ID, s.logger)
}

func (s *FSHandler) rpcDriveStop(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	s.driveManager.StopDrive(p.ID)
	return map[string]string{"status": "stopped"}, nil
}
