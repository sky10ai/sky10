// Package update checks for and applies sky10 binary updates from GitHub releases.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const repo = "sky10ai/sky10"

// checkURL is the GitHub API endpoint; overridden in tests.
var checkURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

// Emitter sends named events to subscribers.
type Emitter func(event string, data interface{})

// ProgressFunc reports download progress (bytes downloaded, total bytes).
type ProgressFunc func(downloaded, total int64)

// Info holds version information for an update check.
type Info struct {
	Current      string `json:"current"`
	Latest       string `json:"latest"`
	Available    bool   `json:"available"`
	AssetURL     string `json:"asset_url,omitempty"`
	MenuAssetURL string `json:"menu_asset_url,omitempty"`
}

// Check queries GitHub for the latest release and compares to current.
func Check(currentVersion string) (*Info, error) {
	resp, err := http.Get(checkURL)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}

	info := &Info{
		Current:   currentVersion,
		Latest:    release.TagName,
		Available: release.TagName != currentVersion,
	}

	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	menuAsset := fmt.Sprintf("sky10-menu-%s-%s", runtime.GOOS, runtime.GOARCH)
	for _, a := range release.Assets {
		if a.Name == asset {
			info.AssetURL = a.BrowserDownloadURL
		}
		if a.Name == menuAsset {
			info.MenuAssetURL = a.BrowserDownloadURL
		}
	}

	return info, nil
}

// Apply downloads the latest binary and replaces the current executable.
// If onProgress is non-nil, it is called periodically with download progress.
func Apply(info *Info, onProgress ProgressFunc) error {
	if info.AssetURL == "" {
		return fmt.Errorf("no binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	execPath, err := executablePath()
	if err != nil {
		return err
	}

	resp, err := http.Get(info.AssetURL)
	if err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			fn:    onProgress,
		}
	}

	// Write to temp file in same directory for atomic rename.
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, "sky10-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file (try running with sudo): %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("writing binary: %w", err)
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replacing binary (try running with sudo): %w", err)
	}

	return nil
}

// ApplyMenu downloads the latest sky10-menu binary to ~/.bin/sky10-menu.
// Skips silently if no menu asset is available in the release.
func ApplyMenu(info *Info) error {
	if info.MenuAssetURL == "" {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home dir: %w", err)
	}

	dest := filepath.Join(home, ".bin", "sky10-menu")

	resp, err := http.Get(info.MenuAssetURL)
	if err != nil {
		return fmt.Errorf("downloading sky10-menu: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sky10-menu download returned %d", resp.StatusCode)
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, "sky10-menu-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("writing sky10-menu: %w", err)
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("replacing sky10-menu: %w", err)
	}

	return nil
}

// PeriodicCheck checks for updates on startup and every 2 hours,
// emitting "update:available" events when a new version is found.
// Blocks until ctx is cancelled.
func PeriodicCheck(ctx context.Context, version string, emit Emitter) {
	check := func() {
		info, err := Check(version)
		if err != nil {
			slog.Debug("update check failed", "error", err)
			return
		}
		if info.Available {
			slog.Info("update available", "current", info.Current, "latest", info.Latest)
			emit("update:available", info)
		}
	}
	check()
	ticker := time.NewTicker(2 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// progressReader wraps an io.Reader and reports download progress.
// Callbacks are throttled to at most once per 100ms.
type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	fn       ProgressFunc
	lastEmit time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.read += int64(n)
	if time.Since(pr.lastEmit) > 100*time.Millisecond || err == io.EOF {
		pr.fn(pr.read, pr.total)
		pr.lastEmit = time.Now()
	}
	return n, err
}

// executablePath returns the resolved path to the current binary.
func executablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding executable path: %w", err)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolving executable path: %w", err)
	}
	return p, nil
}
