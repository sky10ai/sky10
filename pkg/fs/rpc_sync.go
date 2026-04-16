package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

type syncStartParams struct {
	Dir         string `json:"dir"`
	PollSeconds int    `json:"poll_seconds"`
}

type statusResult struct {
	Syncing bool   `json:"syncing"`
	SyncDir string `json:"sync_dir,omitempty"`
}

type activityEntry struct {
	Direction    string `json:"direction"` // "up" or "down"
	Op           string `json:"op"`        // "put" or "delete"
	Phase        string `json:"phase,omitempty"`
	Path         string `json:"path"`
	DriveID      string `json:"drive_id"`
	DriveName    string `json:"drive_name"`
	BytesDone    int64  `json:"bytes_done,omitempty"`
	BytesTotal   int64  `json:"bytes_total,omitempty"`
	ActiveSource string `json:"active_source,omitempty"`
	Timestamp    int64  `json:"ts"`
}

type readSourceEntry struct {
	DriveID    string                    `json:"drive_id"`
	DriveName  string                    `json:"drive_name"`
	LocalHits  int                       `json:"read_local_hits"`
	PeerHits   int                       `json:"read_peer_hits"`
	S3Hits     int                       `json:"read_s3_hits"`
	LastSource string                    `json:"last_read_source,omitempty"`
	LastAt     int64                     `json:"last_read_at,omitempty"`
	PeerHealth chunkSourceHealthSnapshot `json:"peer_source_health"`
	S3Health   chunkSourceHealthSnapshot `json:"s3_source_health"`
}

type conflictEntry struct {
	DriveID   string `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Path      string `json:"path"`
	Timestamp int64  `json:"ts,omitempty"`
}

func (s *FSHandler) rpcSyncStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p syncStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Dir == "" {
		return nil, fmt.Errorf("dir is required")
	}
	if p.PollSeconds <= 0 {
		p.PollSeconds = 30
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// Stop existing sync if running
	if s.syncCancel != nil {
		s.syncCancel()
	}

	syncCtx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncDir = p.Dir
	s.syncing = true

	ignoreMatcher := NewIgnoreMatcher(p.Dir)
	cfg := SyncConfig{
		LocalRoot:  p.Dir,
		IgnoreFunc: ignoreMatcher.IgnoreFunc(),
	}
	daemonCfg := DaemonConfig{
		SyncConfig:  cfg,
		PollSeconds: p.PollSeconds,
	}

	daemon, err := NewDaemonV2_5(s.store, daemonCfg, s.logger)
	if err != nil {
		s.syncing = false
		s.syncCancel = nil
		return nil, fmt.Errorf("creating daemon: %w", err)
	}

	go func() {
		daemon.Run(syncCtx)
		s.syncMu.Lock()
		s.syncing = false
		s.syncDir = ""
		s.syncCancel = nil
		s.syncMu.Unlock()
		s.logger.Info("sync stopped", "dir", p.Dir)
	}()

	s.logger.Info("sync started", "dir", p.Dir, "poll", p.PollSeconds)
	return map[string]string{"status": "started", "dir": p.Dir}, nil
}

func (s *FSHandler) rpcSyncStop(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.syncCancel == nil {
		return map[string]string{"status": "not syncing"}, nil
	}

	s.syncCancel()
	s.syncCancel = nil
	return map[string]string{"status": "stopping"}, nil
}

func (s *FSHandler) rpcSyncStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	syncing := s.syncing
	syncDir := s.syncDir
	s.syncMu.Unlock()

	s.activityMu.Lock()
	active := time.Since(s.lastActivity) < 15*time.Second
	s.activityMu.Unlock()

	return map[string]interface{}{
		"syncing":  syncing || active,
		"sync_dir": syncDir,
	}, nil
}

func (s *FSHandler) rpcSyncActivity(_ context.Context) (interface{}, error) {
	s.driveManager.mu.RLock()
	drives := make(map[string]*Drive, len(s.driveManager.drives))
	for id, d := range s.driveManager.drives {
		drives[id] = d
	}
	s.driveManager.mu.RUnlock()

	pending := make([]activityEntry, 0)
	readSources := make([]readSourceEntry, 0)
	conflicts := make([]conflictEntry, 0)
	sourceStats := s.driveManager.readSourceSnapshots()

	for id, d := range drives {
		dir := driveDataDir(id)

		// Read outbox (pending uploads)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			for _, e := range entries {
				pending = append(pending, activityEntry{
					Direction: "up",
					Op:        string(e.Op),
					Path:      e.Path,
					DriveID:   id,
					DriveName: d.Name,
					Timestamp: e.Timestamp,
				})
			}
		}

		sessions, err := listTransferSessions(dir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			path := session.TargetPath
			if rel, err := filepath.Rel(d.LocalPath, session.TargetPath); err == nil && rel != "." && rel != "" {
				path = filepath.ToSlash(rel)
			}
			direction := "down"
			if session.Kind == "upload" {
				direction = "up"
			}
			pending = append(pending, activityEntry{
				Direction:    direction,
				Op:           session.Kind,
				Phase:        session.Phase,
				Path:         path,
				DriveID:      id,
				DriveName:    d.Name,
				BytesDone:    session.BytesDone,
				BytesTotal:   session.BytesTotal,
				ActiveSource: session.ActiveSource,
				Timestamp:    session.UpdatedAt,
			})
		}

		health := s.driveManager.sourceHealthSnapshot(id)
		stats, ok := sourceStats[id]
		if ok || health.Peer.ConsecutiveFailures > 0 || health.S3.ConsecutiveFailures > 0 || health.Peer.Degraded || health.S3.Degraded {
			readSources = append(readSources, readSourceEntry{
				DriveID:    id,
				DriveName:  d.Name,
				LocalHits:  stats.LocalHits,
				PeerHits:   stats.PeerHits,
				S3Hits:     stats.S3Hits,
				LastSource: stats.LastSource,
				LastAt:     stats.LastAt,
				PeerHealth: health.Peer,
				S3Health:   health.S3,
			})
		}

		localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), s.store.deviceID)
		if snap, err := localLog.Snapshot(); err == nil {
			for _, path := range snapshotConflictPaths(snap) {
				fi, _ := snap.Lookup(path)
				conflicts = append(conflicts, conflictEntry{
					DriveID:   id,
					DriveName: d.Name,
					Path:      path,
					Timestamp: fi.Modified.Unix(),
				})
			}
		}
	}

	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Timestamp == pending[j].Timestamp {
			if pending[i].DriveName == pending[j].DriveName {
				return pending[i].Path < pending[j].Path
			}
			return pending[i].DriveName < pending[j].DriveName
		}
		return pending[i].Timestamp > pending[j].Timestamp
	})

	sort.Slice(readSources, func(i, j int) bool {
		if readSources[i].LastAt == readSources[j].LastAt {
			return readSources[i].DriveName < readSources[j].DriveName
		}
		return readSources[i].LastAt > readSources[j].LastAt
	})

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Timestamp == conflicts[j].Timestamp {
			if conflicts[i].DriveName == conflicts[j].DriveName {
				return conflicts[i].Path < conflicts[j].Path
			}
			return conflicts[i].DriveName < conflicts[j].DriveName
		}
		return conflicts[i].Timestamp > conflicts[j].Timestamp
	})

	return map[string]interface{}{
		"pending":   pending,
		"reads":     readSources,
		"conflicts": conflicts,
	}, nil
}
