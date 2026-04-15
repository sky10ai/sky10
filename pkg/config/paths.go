package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// EnvHome overrides the default ~/.sky10 root for all local sky10 state.
	EnvHome = "SKY10_HOME"
	// EnvRuntimeDir overrides the runtime directory for sockets, logs, and PID files.
	EnvRuntimeDir = "SKY10_RUNTIME_DIR"

	baseDirName    = ".sky10"
	fsDirName      = "fs"
	keysDirName    = "keys"
	kvDirName      = "kv"
	runDirName     = "run"
	ancientDirName = ".skyfs"
	drivesDirName  = "drives"
	storesDirName  = "stores"
	nsidsDirName   = "nsids"
	objectsDirName = "objects"
)

func hasCustomRoot() bool {
	return strings.TrimSpace(os.Getenv(EnvHome)) != ""
}

// RootDir returns the root directory for all local sky10 state.
func RootDir() (string, error) {
	if root := strings.TrimSpace(os.Getenv(EnvHome)); root != "" {
		return filepath.Clean(root), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, baseDirName), nil
}

// ConfigPath returns the path to the fs config file.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFile), nil
}

// KVDir returns the root directory for local KV state.
func KVDir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, kvDirName), nil
}

// KVStoresDir returns the directory containing local KV stores.
func KVStoresDir() (string, error) {
	dir, err := KVDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, storesDirName), nil
}

// KVKeysDir returns the device-scoped KV namespace key cache directory.
func KVKeysDir(deviceID string) (string, error) {
	dir, err := KVDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keysDirName, deviceID), nil
}

// KVNSIDsDir returns the local KV namespace ID cache directory.
func KVNSIDsDir() (string, error) {
	dir, err := KVDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, nsidsDirName), nil
}

// DrivesDir returns the directory containing local fs drive state.
func DrivesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, drivesDirName), nil
}

// FSKeysDir returns the device-scoped fs namespace key cache directory.
func FSKeysDir(deviceID string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keysDirName, deviceID), nil
}

// FSNSIDsDir returns the local fs namespace ID cache directory.
func FSNSIDsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, nsidsDirName), nil
}

// FSObjectsDir returns the local fs chunk/object cache directory.
func FSObjectsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, objectsDirName), nil
}

// RuntimeDir returns the directory for daemon sockets, logs, and PID files.
func RuntimeDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvRuntimeDir)); dir != "" {
		return filepath.Clean(dir)
	}
	if hasCustomRoot() {
		root, err := RootDir()
		if err == nil {
			return filepath.Join(root, runDirName)
		}
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "sky10")
	}
	return "/tmp/sky10"
}
