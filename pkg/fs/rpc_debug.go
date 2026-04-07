package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

func (s *FSHandler) rpcDebugDump(ctx context.Context) (interface{}, error) {
	hostname, _ := os.Hostname()
	deviceAddr := s.store.identity.Address()
	deviceID := shortPubkeyID(deviceAddr)
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")

	dump := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"device":    hostname,
		"device_id": deviceID,
		"pubkey":    deviceAddr,
		"version":   s.version,
	}

	// Collect per-drive data — all local reads, no S3
	s.driveManager.mu.RLock()
	drivesCopy := make(map[string]*Drive, len(s.driveManager.drives))
	for id, d := range s.driveManager.drives {
		drivesCopy[id] = d
	}
	s.driveManager.mu.RUnlock()

	driveDumps := make([]map[string]interface{}, 0)
	for id, d := range drivesCopy {
		dd := map[string]interface{}{
			"id":         id,
			"name":       d.Name,
			"local_path": d.LocalPath,
			"namespace":  d.Namespace,
			"enabled":    d.Enabled,
			"running":    s.driveManager.IsRunning(id),
		}

		dir := driveDataDir(id)

		// Ops log snapshot (local file read)
		localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), s.store.deviceID)
		if snap, err := localLog.Snapshot(); err == nil {
			dd["snapshot_files"] = snap.Files()
			dd["snapshot_file_count"] = snap.Len()
			// last_remote_op removed — snapshot exchange has no cursors
		}

		// Outbox (local file read)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(dir, "outbox.jsonl"))
		if entries, err := outbox.ReadAll(); err == nil {
			dd["outbox"] = entries
			dd["outbox_count"] = len(entries)
		}

		// Local files on disk
		if files, _, err := ScanDirectory(d.LocalPath, nil); err == nil {
			localFiles := make(map[string]string)
			for path, cksum := range files {
				localFiles[path] = cksum
			}
			dd["local_files"] = localFiles
			dd["local_file_count"] = len(localFiles)
		}

		driveDumps = append(driveDumps, dd)
	}
	dump["drives"] = driveDumps

	// S3 calls with short timeouts — each one independent
	s3ctx, s3cancel := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel()

	if keys, err := s.store.backend.List(s3ctx, "ops/"); err == nil {
		dump["remote_ops_count"] = len(keys)
		if len(keys) > 20 {
			keys = keys[len(keys)-20:]
		}
		dump["remote_ops_recent"] = keys
	} else {
		dump["remote_ops_error"] = err.Error()
	}

	s3ctx2, s3cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel2()

	if devices, err := ListDevices(s3ctx2, s.store.backend); err == nil {
		dump["devices"] = devices
	} else {
		dump["devices_error"] = err.Error()
	}

	s3ctx3, s3cancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer s3cancel3()

	if keys, err := s.store.backend.List(s3ctx3, "keys/namespaces/"); err == nil {
		dump["namespace_keys"] = keys
	} else {
		dump["namespace_keys_error"] = err.Error()
	}

	// Logs — recent in-memory ring buffer used by skyfs.logs.
	logLines := s.logBuf.Lines()
	dump["logs"] = logLines
	dump["logs_raw"] = strings.Join(logLines, "\n")

	// Upload to S3 — no wall-clock timeout. The HTTP client has its own
	// idle/read timeouts for dead connections. A fixed deadline kills
	// active uploads that are streaming bytes but happen to be large.
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling debug dump: %w", err)
	}

	key := fmt.Sprintf("debug/%s/%s.json", deviceID, ts)
	r := strings.NewReader(string(data))
	if err := s.store.backend.Put(ctx, key, r, int64(len(data))); err != nil {
		return nil, fmt.Errorf("uploading debug dump: %w", err)
	}

	s.logger.Info("debug dump uploaded", "key", key, "size", len(data))

	return map[string]interface{}{
		"status": "uploaded",
		"key":    key,
		"size":   len(data),
	}, nil
}

func (s *FSHandler) rpcDebugList(ctx context.Context) (interface{}, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	keys, err := s.store.backend.List(listCtx, "debug/")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"keys": keys}, nil
}

func (s *FSHandler) rpcDebugGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rc, err := s.store.backend.Get(getCtx, p.Key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var parsed interface{}
	json.Unmarshal(data, &parsed)
	return parsed, nil
}

func (s *FSHandler) rpcS3List(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Prefix string `json:"prefix"`
	}
	if params != nil {
		json.Unmarshal(params, &p)
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	keys, err := s.store.backend.List(listCtx, p.Prefix)
	if err != nil {
		return nil, err
	}

	// Group by next path component to show "directories"
	type s3Entry struct {
		Key  string `json:"key"`
		Size int64  `json:"size"`
	}
	var files []s3Entry
	dirSet := make(map[string]bool)

	prefixLen := len(p.Prefix)
	for _, key := range keys {
		rest := key[prefixLen:]
		if idx := strings.Index(rest, "/"); idx >= 0 {
			dir := p.Prefix + rest[:idx+1]
			dirSet[dir] = true
		} else {
			meta, err := s.store.backend.Head(listCtx, key)
			size := int64(0)
			if err == nil {
				size = meta.Size
			}
			files = append(files, s3Entry{Key: key, Size: size})
		}
	}

	var dirs []string
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	return map[string]interface{}{
		"files":  files,
		"dirs":   dirs,
		"prefix": p.Prefix,
		"total":  len(keys),
	}, nil
}

func (s *FSHandler) rpcS3Delete(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Key string `json:"key"`
	}
	if params == nil {
		return nil, fmt.Errorf("missing params")
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parsing params: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}

	delCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.store.backend.Delete(delCtx, p.Key); err != nil {
		return nil, err
	}
	return map[string]interface{}{"deleted": p.Key}, nil
}
