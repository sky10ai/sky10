package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/config"
)

type putParams struct {
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

type putResult struct {
	Size   int64 `json:"size"`
	Chunks int   `json:"chunks"`
}

type getParams struct {
	Path    string `json:"path"`
	OutPath string `json:"out_path"`
}

type getResult struct {
	Size int64 `json:"size"`
}

type removeParams struct {
	Drive string `json:"drive"`
	Path  string `json:"path"`
}

type mkdirParams struct {
	Drive string `json:"drive"`
	Path  string `json:"path"`
}

type versionsParams struct {
	Path string `json:"path"`
}

type compactParams struct {
	Keep int `json:"keep"`
}

type gcParams struct {
	DryRun bool `json:"dry_run"`
}

func (s *FSHandler) rpcPut(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p putParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	logicalPath, err := NormalizeLogicalPath(p.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	f, err := os.Open(p.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", p.LocalPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", p.LocalPath, err)
	}

	if err := s.store.Put(ctx, logicalPath, f); err != nil {
		return nil, err
	}

	s.server.Emit("file.changed", map[string]string{"path": logicalPath, "type": "put"})
	return putResult{Size: info.Size()}, nil
}

func (s *FSHandler) rpcGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	f, err := os.Create(p.OutPath)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", p.OutPath, err)
	}

	// TODO(snapshot-exchange): rpcDownload needs GetChunks + local CRDT lookup
	if err := fmt.Errorf("download RPC not yet implemented in snapshot-exchange architecture"); err != nil {
		f.Close()
		os.Remove(p.OutPath)
		return nil, err
	}

	stat, _ := f.Stat()
	f.Close()

	return getResult{Size: stat.Size()}, nil
}

func (s *FSHandler) rpcRemove(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p removeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Drive == "" || p.Path == "" {
		return nil, fmt.Errorf("drive and path are required")
	}

	drive := s.findDrive(p.Drive)
	if drive == nil {
		return nil, fmt.Errorf("drive %q not found", p.Drive)
	}

	logicalPath, err := NormalizeLogicalPath(p.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	target, err := LogicalPathToLocal(drive.LocalPath, logicalPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if err := os.RemoveAll(target); err != nil {
		return nil, fmt.Errorf("removing %s: %w", logicalPath, err)
	}

	s.server.Emit("file.changed", map[string]string{"drive": p.Drive, "path": logicalPath, "type": "delete"})
	return map[string]string{"status": "ok"}, nil
}

func (s *FSHandler) rpcMkdir(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p mkdirParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Drive == "" || p.Path == "" {
		return nil, fmt.Errorf("drive and path are required")
	}

	drive := s.findDrive(p.Drive)
	if drive == nil {
		return nil, fmt.Errorf("drive %q not found", p.Drive)
	}

	logicalPath, err := NormalizeLogicalPath(p.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	target, err := LogicalPathToLocal(drive.LocalPath, logicalPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	if err := os.MkdirAll(target, 0755); err != nil {
		return nil, fmt.Errorf("creating directory %s: %w", logicalPath, err)
	}

	s.server.Emit("file.changed", map[string]string{"drive": p.Drive, "path": logicalPath, "type": "mkdir"})
	return map[string]string{"status": "ok"}, nil
}

// findDrive looks up a drive by ID or name.
func (s *FSHandler) findDrive(nameOrID string) *Drive {
	// Try by ID first (e.g. "drive_Test").
	if d := s.driveManager.GetDrive(nameOrID); d != nil {
		return d
	}
	// Fall back to "drive_<name>" convention.
	if d := s.driveManager.GetDrive("drive_" + nameOrID); d != nil {
		return d
	}
	// Search by name.
	for _, d := range s.driveManager.ListDrives() {
		if d.Name == nameOrID {
			return d
		}
	}
	return nil
}

func (s *FSHandler) rpcVersions(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p versionsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	logicalPath, err := NormalizeLogicalPath(p.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	versions, err := ListVersions(ctx, s.store, logicalPath)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{"versions": versions}, nil
}

func (s *FSHandler) rpcCompact(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p compactParams
	p.Keep = 3
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	s.server.Emit("compact.start", map[string]any{"phase": "reading ops"})

	// Compaction is no longer needed in the snapshot-exchange architecture.
	return map[string]string{"status": "no-op", "reason": "compaction removed"}, nil
}

func (s *FSHandler) rpcReset(ctx context.Context) (interface{}, error) {
	deleted := 0

	// Delete all S3 ops
	if keys, err := s.store.backend.List(ctx, "ops/"); err == nil {
		for _, key := range keys {
			s.store.backend.Delete(ctx, key)
			deleted++
		}
	}

	// Delete all S3 snapshots
	if keys, err := s.store.backend.List(ctx, "manifests/snapshot-"); err == nil {
		for _, key := range keys {
			s.store.backend.Delete(ctx, key)
			deleted++
		}
	}

	// Delete local drive state files
	drivesDir, _ := config.DrivesDir()
	stateFiles := []string{"ops.jsonl", "outbox.jsonl", "state.json", "inbox.jsonl", "manifest.json"}
	localDeleted := 0
	if entries, err := os.ReadDir(drivesDir); err == nil {
		for _, d := range entries {
			if !d.IsDir() {
				continue
			}
			driveDir := filepath.Join(drivesDir, d.Name())
			for _, f := range stateFiles {
				if os.Remove(filepath.Join(driveDir, f)) == nil {
					localDeleted++
				}
			}
			// Clear baselines — stale baselines cause the poller to
			// skip changes that match the old baseline.
			baselinesDir := filepath.Join(driveDir, "baselines")
			if bEntries, err := os.ReadDir(baselinesDir); err == nil {
				for _, b := range bEntries {
					if os.Remove(filepath.Join(baselinesDir, b.Name())) == nil {
						localDeleted++
					}
				}
			}
		}
	}

	s.logger.Info("reset complete", "s3_deleted", deleted, "local_deleted", localDeleted)
	return map[string]interface{}{
		"s3_deleted":    deleted,
		"local_deleted": localDeleted,
	}, nil
}

func (s *FSHandler) rpcGC(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p gcParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	// Get namespace encryption key for reading snapshots
	encKey, err := s.store.getOrCreateNamespaceKey(ctx, "default")
	if err != nil {
		return nil, fmt.Errorf("getting encryption key: %w", err)
	}
	// Collect namespace IDs from drives
	var nsIDs []string
	if s.store.nsID != "" {
		nsIDs = []string{s.store.nsID}
	}
	result, err := GC(ctx, s.store.backend, encKey, GCConfig{
		NSIDs:         nsIDs,
		RetentionDays: 30,
		DryRun:        p.DryRun,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
