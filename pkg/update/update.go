// Package update checks for and applies sky10 binary updates from GitHub releases.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/releases"
)

const repo = "sky10ai/sky10"
const updateDownloadUserAgent = "sky10-updater"

// checkURL is the GitHub API endpoint; overridden in tests.
var checkURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

var (
	updateUserHomeDir        = os.UserHomeDir
	updateExecutablePathFunc = executablePath
)

// Emitter sends named events to subscribers.
type Emitter func(event string, data interface{})

// ProgressFunc reports download progress (bytes downloaded, total bytes).
type ProgressFunc func(downloaded, total int64)

// Info holds version information for an update check.
type Info struct {
	Current          string `json:"current"`
	Latest           string `json:"latest"`
	Available        bool   `json:"available"`
	CLIAvailable     bool   `json:"cli_available"`
	MenuAvailable    bool   `json:"menu_available"`
	AssetURL         string `json:"asset_url,omitempty"`
	MenuAssetURL     string `json:"menu_asset_url,omitempty"`
	MenuChecksumsURL string `json:"menu_checksums_url,omitempty"`
}

// Check queries GitHub for the latest release and compares to current.
func Check(currentVersion string) (*Info, error) {
	currentVersion = strings.TrimSpace(currentVersion)
	if isDevBuildVersion(currentVersion) {
		return &Info{Current: currentVersion}, nil
	}

	release, err := releases.NewGitHubClient("sky10/"+currentVersion).Latest(context.Background(), checkURL)
	if err != nil {
		return nil, err
	}

	info := &Info{
		Current:      currentVersion,
		Latest:       release.TagName,
		CLIAvailable: release.TagName != currentVersion,
	}

	asset := fmt.Sprintf("sky10-%s-%s", runtime.GOOS, runtime.GOARCH)
	menuAsset := fmt.Sprintf("sky10-menu-%s-%s", runtime.GOOS, runtime.GOARCH)
	for _, a := range release.Assets {
		switch a.Name {
		case asset:
			info.AssetURL = a.BrowserDownloadURL
		case menuAsset:
			info.MenuAssetURL = a.BrowserDownloadURL
		case "checksums-menu.txt":
			info.MenuChecksumsURL = a.BrowserDownloadURL
		}
	}

	info.MenuAvailable = menuNeedsUpdate(info)
	info.Available = info.CLIAvailable || info.MenuAvailable

	return info, nil
}

// Apply downloads the latest binary and replaces the current executable.
// If onProgress is non-nil, it is called periodically with download progress.
func Apply(info *Info, onProgress ProgressFunc) error {
	if !info.CLIAvailable {
		return nil
	}
	if info.AssetURL == "" {
		return fmt.Errorf("no binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	execPath, err := updateExecutablePathFunc()
	if err != nil {
		return err
	}
	return downloadToPath(info.AssetURL, execPath, "sky10-update-*", "downloading binary", onProgress)
}

// ApplyMenu downloads the latest sky10-menu binary to ~/.bin/sky10-menu.
// Returns true if the binary was updated. Skips the download entirely
// when checksums.txt shows the local binary is already current.
func ApplyMenu(info *Info) (changed bool, err error) {
	if info.MenuAssetURL == "" {
		return false, nil
	}

	home, err := updateUserHomeDir()
	if err != nil {
		return false, fmt.Errorf("finding home dir: %w", err)
	}

	dest := filepath.Join(home, ".bin", "sky10-menu")
	if !info.MenuAvailable && fileExists(dest) {
		return false, nil
	}

	if err := downloadToPath(info.MenuAssetURL, dest, "sky10-menu-update-*", "downloading sky10-menu", nil); err != nil {
		return false, err
	}

	return true, nil
}

func menuNeedsUpdate(info *Info) bool {
	if info.MenuAssetURL == "" {
		return false
	}

	home, err := updateUserHomeDir()
	if err != nil {
		return info.CLIAvailable
	}

	dest := filepath.Join(home, ".bin", "sky10-menu")
	localHash := hashFile(dest)
	if localHash == "" {
		return true
	}

	if info.MenuChecksumsURL == "" {
		return info.CLIAvailable
	}

	menuAsset := fmt.Sprintf("sky10-menu-%s-%s", runtime.GOOS, runtime.GOARCH)
	remoteHash, err := fetchChecksum(info.MenuChecksumsURL, menuAsset)
	if err != nil {
		return info.CLIAvailable
	}
	return localHash != remoteHash
}

// fetchChecksum fetches checksums.txt and returns the SHA-256 for the named asset.
func fetchChecksum(url, asset string) (string, error) {
	req, err := updateRequest(http.MethodGet, url)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums returned %d", resp.StatusCode)
	}

	// Format: "<hash>  <filename>" (two spaces, matching shasum output).
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 2 && parts[1] == asset {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("asset %s not found in checksums", asset)
}

// hashFile returns the hex SHA-256 of a file, or "" on any error.
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PeriodicCheck checks for updates on startup and every 2 hours,
// emitting "update:available" events when a new version is found.
// Blocks until ctx is cancelled.
func PeriodicCheck(ctx context.Context, version string, emit Emitter) {
	if isDevBuildVersion(version) {
		return
	}

	check := func() {
		info, err := Check(version)
		if err != nil {
			logger().Debug("update check failed", "error", err)
			return
		}
		if info.Available {
			logger().Info("update available", "current", info.Current, "latest", info.Latest)
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

func downloadToPath(url, dest, pattern, action string, onProgress ProgressFunc) error {
	req, err := updateRequest(http.MethodGet, url)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: returned %d", action, resp.StatusCode)
	}

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			fn:    onProgress,
		}
	}

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

	if _, err := io.Copy(tmp, src); err != nil {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func updateRequest(method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", updateDownloadUserAgent)
	return req, nil
}

func isDevBuildVersion(version string) bool {
	version = strings.TrimSpace(version)
	return version == "" || strings.EqualFold(version, "dev")
}
