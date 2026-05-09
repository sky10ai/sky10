package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

const maxDebugScreenshotBytes = 32 << 20

type debugScreenshotParams struct {
	CapturedAt  string                 `json:"captured_at"`
	ContentType string                 `json:"content_type"`
	DataBase64  string                 `json:"data_base64"`
	Filename    string                 `json:"filename"`
	Height      int                    `json:"height"`
	PageContext map[string]interface{} `json:"page_context,omitempty"`
	SizeBytes   int64                  `json:"size_bytes"`
	Width       int                    `json:"width"`
}

type debugScreenshotResult struct {
	Status      string `json:"status"`
	Key         string `json:"key"`
	MetadataKey string `json:"metadata_key"`
	ImageKey    string `json:"image_key"`
	ContentType string `json:"content_type"`
	Height      int    `json:"height"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
}

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

func (s *FSHandler) rpcDebugScreenshot(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p debugScreenshotParams
	if len(params) == 0 || string(params) == "null" {
		return nil, fmt.Errorf("missing params")
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	imageData, err := decodeDebugScreenshotData(p.DataBase64)
	if err != nil {
		return nil, err
	}
	if len(imageData) > maxDebugScreenshotBytes {
		return nil, fmt.Errorf("screenshot exceeds max size %d bytes", maxDebugScreenshotBytes)
	}
	if p.SizeBytes > 0 && p.SizeBytes != int64(len(imageData)) {
		return nil, fmt.Errorf("size_bytes does not match decoded screenshot size")
	}
	if p.Width <= 0 || p.Height <= 0 {
		return nil, fmt.Errorf("width and height are required")
	}

	contentType := strings.TrimSpace(p.ContentType)
	if contentType == "" {
		contentType = contentTypeFromDataURL(p.DataBase64)
	}
	if contentType == "" {
		contentType = "image/png"
	}
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType != "image/png" {
		return nil, fmt.Errorf("unsupported screenshot content_type %q", contentType)
	}

	capturedAt := time.Now().UTC()
	if strings.TrimSpace(p.CapturedAt) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, p.CapturedAt)
		if err != nil {
			return nil, fmt.Errorf("captured_at must be RFC3339: %w", err)
		}
		capturedAt = parsed.UTC()
	}

	hostname, _ := os.Hostname()
	deviceAddr := s.store.identity.Address()
	deviceID := shortPubkeyID(deviceAddr)
	filename := sanitizeDebugScreenshotFilename(p.Filename, capturedAt)
	keyTS := capturedAt.Format("2006-01-02T15-04-05.000000000Z")
	baseKey := fmt.Sprintf("debug/%s/%s-ui-screenshot", deviceID, keyTS)
	imageKey := baseKey + ".png"
	metadataKey := baseKey + ".json"

	if err := s.store.backend.Put(ctx, imageKey, bytes.NewReader(imageData), int64(len(imageData))); err != nil {
		return nil, fmt.Errorf("uploading debug screenshot image: %w", err)
	}

	sum := sha256.Sum256(imageData)
	sha := fmt.Sprintf("%x", sum[:])
	metadata := map[string]interface{}{
		"type":         "ui_screenshot",
		"timestamp":    capturedAt.Format(time.RFC3339Nano),
		"device":       hostname,
		"device_id":    deviceID,
		"pubkey":       deviceAddr,
		"version":      s.version,
		"image_key":    imageKey,
		"metadata_key": metadataKey,
		"screenshot": map[string]interface{}{
			"content_type": contentType,
			"filename":     filename,
			"height":       p.Height,
			"key":          imageKey,
			"sha256":       sha,
			"size_bytes":   len(imageData),
			"width":        p.Width,
		},
	}
	if len(p.PageContext) > 0 {
		metadata["page_context"] = p.PageContext
	}

	metadataData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling debug screenshot metadata: %w", err)
	}
	if err := s.store.backend.Put(ctx, metadataKey, bytes.NewReader(metadataData), int64(len(metadataData))); err != nil {
		return nil, fmt.Errorf("uploading debug screenshot metadata: %w", err)
	}

	s.logger.Info("debug screenshot uploaded", "key", metadataKey, "image_key", imageKey, "size", len(imageData))

	return debugScreenshotResult{
		Status:      "uploaded",
		Key:         metadataKey,
		MetadataKey: metadataKey,
		ImageKey:    imageKey,
		ContentType: contentType,
		Height:      p.Height,
		SHA256:      sha,
		Size:        int64(len(imageData)),
		Width:       p.Width,
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
	if err := json.Unmarshal(data, &parsed); err == nil {
		return parsed, nil
	}
	return map[string]interface{}{
		"key":          p.Key,
		"content_type": debugContentTypeForKey(p.Key),
		"data_base64":  base64.StdEncoding.EncodeToString(data),
		"size":         len(data),
	}, nil
}

func decodeDebugScreenshotData(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("data_base64 is required")
	}
	if strings.HasPrefix(value, "data:") {
		idx := strings.Index(value, ",")
		if idx < 0 {
			return nil, fmt.Errorf("invalid data URL")
		}
		value = value[idx+1:]
	}
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	if len(value) > base64.StdEncoding.EncodedLen(maxDebugScreenshotBytes) {
		return nil, fmt.Errorf("screenshot exceeds max size %d bytes", maxDebugScreenshotBytes)
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, fmt.Errorf("decode data_base64: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("decoded screenshot is empty")
	}
	return data, nil
}

func contentTypeFromDataURL(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "data:") {
		return ""
	}
	idx := strings.Index(value, ",")
	if idx < 0 {
		return ""
	}
	header := strings.TrimPrefix(value[:idx], "data:")
	if semi := strings.Index(header, ";"); semi >= 0 {
		header = header[:semi]
	}
	return strings.TrimSpace(header)
}

func sanitizeDebugScreenshotFilename(filename string, capturedAt time.Time) string {
	name := strings.TrimSpace(filepath.Base(filename))
	if name == "" || name == "." || name == string(os.PathSeparator) {
		name = fmt.Sprintf("sky10-context-%s.png", capturedAt.UTC().Format("2006-01-02T15-04-05Z"))
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name = strings.Trim(b.String(), ".-")
	if name == "" {
		name = fmt.Sprintf("sky10-context-%s.png", capturedAt.UTC().Format("2006-01-02T15-04-05Z"))
	}
	if !strings.HasSuffix(strings.ToLower(name), ".png") {
		name += ".png"
	}
	if len(name) > 128 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		limit := 128 - len(ext)
		if limit < 1 {
			limit = 1
		}
		if len(base) > limit {
			base = base[:limit]
		}
		name = base + ext
	}
	return name
}

func debugContentTypeForKey(key string) string {
	if contentType := mime.TypeByExtension(filepath.Ext(key)); contentType != "" {
		return contentType
	}
	return "application/octet-stream"
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
