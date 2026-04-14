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
