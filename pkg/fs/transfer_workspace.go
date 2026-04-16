package fs

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	transferPhaseWriting = "writing"
	transferPhaseStaged  = "staged"
)

type transferRecoveryStats struct {
	Republished     int
	CleanedSessions int
	CleanedStaging  int
}

type transferSession struct {
	Kind         string `json:"kind"`
	Phase        string `json:"phase"`
	TempPath     string `json:"temp_path"`
	TargetPath   string `json:"target_path"`
	BytesDone    int64  `json:"bytes_done,omitempty"`
	BytesTotal   int64  `json:"bytes_total,omitempty"`
	ActiveSource string `json:"active_source,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`

	path string `json:"-"`
}

type transferSessionCounts struct {
	Pending int
	Staged  int
}

func transferDir(baseDir string) string {
	if baseDir == "" {
		return ""
	}
	return filepath.Join(baseDir, "transfer")
}

func transferStagingDir(baseDir string) string {
	dir := transferDir(baseDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "staging")
}

func transferObjectsDir(baseDir string) string {
	dir := transferDir(baseDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "objects")
}

func transferSessionsDir(baseDir string) string {
	dir := transferDir(baseDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "sessions")
}

func transferSessionsDirFromStaging(stagingDir string) string {
	if stagingDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(stagingDir), "sessions")
}

func ensureTransferWorkspace(baseDir string) error {
	for _, dir := range []string{
		transferStagingDir(baseDir),
		transferObjectsDir(baseDir),
		transferSessionsDir(baseDir),
	} {
		if dir == "" {
			return fmt.Errorf("transfer workspace base dir is required")
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating transfer workspace %s: %w", dir, err)
		}
	}
	return nil
}

func newTransferSession(sessionsDir, kind, tempPath, targetPath string) (*transferSession, error) {
	if sessionsDir == "" {
		return nil, fmt.Errorf("sessions dir is required")
	}
	if tempPath == "" || targetPath == "" {
		return nil, fmt.Errorf("temp path and target path are required")
	}
	s := &transferSession{
		Kind:       kind,
		Phase:      transferPhaseWriting,
		TempPath:   tempPath,
		TargetPath: targetPath,
		path:       filepath.Join(sessionsDir, filepath.Base(tempPath)+".json"),
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return s, nil
}

func loadTransferSession(path string) (*transferSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading transfer session: %w", err)
	}
	var s transferSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing transfer session: %w", err)
	}
	s.path = path
	if s.UpdatedAt == 0 {
		if info, err := os.Stat(path); err == nil {
			s.UpdatedAt = info.ModTime().Unix()
		}
	}
	return &s, nil
}

func (s *transferSession) save() error {
	if s == nil {
		return fmt.Errorf("transfer session is nil")
	}
	if s.path == "" {
		return fmt.Errorf("transfer session path is required")
	}
	s.UpdatedAt = time.Now().Unix()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp session file: %w", err)
	}
	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return fmt.Errorf("writing transfer session: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return fmt.Errorf("closing transfer session file: %w", err)
	}
	if err := os.Rename(tmpFile.Name(), s.path); err != nil {
		os.Remove(tmpFile.Name())
		return fmt.Errorf("publishing transfer session file: %w", err)
	}
	return nil
}

func (s *transferSession) markStaged() error {
	s.Phase = transferPhaseStaged
	if s.BytesTotal > 0 && s.BytesDone < s.BytesTotal {
		s.BytesDone = s.BytesTotal
	}
	return s.save()
}

func (s *transferSession) updateProgress(done, total int64, activeSource string) error {
	if s == nil {
		return fmt.Errorf("transfer session is nil")
	}
	changed := false
	if done >= 0 && s.BytesDone != done {
		s.BytesDone = done
		changed = true
	}
	if total >= 0 && s.BytesTotal != total {
		s.BytesTotal = total
		changed = true
	}
	if activeSource != "" && s.ActiveSource != activeSource {
		s.ActiveSource = activeSource
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

func (s *transferSession) remove() error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cleanupSessionArtifacts(s *transferSession) error {
	if s == nil {
		return nil
	}
	if s.TempPath != "" {
		if err := os.RemoveAll(s.TempPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return s.remove()
}

func cleanupStagingDir(stagingDir string) (int, error) {
	if stagingDir == "" {
		return 0, fmt.Errorf("staging dir is required")
	}
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading staging dir: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(stagingDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("removing stale staging path %s: %w", path, err)
		}
		removed++
	}
	return removed, nil
}

func listTransferSessions(baseDir string) ([]*transferSession, error) {
	sessionsDir := transferSessionsDir(baseDir)
	if sessionsDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}
	sessions := make([]*transferSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		session, err := loadTransferSession(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt == sessions[j].UpdatedAt {
			return sessions[i].TargetPath < sessions[j].TargetPath
		}
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func summarizeTransferSessions(baseDir string) (transferSessionCounts, error) {
	var counts transferSessionCounts
	sessions, err := listTransferSessions(baseDir)
	if err != nil {
		return counts, err
	}
	for _, session := range sessions {
		counts.Pending++
		if session.Phase == transferPhaseStaged {
			counts.Staged++
		}
	}
	return counts, nil
}

func recoverTransferWorkspace(baseDir string, logger *slog.Logger) (transferRecoveryStats, error) {
	var stats transferRecoveryStats
	if err := ensureTransferWorkspace(baseDir); err != nil {
		return stats, err
	}

	stagingDir := transferStagingDir(baseDir)
	sessionsDir := transferSessionsDir(baseDir)
	keep := make(map[string]bool)

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return stats, fmt.Errorf("reading sessions dir: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(sessionsDir, entry.Name())
		if entry.IsDir() {
			if err := os.RemoveAll(path); err == nil {
				stats.CleanedSessions++
			}
			continue
		}

		session, err := loadTransferSession(path)
		if err != nil {
			if logger != nil {
				logger.Warn("invalid transfer session removed", "path", path, "error", err)
			}
			if err := os.Remove(path); err == nil || os.IsNotExist(err) {
				stats.CleanedSessions++
			}
			continue
		}

		switch session.Phase {
		case transferPhaseStaged:
			if session.TempPath == "" || session.TargetPath == "" {
				if logger != nil {
					logger.Warn("staged transfer session missing paths; cleaning", "path", session.path)
				}
				if err := cleanupSessionArtifacts(session); err == nil {
					stats.CleanedSessions++
				}
				continue
			}
			if _, err := os.Stat(session.TempPath); err != nil {
				if err := session.remove(); err == nil {
					stats.CleanedSessions++
				}
				continue
			}
			if err := publishStagedFile(session.TempPath, session.TargetPath); err != nil {
				if logger != nil {
					logger.Warn("transfer session republish failed", "temp_path", session.TempPath, "target_path", session.TargetPath, "error", err)
				}
				keep[session.TempPath] = true
				continue
			}
			if err := session.remove(); err == nil {
				stats.CleanedSessions++
			}
			stats.Republished++
		default:
			if err := cleanupSessionArtifacts(session); err == nil {
				stats.CleanedSessions++
			}
		}
	}

	stagingEntries, err := os.ReadDir(stagingDir)
	if err != nil {
		return stats, fmt.Errorf("reading staging dir: %w", err)
	}
	for _, entry := range stagingEntries {
		path := filepath.Join(stagingDir, entry.Name())
		if keep[path] {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return stats, fmt.Errorf("removing stale staging path %s: %w", path, err)
		}
		stats.CleanedStaging++
	}

	return stats, nil
}

func createStagingTempFile(stagingDir, pattern string) (*os.File, string, error) {
	if stagingDir == "" {
		return nil, "", fmt.Errorf("staging dir is required")
	}
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		return nil, "", fmt.Errorf("creating staging dir: %w", err)
	}
	f, err := os.CreateTemp(stagingDir, pattern)
	if err != nil {
		return nil, "", fmt.Errorf("creating temp file: %w", err)
	}
	return f, f.Name(), nil
}

func publishStagedFile(tmpPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return fmt.Errorf("creating target dir: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err == nil {
		return nil
	}
	if err := copyFile(tmpPath, dstPath); err != nil {
		return fmt.Errorf("copying staged file: %w", err)
	}
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing staged file: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
