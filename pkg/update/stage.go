package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var ErrNoStagedUpdate = errors.New("no staged update")

type StagedRelease struct {
	Current      string    `json:"current"`
	Latest       string    `json:"latest"`
	CLIStaged    bool      `json:"cli_staged"`
	MenuStaged   bool      `json:"menu_staged"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

type StagedStatus struct {
	Current    string `json:"current"`
	Ready      bool   `json:"ready"`
	Latest     string `json:"latest,omitempty"`
	CLIStaged  bool   `json:"cli_staged"`
	MenuStaged bool   `json:"menu_staged"`
}

func Stage(info *Info, onProgress ProgressFunc) (*StagedRelease, error) {
	if !info.Available {
		return nil, nil
	}

	existing, err := readStagedRelease()
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Latest == info.Latest &&
		existing.CLIStaged == info.CLIAvailable &&
		existing.MenuStaged == info.MenuAvailable &&
		stagedFilesExist(existing) {
		return existing, nil
	}

	if err := clearStage(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(updateDir(), 0755); err != nil {
		return nil, fmt.Errorf("creating update dir: %w", err)
	}

	staged := &StagedRelease{
		Current:      info.Current,
		Latest:       info.Latest,
		CLIStaged:    info.CLIAvailable,
		MenuStaged:   info.MenuAvailable,
		DownloadedAt: time.Now().UTC(),
	}

	if staged.CLIStaged {
		if info.AssetURL == "" {
			return nil, fmt.Errorf("no binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		if err := downloadToPath(info.AssetURL, stagedCLIPath(), "sky10-stage-*", "downloading binary", onProgress); err != nil {
			return nil, err
		}
	}
	if staged.MenuStaged {
		if info.MenuAssetURL == "" {
			return nil, fmt.Errorf("no sky10-menu binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		if err := downloadToPath(info.MenuAssetURL, stagedMenuPath(), "sky10-menu-stage-*", "downloading sky10-menu", nil); err != nil {
			return nil, err
		}
	}

	if err := writeStagedRelease(staged); err != nil {
		return nil, err
	}
	return staged, nil
}

func Status(currentVersion string) (*StagedStatus, error) {
	staged, err := readStagedRelease()
	if err != nil {
		return nil, err
	}

	status := &StagedStatus{Current: currentVersion}
	if staged == nil {
		return status, nil
	}
	if !stagedFilesExist(staged) || stagedReleaseIsStale(currentVersion, staged.Latest) {
		if err := clearStage(); err != nil {
			return nil, err
		}
		return status, nil
	}

	status.Ready = staged.CLIStaged || staged.MenuStaged
	status.Latest = staged.Latest
	status.CLIStaged = staged.CLIStaged
	status.MenuStaged = staged.MenuStaged
	return status, nil
}

// stagedReleaseIsStale reports whether a staged release tag is no newer than
// the running version. We only treat the stage as stale when both versions
// parse as clean semver release tags; ambiguous inputs (dev builds, missing
// tags) leave the stage untouched.
func stagedReleaseIsStale(current, staged string) bool {
	cur, ok := parseSemverTag(current)
	if !ok {
		return false
	}
	stg, ok := parseSemverTag(staged)
	if !ok {
		return false
	}
	return compareSemver(cur, stg) >= 0
}

func parseSemverTag(v string) ([3]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func InstallStaged() (*StagedRelease, error) {
	staged, err := readStagedRelease()
	if err != nil {
		return nil, err
	}
	if staged == nil || !(staged.CLIStaged || staged.MenuStaged) {
		return nil, ErrNoStagedUpdate
	}
	if !stagedFilesExist(staged) {
		if err := clearStage(); err != nil {
			return nil, err
		}
		return nil, ErrNoStagedUpdate
	}

	if staged.CLIStaged {
		execPath, err := updateExecutablePathFunc()
		if err != nil {
			return nil, err
		}
		if err := installFromPath(stagedCLIPath(), execPath, "sky10-install-*"); err != nil {
			return nil, err
		}
	}
	if staged.MenuStaged {
		home, err := updateUserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("finding home dir: %w", err)
		}
		menuPath := menuInstallPath(home, runtime.GOOS)
		if err := installFromPath(stagedMenuPath(), menuPath, "sky10-menu-install-*"); err != nil {
			return nil, err
		}
	}

	if err := clearStage(); err != nil {
		return nil, err
	}
	return staged, nil
}

func updateDir() string {
	home, err := updateUserHomeDir()
	if err != nil {
		return filepath.Join(".sky10", "update")
	}
	return filepath.Join(home, ".sky10", "update")
}

func stagedMetadataPath() string {
	return filepath.Join(updateDir(), "staged.json")
}

func stagedCLIPath() string {
	return filepath.Join(updateDir(), cliBinaryName(runtime.GOOS))
}

func stagedMenuPath() string {
	return filepath.Join(updateDir(), menuBinaryName(runtime.GOOS))
}

func readStagedRelease() (*StagedRelease, error) {
	data, err := os.ReadFile(stagedMetadataPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading staged update metadata: %w", err)
	}

	var staged StagedRelease
	if err := json.Unmarshal(data, &staged); err != nil {
		return nil, fmt.Errorf("parsing staged update metadata: %w", err)
	}
	return &staged, nil
}

func writeStagedRelease(staged *StagedRelease) error {
	data, err := json.MarshalIndent(staged, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding staged update metadata: %w", err)
	}
	if err := os.WriteFile(stagedMetadataPath(), data, 0644); err != nil {
		return fmt.Errorf("writing staged update metadata: %w", err)
	}
	return nil
}

func clearStage() error {
	if err := os.RemoveAll(updateDir()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing staged updates: %w", err)
	}
	return nil
}

func stagedFilesExist(staged *StagedRelease) bool {
	if staged.CLIStaged && !fileExists(stagedCLIPath()) {
		return false
	}
	if staged.MenuStaged && !fileExists(stagedMenuPath()) {
		return false
	}
	return true
}

func installFromPath(src, dest, pattern string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening staged file: %w", err)
	}
	defer in.Close()

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("writing file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("replacing file: %w", err)
	}
	return nil
}
