// Package devkit manages sky10-controlled build and repair toolchains.
package devkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/releases"
)

// ID identifies a managed devkit tool.
type ID string

const (
	// ToolBun is the Bun JavaScript runtime used by repo automation and repair.
	ToolBun ID = "bun"
	// ToolGo is the Go SDK/toolchain used by repo automation and repair.
	ToolGo ID = "go"
)

// ProgressFunc reports download progress in bytes.
type ProgressFunc func(downloaded, total int64)

// ToolInfo is public metadata for a managed devkit tool.
type ToolInfo struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// Status describes the sky10-managed devkit tool state.
type Status struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Managed     bool   `json:"managed"`
	ManagedPath string `json:"managed_path,omitempty"`
	ActivePath  string `json:"active_path,omitempty"`
	Version     string `json:"version,omitempty"`
}

// ReleaseInfo describes the selected install candidate for a devkit tool.
type ReleaseInfo struct {
	ID        ID     `json:"id"`
	Installed bool   `json:"installed"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	AssetName string `json:"asset_name,omitempty"`
	AssetURL  string `json:"asset_url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// UninstallResult describes the outcome of removing a managed devkit tool.
type UninstallResult struct {
	ID      ID     `json:"id"`
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
}

type spec struct {
	ID              ID
	Name            string
	Executable      string
	ExecutableFor   func(goos, goarch string) string
	EntrySubpath    string
	EntrySubpathFor func(goos, goarch string) string
	VersionArgs     []string
	Normalize       func(string) string
	ResolveRelease  func(context.Context, spec, string, string) (*ReleaseInfo, error)
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
	ToolBun: {
		ID:              ToolBun,
		Name:            "Bun",
		Executable:      "bun",
		ExecutableFor:   bunExecutableFor,
		EntrySubpathFor: bunEntrySubpath,
		VersionArgs:     []string{"--version"},
		Normalize:       normalizeVVersion,
		ResolveRelease:  resolveBunRelease,
	},
	ToolGo: {
		ID:              ToolGo,
		Name:            "Go SDK",
		Executable:      "go",
		ExecutableFor:   goExecutableFor,
		EntrySubpathFor: goEntrySubpath,
		VersionArgs:     []string{"version"},
		Normalize:       normalizeGoVersion,
		ResolveRelease:  resolveGoRelease,
	},
}

var (
	ghReleaseURL = func(s spec) string {
		switch s.ID {
		case ToolBun:
			return "https://api.github.com/repos/oven-sh/bun/releases/latest"
		default:
			return ""
		}
	}
	ghReleaseClient = releases.NewGitHubClient("sky10-devkit")

	goManifestURL     = "https://go.dev/dl/?mode=json&include=all"
	goDownloadBaseURL = "https://go.dev/dl/"
	httpClient        = &http.Client{Timeout: 30 * time.Second}
	vVersionPattern   = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)
	goVersionPattern  = regexp.MustCompile(`go\d+(?:\.\d+){1,2}(?:[a-z]+[0-9.]*)?|\d+(?:\.\d+){1,2}(?:[a-z]+[0-9.]*)?`)
	managedToolLogger = func() *slog.Logger {
		return logging.WithComponent(slog.Default(), "devkit")
	}
)

// List returns known managed devkit tools.
func List() []ToolInfo {
	items := make([]ToolInfo, 0, len(registry))
	for _, s := range registry {
		items = append(items, ToolInfo{ID: s.ID, Name: s.Name})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

// Lookup returns metadata for a known devkit tool.
func Lookup(id string) (*ToolInfo, error) {
	s, err := lookupSpec(ID(id))
	if err != nil {
		return nil, err
	}
	return &ToolInfo{ID: s.ID, Name: s.Name}, nil
}

// RootDir returns the root directory for managed devkit state.
func RootDir() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "devkit"), nil
}

// BinDir returns the directory containing stable devkit entrypoints.
func BinDir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bin"), nil
}

// ManagedPath returns the stable sky10-managed executable path for a devkit tool.
func ManagedPath(id ID) (string, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return "", err
	}
	dir, err := BinDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, executableFor(s, runtime.GOOS, runtime.GOARCH)), nil
}

// InstalledPath returns the versioned on-disk executable path for the current tool version.
func InstalledPath(id ID) (string, error) {
	state, err := ensureManagedState(id)
	if err != nil {
		return "", err
	}
	return state.InstalledPath, nil
}

// StatusFor returns the sky10-managed state for a devkit tool.
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
	if state.InstalledPath == "" {
		return st, nil
	}
	if _, err := os.Stat(state.StablePath); err != nil {
		return st, nil
	}
	st.Installed = true
	st.Managed = isManagedActivePath(state.StablePath, state.InstalledPath)
	st.ActivePath = state.StablePath
	st.Version = installedVersionAtPath(s, state.StablePath)
	if st.Version == "" {
		st.Version = state.Current
	}
	return st, nil
}

// InstalledVersion returns the current managed version for a devkit tool.
func InstalledVersion(id ID) string {
	st, err := StatusFor(id)
	if err != nil {
		return ""
	}
	return st.Version
}

// CheckRelease resolves an install candidate. requestedVersion can be empty for latest.
func CheckRelease(id ID, current, requestedVersion string) (*ReleaseInfo, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return nil, err
	}
	if s.ResolveRelease == nil {
		return nil, fmt.Errorf("%s release resolver is not configured", id)
	}
	return s.ResolveRelease(context.Background(), s, s.Normalize(current), strings.TrimSpace(requestedVersion))
}

// CheckLatest resolves the latest install candidate relative to current managed state.
func CheckLatest(id ID) (*ReleaseInfo, error) {
	return CheckRelease(id, InstalledVersion(id), "")
}

// Install writes the selected release asset into the managed devkit store.
func Install(id ID, info *ReleaseInfo, onProgress ProgressFunc) error {
	if info == nil {
		return fmt.Errorf("missing release info")
	}
	if strings.TrimSpace(info.AssetURL) == "" {
		return fmt.Errorf("no %s archive available for %s/%s", id, runtime.GOOS, runtime.GOARCH)
	}
	s, err := lookupSpec(id)
	if err != nil {
		return err
	}
	version := s.Normalize(info.Latest)
	if version == "" {
		return fmt.Errorf("missing %s version", id)
	}

	versionRoot, err := versionRootDir(s.ID, version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(versionRoot); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("resetting %s version directory: %w", id, err)
	}
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		return fmt.Errorf("creating %s version directory: %w", id, err)
	}
	if err := downloadAndExtractArchive(info, versionRoot, string(id)+"-archive-*", "downloading "+string(id), onProgress); err != nil {
		return err
	}

	dest, err := versionBinaryPath(s, version)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dest); err != nil {
		return fmt.Errorf("%s archive did not produce %q: %w", id, dest, err)
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

// Upgrade installs the requested version, or the latest available version if requestedVersion is empty.
func Upgrade(id ID, requestedVersion string, onProgress ProgressFunc) (*ReleaseInfo, error) {
	status, err := StatusFor(id)
	if err != nil {
		return nil, err
	}
	info, err := CheckRelease(id, status.Version, requestedVersion)
	if err != nil {
		return nil, err
	}
	shouldInstall := info.Available || !status.Managed
	if !shouldInstall {
		return info, nil
	}
	if err := Install(id, info, onProgress); err != nil {
		return nil, err
	}
	if !info.Available && !status.Managed {
		info.Available = true
	}
	return info, nil
}

// Uninstall removes the current managed devkit tool.
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
	if state.Current != "" {
		root, err := versionRootDir(id, state.Current)
		if err != nil {
			return nil, err
		}
		if err := os.RemoveAll(root); err == nil {
			removed = true
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing managed version directory: %w", err)
		}
	}
	if err := removeCurrentMetadata(id); err != nil {
		return nil, err
	}
	return &UninstallResult{ID: id, Path: resultPath, Removed: removed}, nil
}

func lookupSpec(id ID) (spec, error) {
	s, ok := registry[id]
	if !ok {
		return spec{}, fmt.Errorf("unknown devkit tool: %s", id)
	}
	return s, nil
}

func toolsDir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tools"), nil
}

func toolDir(id ID) (string, error) {
	root, err := toolsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, string(id)), nil
}

func versionsDir(id ID) (string, error) {
	dir, err := toolDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "versions"), nil
}

func versionRootDir(id ID, version string) (string, error) {
	dir, err := versionsDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, version), nil
}

func currentMetadataPath(id ID) (string, error) {
	dir, err := toolDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "current.json"), nil
}

func versionBinaryPath(s spec, version string) (string, error) {
	root, err := versionRootDir(s.ID, s.Normalize(version))
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(entrySubpath(s))), nil
}

func executableFor(s spec, goos, goarch string) string {
	if s.ExecutableFor != nil {
		if executable := strings.TrimSpace(s.ExecutableFor(goos, goarch)); executable != "" {
			return executable
		}
	}
	return s.Executable
}

func entrySubpath(s spec) string {
	if s.EntrySubpathFor != nil {
		if entry := strings.TrimSpace(s.EntrySubpathFor(runtime.GOOS, runtime.GOARCH)); entry != "" {
			return entry
		}
	}
	return s.EntrySubpath
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
		managedToolLogger().Warn("devkit state drift detected",
			"tool", id,
			"installed_path", installedPath,
			"current_version", current,
		)
		if err := removeCurrentMetadata(id); err != nil {
			return nil, err
		}
	}

	if id == ToolBun {
		migrated, err := migrateLegacyAppsBun(s)
		if err != nil {
			return nil, err
		}
		if migrated != nil {
			return migrated, nil
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

func installedVersionAtPath(s spec, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, s.VersionArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return s.Normalize(string(out))
}

func isManagedActivePath(activePath, installedPath string) bool {
	if activePath == "" || installedPath == "" {
		return false
	}
	activePath = filepath.Clean(activePath)
	if installedPath != "" && activePath == filepath.Clean(installedPath) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(activePath); err == nil {
		installedResolved, installedErr := filepath.EvalSymlinks(installedPath)
		if installedErr == nil {
			return filepath.Clean(resolved) == filepath.Clean(installedResolved)
		}
		return filepath.Clean(resolved) == filepath.Clean(installedPath)
	}
	// On Windows the stable path is a managed copy rather than a symlink.
	return runtime.GOOS == "windows"
}

func readCurrentMetadata(id ID) (string, error) {
	s, err := lookupSpec(id)
	if err != nil {
		return "", err
	}
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
	return s.Normalize(meta.Current), nil
}

func writeCurrentMetadata(id ID, version string) error {
	s, err := lookupSpec(id)
	if err != nil {
		return err
	}
	path, err := currentMetadataPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating devkit tool directory: %w", err)
	}
	meta := currentMetadata{
		Current:   s.Normalize(version),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding current metadata: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
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

func downloadAndExtractArchive(info *ReleaseInfo, destDir, pattern, action string, onProgress ProgressFunc) error {
	tmpPath, err := downloadToTemp(info.AssetURL, destDir, pattern, action, onProgress)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	if strings.TrimSpace(info.SHA256) != "" {
		if err := verifySHA256(tmpPath, info.SHA256); err != nil {
			return err
		}
	}

	name := strings.TrimSpace(info.AssetName)
	if name == "" {
		name = info.AssetURL
	}
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return extractTarGz(tmpPath, destDir)
	case strings.HasSuffix(name, ".zip"):
		return extractZip(tmpPath, destDir)
	default:
		return fmt.Errorf("unsupported archive format for %q", name)
	}
}

func downloadToTemp(url, destDir, pattern, action string, onProgress ProgressFunc) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("%s: %w", action, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: returned %d", action, resp.StatusCode)
	}

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{
			r:     resp.Body,
			total: resp.ContentLength,
			fn:    onProgress,
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("creating archive destination: %w", err)
	}
	tmp, err := os.CreateTemp(destDir, pattern)
	if err != nil {
		return "", fmt.Errorf("creating archive temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("writing archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("closing archive temp file: %w", err)
	}
	return tmpPath, nil
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening archive for checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing archive: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	want = strings.ToLower(strings.TrimSpace(want))
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
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

func ensureActiveBinary(target, stablePath string, force bool) error {
	if target == "" || stablePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(stablePath), 0o755); err != nil {
		return fmt.Errorf("creating devkit bin directory: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := os.Chmod(tmpPath, 0o755); err != nil {
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

func normalizeVVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	match := vVersionPattern.FindString(raw)
	if match == "" {
		return raw
	}
	if strings.HasPrefix(match, "v") {
		return match
	}
	return "v" + match
}

func normalizeGoVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	match := goVersionPattern.FindString(raw)
	if match == "" {
		return raw
	}
	if strings.HasPrefix(match, "go") {
		return match
	}
	return "go" + match
}
