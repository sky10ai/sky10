// Package apps manages optional helper binaries installed under sky10 control.
package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

// ID identifies a managed helper app.
type ID string

const (
	// AppOWS is the Open Wallet Standard CLI.
	AppOWS ID = "ows"
)

// ProgressFunc reports download progress in bytes.
type ProgressFunc func(downloaded, total int64)

// AppInfo is public metadata for a managed app.
type AppInfo struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// Status describes the active binary for a managed app.
type Status struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Managed     bool   `json:"managed"`
	ManagedPath string `json:"managed_path,omitempty"`
	ActivePath  string `json:"active_path,omitempty"`
	Version     string `json:"version,omitempty"`
}

// ReleaseInfo describes the latest known release for an app.
type ReleaseInfo struct {
	ID        ID     `json:"id"`
	Installed bool   `json:"installed"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	AssetURL  string `json:"asset_url,omitempty"`
}

// UninstallResult describes the outcome of removing a managed binary.
type UninstallResult struct {
	ID      ID     `json:"id"`
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
}

type spec struct {
	ID          ID
	Name        string
	Repo        string
	Executable  string
	VersionArgs []string
	AssetName   func(goos, goarch string) string
}

var registry = map[ID]spec{
	AppOWS: {
		ID:          AppOWS,
		Name:        "Open Wallet Standard",
		Repo:        "open-wallet-standard/core",
		Executable:  "ows",
		VersionArgs: []string{"--version"},
		AssetName: func(goos, goarch string) string {
			arch := goarch
			switch arch {
			case "arm64":
				arch = "aarch64"
			case "amd64":
				arch = "x86_64"
			}
			return fmt.Sprintf("ows-%s-%s", goos, arch)
		},
	},
}

// ghReleaseURL builds the GitHub release endpoint; overridden in tests.
var ghReleaseURL = func(s spec) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", s.Repo)
}

// List returns the known managed apps.
func List() []AppInfo {
	items := make([]AppInfo, 0, len(registry))
	for _, s := range registry {
		items = append(items, AppInfo{ID: s.ID, Name: s.Name})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// Lookup returns metadata for a known app.
func Lookup(id string) (*AppInfo, error) {
	s, err := lookupSpec(ID(id))
	if err != nil {
		return nil, err
	}
	return &AppInfo{ID: s.ID, Name: s.Name}, nil
}

// BinDir returns the directory containing managed helper binaries.
func BinDir() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "bin"), nil
}

// ManagedPath returns the sky10-managed executable path for an app.
func ManagedPath(id ID) (string, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return "", err
	}
	dir, err := BinDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.Executable), nil
}

// StatusFor returns the active binary status for an app.
func StatusFor(id ID) (*Status, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return nil, err
	}
	managedPath, err := ManagedPath(id)
	if err != nil {
		return nil, err
	}
	st := &Status{
		ID:          s.ID,
		Name:        s.Name,
		ManagedPath: managedPath,
	}
	activePath, managed := resolveBinary(s, managedPath)
	if activePath == "" {
		return st, nil
	}
	st.Installed = true
	st.Managed = managed
	st.ActivePath = activePath
	st.Version = installedVersionAtPath(s, activePath)
	return st, nil
}

// InstalledVersion returns the version of the active binary for an app,
// or "" if the app is not installed or the binary cannot execute.
func InstalledVersion(id ID) string {
	s, err := lookupSpec(id)
	if err != nil {
		return ""
	}
	managedPath, err := ManagedPath(id)
	if err != nil {
		return ""
	}
	activePath, _ := resolveBinary(s, managedPath)
	if activePath == "" {
		return ""
	}
	return installedVersionAtPath(s, activePath)
}

// CheckRelease queries GitHub for the latest release for an app.
func CheckRelease(id ID, current string) (*ReleaseInfo, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return nil, err
	}

	resp, err := http.Get(ghReleaseURL(s))
	if err != nil {
		return nil, fmt.Errorf("fetching latest %s release: %w", s.Name, err)
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
		ID:        id,
		Installed: current != "",
		Current:   current,
		Latest:    release.TagName,
		Available: current == "" || release.TagName != current,
	}

	asset := s.AssetName(runtime.GOOS, runtime.GOARCH)
	for _, a := range release.Assets {
		if a.Name == asset {
			info.AssetURL = a.BrowserDownloadURL
			break
		}
	}

	return info, nil
}

// CheckLatest checks the latest release relative to the current active version.
func CheckLatest(id ID) (*ReleaseInfo, error) {
	return CheckRelease(id, InstalledVersion(id))
}

// Install writes the provided release asset into the managed binary path.
func Install(id ID, info *ReleaseInfo, onProgress ProgressFunc) error {
	if info == nil {
		return fmt.Errorf("missing release info")
	}
	if info.AssetURL == "" {
		return fmt.Errorf("no %s binary available for %s/%s", id, runtime.GOOS, runtime.GOARCH)
	}
	dest, err := ManagedPath(id)
	if err != nil {
		return err
	}
	return downloadToPath(info.AssetURL, dest, string(id)+"-install-*", "downloading "+string(id), onProgress)
}

// Upgrade checks the latest release and installs it if needed.
func Upgrade(id ID, onProgress ProgressFunc) (*ReleaseInfo, error) {
	info, err := CheckLatest(id)
	if err != nil {
		return nil, err
	}
	if !info.Available {
		return info, nil
	}
	if err := Install(id, info, onProgress); err != nil {
		return nil, err
	}
	return info, nil
}

// Uninstall removes the managed binary for an app.
func Uninstall(id ID) (*UninstallResult, error) {
	dest, err := ManagedPath(id)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(dest); err != nil {
		if os.IsNotExist(err) {
			return &UninstallResult{ID: id, Path: dest, Removed: false}, nil
		}
		return nil, fmt.Errorf("removing %s binary: %w", id, err)
	}
	return &UninstallResult{ID: id, Path: dest, Removed: true}, nil
}

func lookupSpec(id ID) (spec, error) {
	s, ok := registry[id]
	if !ok {
		return spec{}, fmt.Errorf("unknown app: %s", id)
	}
	return s, nil
}

func resolveBinary(s spec, managedPath string) (path string, managed bool) {
	if managedPath != "" {
		if _, err := os.Stat(managedPath); err == nil {
			return managedPath, true
		}
	}
	if bin, err := exec.LookPath(s.Executable); err == nil {
		return bin, false
	}
	return "", false
}

func installedVersionAtPath(s spec, path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, s.VersionArgs...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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

func downloadToPath(url, dest, pattern, action string, onProgress ProgressFunc) error {
	resp, err := http.Get(url)
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
		return fmt.Errorf("writing binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("installing binary: %w", err)
	}

	return nil
}
