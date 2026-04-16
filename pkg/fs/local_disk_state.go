package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const localDiskStateFile = "local-disk-state.json"

type localDiskState struct {
	Paths []string `json:"paths,omitempty"`
}

func loadLocalDiskState(driveDir string) (map[string]bool, error) {
	if driveDir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(driveDir, localDiskStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read local disk state: %w", err)
	}
	var state localDiskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode local disk state: %w", err)
	}
	paths := make(map[string]bool, len(state.Paths))
	for _, path := range state.Paths {
		if path != "" {
			paths[path] = true
		}
	}
	return paths, nil
}

func saveLocalDiskState(driveDir string, localFiles map[string]string) error {
	if driveDir == "" {
		return nil
	}
	if err := os.MkdirAll(driveDir, 0700); err != nil {
		return fmt.Errorf("create drive dir for local disk state: %w", err)
	}
	paths := make([]string, 0, len(localFiles))
	for path := range localFiles {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(localDiskState{Paths: paths}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local disk state: %w", err)
	}
	target := filepath.Join(driveDir, localDiskStateFile)
	tmpFile, err := os.CreateTemp(driveDir, filepath.Base(target)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create local disk state temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write local disk state temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close local disk state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish local disk state: %w", err)
	}
	return nil
}
