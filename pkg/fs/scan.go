package fs

import (
	"crypto/sha3"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScanResult maps relative paths to their SHA3-256 checksums.
type ScanResult map[string]string

// ScanDirectory walks a directory and computes checksums for all files.
// Paths are relative to root. The ignore function controls what to skip.
//
// Symlinks are detected via os.Lstat and returned separately — their
// "checksum" (SHA3-256 of the target path) is also added to the main
// ScanResult so the reconciler's checksum comparison works uniformly.
// The returned symlinks map is path → link target.
func ScanDirectory(root string, ignore func(string) bool) (ScanResult, map[string]string, error) {
	result := make(ScanResult)
	symlinks := make(map[string]string)

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

		// Detect symlinks before computing checksum.
		// filepath.WalkDir uses Lstat internally, so d.Type() has ModeSymlink.
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("reading symlink %s: %w", rel, err)
			}
			symlinks[rel] = target
			result[rel] = symlinkChecksum(target)
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
		return nil, nil, fmt.Errorf("scanning %s: %w", root, err)
	}

	return result, symlinks, nil
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

func stableFileChecksum(path string, stableWindow time.Duration) (string, bool, error) {
	infoBefore, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if stableWindow > 0 && !fileSettled(infoBefore, stableWindow, time.Now()) {
		return "", false, nil
	}

	checksum, err := fileChecksum(path)
	if err != nil {
		return "", false, err
	}

	infoAfter, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if infoBefore.Size() != infoAfter.Size() || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		return "", false, nil
	}
	if stableWindow > 0 && !fileSettled(infoAfter, stableWindow, time.Now()) {
		return "", false, nil
	}

	return checksum, true, nil
}

func fileSettled(info os.FileInfo, stableWindow time.Duration, now time.Time) bool {
	if info == nil || stableWindow <= 0 {
		return true
	}
	return now.Sub(info.ModTime()) >= stableWindow
}

// symlinkChecksum computes SHA3-256 of a symlink's target path.
// Used for change detection — if the target changes, the checksum changes.
func symlinkChecksum(target string) string {
	h := sha3.New256()
	h.Write([]byte(target))
	return hex.EncodeToString(h.Sum(nil))
}
