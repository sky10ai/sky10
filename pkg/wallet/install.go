package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const owsRepo = "open-wallet-standard/core"

// ghReleaseURL is the GitHub API endpoint; overridden in tests.
var ghReleaseURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", owsRepo)

// ProgressFunc reports download progress (bytes downloaded, total bytes).
type ProgressFunc func(downloaded, total int64)

// Emitter sends named events to subscribers.
type Emitter func(event string, data interface{})

// ReleaseInfo holds version information for OWS.
type ReleaseInfo struct {
	Installed bool   `json:"installed"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	AssetURL  string `json:"asset_url,omitempty"`
}

// BinDir returns the directory where managed binaries live (~/.sky10/bin/).
func BinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, ".sky10", "bin"), nil
}

// BinPath returns the full path to the managed ows binary.
func BinPath() (string, error) {
	dir, err := BinDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ows"), nil
}

// CheckRelease queries GitHub for the latest OWS release and compares
// to the currently installed version (if any).
func CheckRelease(current string) (*ReleaseInfo, error) {
	resp, err := http.Get(ghReleaseURL)
	if err != nil {
		return nil, fmt.Errorf("fetching latest OWS release: %w", err)
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

	info := &ReleaseInfo{
		Installed: current != "",
		Current:   current,
		Latest:    release.TagName,
		Available: current == "" || release.TagName != current,
	}

	asset := owsAssetName()
	for _, a := range release.Assets {
		if a.Name == asset {
			info.AssetURL = a.BrowserDownloadURL
			break
		}
	}

	return info, nil
}

// Install downloads the OWS binary to ~/.sky10/bin/ows.
// If onProgress is non-nil, it is called periodically with download progress.
func Install(info *ReleaseInfo, onProgress ProgressFunc) error {
	if info.AssetURL == "" {
		return fmt.Errorf("no OWS binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, err := BinDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("creating bin directory: %w", err)
	}

	dest := filepath.Join(binDir, "ows")

	resp, err := http.Get(info.AssetURL)
	if err != nil {
		return fmt.Errorf("downloading OWS binary: %w", err)
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

	// Write to temp file for atomic rename.
	tmp, err := os.CreateTemp(binDir, "ows-install-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
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

	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("installing binary: %w", err)
	}

	return nil
}

// InstalledVersion returns the version of the managed OWS binary,
// or "" if not installed. Runs `ows --version`.
func InstalledVersion() string {
	c := findClient()
	if c == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := c.run(ctx, "--version")
	if err != nil {
		return ""
	}
	return string(out)
}

// owsAssetName returns the expected GitHub release asset name for this platform.
func owsAssetName() string {
	return fmt.Sprintf("ows-%s-%s", runtime.GOOS, runtime.GOARCH)
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
