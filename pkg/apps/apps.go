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
	"regexp"
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

type currentMetadata struct {
	Current   string `json:"current"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type managedState struct {
	Current       string
	StablePath    string
	InstalledPath string
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

var versionPattern = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)

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

// BinDir returns the directory containing stable managed app entrypoints.
func BinDir() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "bin"), nil
}

// ManagedPath returns the stable sky10-managed executable path for an app.
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

// InstalledPath returns the versioned on-disk binary path for the current app version.
func InstalledPath(id ID) (string, error) {
	state, err := ensureManagedState(id)
	if err != nil {
		return "", err
	}
	return state.InstalledPath, nil
}

// StatusFor returns the active binary status for an app.
func StatusFor(id ID) (*Status, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return nil, err
	}
	state, err := ensureManagedState(id)
	if err != nil {
		return nil, err
	}
	st := &Status{
		ID:          s.ID,
		Name:        s.Name,
		ManagedPath: state.InstalledPath,
	}
	activePath, _ := resolveBinary(s, state.StablePath)
	if activePath == "" {
		return st, nil
	}
	st.Installed = true
	st.Managed = isManagedActivePath(activePath, state.StablePath, state.InstalledPath)
	st.ActivePath = activePath
	st.Version = installedVersionAtPath(s, activePath)
	return st, nil
}

// InstalledVersion returns the version of the active binary for an app,
// or "" if the app is not installed or the binary cannot execute.
func InstalledVersion(id ID) string {
	st, err := StatusFor(id)
	if err != nil {
		return ""
	}
	return st.Version
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
		Current:   normalizeVersion(current),
		Latest:    normalizeVersion(release.TagName),
	}
	info.Available = info.Current == "" || info.Latest != info.Current

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

// Install writes the provided release asset into the managed version store
// and activates the stable bin entrypoint for the app.
func Install(id ID, info *ReleaseInfo, onProgress ProgressFunc) error {
	if info == nil {
		return fmt.Errorf("missing release info")
	}
	if info.AssetURL == "" {
		return fmt.Errorf("no %s binary available for %s/%s", id, runtime.GOOS, runtime.GOARCH)
	}

	s, err := lookupSpec(id)
	if err != nil {
		return err
	}
	version := normalizeVersion(info.Latest)
	if version == "" {
		return fmt.Errorf("missing %s version", id)
	}
	dest, err := versionBinaryPath(s, version)
	if err != nil {
		return err
	}
	if err := downloadToPath(info.AssetURL, dest, string(id)+"-install-*", "downloading "+string(id), onProgress); err != nil {
		return err
	}
	stablePath, err := ManagedPath(id)
	if err != nil {
		return err
	}
	if err := writeCurrentMetadata(id, version); err != nil {
		return err
	}
	return ensureActiveBinary(dest, stablePath, true)
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

// Uninstall removes the active managed binary for an app.
func Uninstall(id ID) (*UninstallResult, error) {
	state, err := ensureManagedState(id)
	if err != nil {
		return nil, err
	}
	removed := false
	resultPath := state.InstalledPath
	if resultPath == "" {
		resultPath = state.StablePath
	}

	if state.StablePath != "" {
		if err := os.Remove(state.StablePath); err == nil {
			removed = true
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing managed entrypoint: %w", err)
		}
	}
	if state.InstalledPath != "" {
		if err := os.Remove(state.InstalledPath); err == nil {
			removed = true
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing managed binary: %w", err)
		}
		_ = os.Remove(filepath.Dir(state.InstalledPath))
	}
	if err := removeCurrentMetadata(id); err != nil {
		return nil, err
	}
	return &UninstallResult{ID: id, Path: resultPath, Removed: removed}, nil
}

func lookupSpec(id ID) (spec, error) {
	s, ok := registry[id]
	if !ok {
		return spec{}, fmt.Errorf("unknown app: %s", id)
	}
	return s, nil
}

func appsRootDir() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "apps"), nil
}

func appDir(id ID) (string, error) {
	root, err := appsRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, string(id)), nil
}

func versionsDir(id ID) (string, error) {
	dir, err := appDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "versions"), nil
}

func currentMetadataPath(id ID) (string, error) {
	dir, err := appDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "current.json"), nil
}

func versionBinaryPath(s spec, version string) (string, error) {
	dir, err := versionsDir(s.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, normalizeVersion(version), s.Executable), nil
}

func ensureManagedState(id ID) (*managedState, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return nil, err
	}
	stablePath, err := ManagedPath(id)
	if err != nil {
		return nil, err
	}
	state := &managedState{StablePath: stablePath}

	current, err := readCurrentMetadata(id)
	if err != nil {
		return nil, err
	}
	if current != "" {
		installedPath, err := versionBinaryPath(s, current)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(installedPath); err == nil {
			state.Current = current
			state.InstalledPath = installedPath
			if err := ensureActiveBinary(installedPath, stablePath, false); err != nil {
				return nil, err
			}
			return state, nil
		}
		if err := removeCurrentMetadata(id); err != nil {
			return nil, err
		}
	}

	info, err := os.Lstat(stablePath)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("stat managed binary: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(stablePath)
		if err == nil {
			if version, ok := inferVersionFromManagedTarget(id, s, target); ok {
				state.Current = version
				state.InstalledPath = target
				if err := writeCurrentMetadata(id, version); err != nil {
					return nil, err
				}
				return state, nil
			}
		}
	}

	version := installedVersionAtPath(s, stablePath)
	if version == "" {
		return state, nil
	}
	installedPath, err := versionBinaryPath(s, version)
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyBinary(stablePath, installedPath); err != nil {
		return nil, err
	}
	if err := writeCurrentMetadata(id, version); err != nil {
		return nil, err
	}
	if err := ensureActiveBinary(installedPath, stablePath, true); err != nil {
		return nil, err
	}
	state.Current = version
	state.InstalledPath = installedPath
	return state, nil
}

func inferVersionFromManagedTarget(id ID, s spec, target string) (string, bool) {
	dir, err := versionsDir(id)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return "", false
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) != 2 || parts[1] != s.Executable {
		return "", false
	}
	return normalizeVersion(parts[0]), true
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

func isManagedActivePath(activePath, stablePath, installedPath string) bool {
	if activePath == "" {
		return false
	}
	activePath = filepath.Clean(activePath)
	if stablePath != "" && activePath == filepath.Clean(stablePath) {
		return true
	}
	if installedPath != "" && activePath == filepath.Clean(installedPath) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(activePath); err == nil {
		return installedPath != "" && filepath.Clean(resolved) == filepath.Clean(installedPath)
	}
	return false
}

func installedVersionAtPath(s spec, path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, s.VersionArgs...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeVersion(string(out))
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

func ensureActiveBinary(target, stablePath string, force bool) error {
	if target == "" || stablePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(stablePath), 0755); err != nil {
		return fmt.Errorf("creating bin directory: %w", err)
	}

	if runtime.GOOS == "windows" {
		if !force {
			if _, err := os.Stat(stablePath); err == nil {
				return nil
			}
		}
		return copyExecutable(target, stablePath)
	}

	if !force {
		if resolved, err := filepath.EvalSymlinks(stablePath); err == nil && filepath.Clean(resolved) == filepath.Clean(target) {
			return nil
		}
	}

	tmpPath := fmt.Sprintf("%s.tmp-%d", stablePath, time.Now().UnixNano())
	_ = os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return fmt.Errorf("creating managed symlink: %w", err)
	}
	if err := os.Remove(stablePath); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("removing existing managed binary: %w", err)
	}
	if err := os.Rename(tmpPath, stablePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("activating managed binary: %w", err)
	}
	return nil
}

func migrateLegacyBinary(legacyPath, installedPath string) error {
	if legacyPath == "" || installedPath == "" || filepath.Clean(legacyPath) == filepath.Clean(installedPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(installedPath), 0755); err != nil {
		return fmt.Errorf("creating version directory: %w", err)
	}
	if _, err := os.Stat(installedPath); err == nil {
		if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing legacy managed binary: %w", err)
		}
		return nil
	}
	if err := os.Rename(legacyPath, installedPath); err == nil {
		return nil
	}
	if err := copyExecutable(legacyPath, installedPath); err != nil {
		return fmt.Errorf("copying legacy managed binary: %w", err)
	}
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing legacy managed binary: %w", err)
	}
	return nil
}

func copyExecutable(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("opening source binary: %w", err)
	}
	defer src.Close()

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating destination temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("copying binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing destination temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting executable permissions: %w", err)
	}
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing previous destination binary: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("activating copied binary: %w", err)
	}
	return nil
}

func readCurrentMetadata(id ID) (string, error) {
	path, err := currentMetadataPath(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading current metadata: %w", err)
	}
	var meta currentMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("parsing current metadata: %w", err)
	}
	return normalizeVersion(meta.Current), nil
}

func writeCurrentMetadata(id ID, version string) error {
	path, err := currentMetadataPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating app directory: %w", err)
	}
	meta := currentMetadata{
		Current:   normalizeVersion(version),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding current metadata: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing current metadata: %w", err)
	}
	return nil
}

func removeCurrentMetadata(id ID) error {
	path, err := currentMetadataPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing current metadata: %w", err)
	}
	return nil
}

func normalizeVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	match := versionPattern.FindString(raw)
	if match == "" {
		return raw
	}
	if strings.HasPrefix(match, "v") {
		return match
	}
	return "v" + match
}
