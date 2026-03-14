package skyfs

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
// Paths are relative to root. Skips dotfiles and directories starting with ".".
func ScanDirectory(root string, ignore func(string) bool) (ScanResult, error) {
	result := make(ScanResult)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip dotfiles/dirs
		name := d.Name()
		if strings.HasPrefix(name, ".") && name != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		// Normalize to forward slashes for S3 compatibility
		rel = filepath.ToSlash(rel)

		if ignore != nil && ignore(rel) {
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
