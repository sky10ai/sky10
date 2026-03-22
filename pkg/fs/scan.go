package fs

import (
	"crypto/sha3"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ScanResult maps relative paths to their SHA3-256 checksums.
type ScanResult map[string]string

// ScanDirectory walks a directory and computes checksums for all files.
// Paths are relative to root. The ignore function controls what to skip.
func ScanDirectory(root string, ignore func(string) bool) (ScanResult, error) {
	result := make(ScanResult)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}
		rel = filepath.ToSlash(rel)

		if ignore != nil && ignore(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Skip conflict artifacts
		name := d.Name()
		if strings.Contains(name, ".conflict.") {
			return nil
		}

		checksum, err := fileChecksum(path)
		if err != nil {
			return fmt.Errorf("checksumming %s: %w", rel, err)
		}

		result[rel] = checksum
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}

	return result, nil
}

// ScanEmptyDirectories returns relative paths of directories that contain no
// non-ignored files. Used by seed to detect empty dirs for create_dir ops.
func ScanEmptyDirectories(root string, ignore func(string) bool) []string {
	var dirs []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if ignore != nil && ignore(rel) {
			return filepath.SkipDir
		}
		entries, _ := os.ReadDir(path)
		hasVisible := false
		for _, e := range entries {
			childRel := rel + "/" + e.Name()
			if ignore != nil && ignore(childRel) {
				continue
			}
			hasVisible = true
			break
		}
		if !hasVisible {
			dirs = append(dirs, rel)
		}
		return nil
	})
	return dirs
}

// fileChecksum computes SHA3-256 of a file by streaming (not loading into memory).
func fileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha3.New256()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
