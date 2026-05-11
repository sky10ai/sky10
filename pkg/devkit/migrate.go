package devkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sky10/sky10/pkg/config"
)

func migrateLegacyAppsBun(s spec) (*managedState, error) {
	root, err := config.RootDir()
	if err != nil {
		return nil, fmt.Errorf("finding root directory: %w", err)
	}

	oldCurrentPath := filepath.Join(root, "apps", "bun", "current.json")
	data, err := os.ReadFile(oldCurrentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading legacy bun metadata: %w", err)
	}
	var meta currentMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing legacy bun metadata: %w", err)
	}
	version := s.Normalize(meta.Current)
	if version == "" {
		return nil, nil
	}

	oldInstalled := filepath.Join(root, "apps", "bun", "versions", version, filepath.FromSlash(bunEntrySubpath(runtime.GOOS, runtime.GOARCH)))
	if _, err := os.Stat(oldInstalled); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat legacy bun binary: %w", err)
	}

	newInstalled, err := versionBinaryPath(s, version)
	if err != nil {
		return nil, err
	}
	if err := copyExecutable(oldInstalled, newInstalled); err != nil {
		return nil, fmt.Errorf("migrating legacy bun binary: %w", err)
	}
	stablePath, err := ManagedPath(s.ID)
	if err != nil {
		return nil, err
	}
	if err := writeCurrentMetadata(s.ID, version); err != nil {
		return nil, err
	}
	if err := ensureActiveBinary(newInstalled, stablePath, true); err != nil {
		return nil, err
	}

	removeLegacyAppsBunState(root)
	managedToolLogger().Info("migrated bun from managed apps to devkit",
		"version", version,
		"installed_path", newInstalled,
	)
	return &managedState{
		Current:       version,
		StablePath:    stablePath,
		InstalledPath: newInstalled,
	}, nil
}

func removeLegacyAppsBunState(root string) {
	_ = os.Remove(filepath.Join(root, "bin", "bun"))
	_ = os.Remove(filepath.Join(root, "bin", "bun.exe"))
	_ = os.Remove(filepath.Join(root, "apps", "bun", "current.json"))
}
