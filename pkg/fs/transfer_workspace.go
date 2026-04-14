package fs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
