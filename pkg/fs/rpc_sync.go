package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
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
	Direction string `json:"direction"` // "up" or "down"
	Op        string `json:"op"`        // "put" or "delete"
	Path      string `json:"path"`
	DriveID   string `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Timestamp int64  `json:"ts"`
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

	}

	return map[string]interface{}{"pending": pending}, nil
}
