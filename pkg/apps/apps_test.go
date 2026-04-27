package apps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestManagedPath_UsesRootDir(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())

	p, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	want := filepath.Join(os.Getenv(config.EnvHome), "bin", "ows")
	if p != want {
		t.Fatalf("ManagedPath() = %q, want %q", p, want)
	}
}

func TestStatusFor_MissingManagedBinaryWritesAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("PATH", "")

	if err := writeCurrentMetadata(AppOWS, "v1.2.4"); err != nil {
		t.Fatalf("writeCurrentMetadata() error: %v", err)
	}

	status, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("StatusFor() error: %v", err)
	}
	if status.Installed {
		t.Fatal("expected installed=false")
	}
	if status.Managed {
		t.Fatal("expected managed=false")
	}

	events := readManagedAppAuditEvents(t)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	wantInstalled, err := versionBinaryPath(registry[AppOWS], "v1.2.4")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if events[0].Event != "state_drift_detected" {
		t.Fatalf("event = %q, want state_drift_detected", events[0].Event)
	}
	if events[0].App != AppOWS {
		t.Fatalf("app = %q, want %q", events[0].App, AppOWS)
	}
	if events[0].MissingPath != wantInstalled {
		t.Fatalf("missing path = %q, want %q", events[0].MissingPath, wantInstalled)
	}
	if events[0].CurrentVersion != "v1.2.4" {
		t.Fatalf("current version = %q, want %q", events[0].CurrentVersion, "v1.2.4")
	}
	if got, err := readCurrentMetadata(AppOWS); err != nil {
		t.Fatalf("readCurrentMetadata() error: %v", err)
	} else if got != "" {
		t.Fatalf("current metadata = %q, want empty", got)
	}
}

func TestStatusFor_NotInstalled(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv("PATH", "")

	status, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Installed {
		t.Fatal("expected installed=false")
	}
	if status.Managed {
		t.Fatal("expected managed=false")
	}
	if status.ActivePath != "" {
		t.Fatalf("active path = %q, want empty", status.ActivePath)
	}
	if status.ManagedPath != "" {
		t.Fatalf("managed path = %q, want empty", status.ManagedPath)
	}
}

func TestStatusFor_ManagedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("PATH", "")

	s := registry[AppOWS]
	installedPath, err := versionBinaryPath(s, "v1.2.4")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(installedPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "#!/bin/sh\necho 'ows 1.2.4 (2fbd309)'\n"
	if err := os.WriteFile(installedPath, []byte(content), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := writeCurrentMetadata(AppOWS, "v1.2.4"); err != nil {
		t.Fatalf("writeCurrentMetadata() error: %v", err)
	}

	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := ensureActiveBinary(installedPath, stablePath, true); err != nil {
		t.Fatalf("ensureActiveBinary() error: %v", err)
	}

	status, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Installed {
		t.Fatal("expected installed=true")
	}
	if !status.Managed {
		t.Fatal("expected managed=true")
	}
	if status.ManagedPath != installedPath {
		t.Fatalf("managed path = %q, want %q", status.ManagedPath, installedPath)
	}
	if status.ActivePath != stablePath {
		t.Fatalf("active path = %q, want %q", status.ActivePath, stablePath)
	}
	if status.Version != "v1.2.4" {
		t.Fatalf("version = %q, want %q", status.Version, "v1.2.4")
	}
}

func TestStatusFor_MigratesLegacyManagedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("PATH", "")

	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(stablePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "#!/bin/sh\necho 'ows 1.2.4 (2fbd309)'\n"
	if err := os.WriteFile(stablePath, []byte(content), 0755); err != nil {
		t.Fatalf("write legacy binary: %v", err)
	}

	status, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Managed {
		t.Fatal("expected managed=true")
	}
	if status.Version != "v1.2.4" {
		t.Fatalf("version = %q, want %q", status.Version, "v1.2.4")
	}
	wantInstalled, err := versionBinaryPath(registry[AppOWS], "v1.2.4")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if status.ManagedPath != wantInstalled {
		t.Fatalf("managed path = %q, want %q", status.ManagedPath, wantInstalled)
	}
	if _, err := os.Stat(wantInstalled); err != nil {
		t.Fatalf("expected migrated binary at %q: %v", wantInstalled, err)
	}
	if got, err := readCurrentMetadata(AppOWS); err != nil {
		t.Fatalf("readCurrentMetadata() error: %v", err)
	} else if got != "v1.2.4" {
		t.Fatalf("current metadata = %q, want %q", got, "v1.2.4")
	}
}

func TestCheckRelease_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assets := registry[AppOWS].ReleaseAssets("v0.5.0", runtime.GOOS, runtime.GOARCH)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets": []map[string]string{
				{"name": assets[0].Name, "browser_download_url": "https://example.com/ows"},
			},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppOWS, "v0.4.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Available {
		t.Fatal("expected update available")
	}
	if info.Latest != "v0.5.0" {
		t.Fatalf("latest = %q, want %q", info.Latest, "v0.5.0")
	}
	if info.AssetURL != "https://example.com/ows" {
		t.Fatalf("asset URL = %q, want %q", info.AssetURL, "https://example.com/ows")
	}
}

func TestCheckRelease_AlreadyUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppOWS, "v0.5.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Available {
		t.Fatal("expected no update available")
	}
}

func TestCheckRelease_NormalizesInstalledVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v1.2.4",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppOWS, "ows 1.2.4 (2fbd309)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Current != "v1.2.4" {
		t.Fatalf("current = %q, want %q", info.Current, "v1.2.4")
	}
	if info.Available {
		t.Fatal("expected no update available")
	}
}

func TestManagedRuntimePlatformAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		goos         string
		goarch       string
		wantBunAsset string
		wantBunEntry string
		wantZerobox  string
	}{
		{
			name:         "darwin arm64",
			goos:         "darwin",
			goarch:       "arm64",
			wantBunAsset: "bun-darwin-aarch64",
			wantBunEntry: filepath.Join("bun-darwin-aarch64", "bun"),
			wantZerobox:  "zerobox-aarch64-apple-darwin.tar.gz",
		},
		{
			name:         "darwin amd64",
			goos:         "darwin",
			goarch:       "amd64",
			wantBunAsset: "bun-darwin-x64",
			wantBunEntry: filepath.Join("bun-darwin-x64", "bun"),
			wantZerobox:  "zerobox-x86_64-apple-darwin.tar.gz",
		},
		{
			name:         "linux arm64",
			goos:         "linux",
			goarch:       "arm64",
			wantBunAsset: "bun-linux-aarch64",
			wantBunEntry: filepath.Join("bun-linux-aarch64", "bun"),
			wantZerobox:  "zerobox-aarch64-unknown-linux-gnu.tar.gz",
		},
		{
			name:         "linux amd64",
			goos:         "linux",
			goarch:       "amd64",
			wantBunAsset: "bun-linux-x64",
			wantBunEntry: filepath.Join("bun-linux-x64", "bun"),
			wantZerobox:  "zerobox-x86_64-unknown-linux-gnu.tar.gz",
		},
		{
			name:         "windows amd64",
			goos:         "windows",
			goarch:       "amd64",
			wantBunAsset: "bun-windows-x64",
			wantBunEntry: filepath.Join("bun-windows-x64", "bun.exe"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bunAsset, _ := bunReleasePlatform(tt.goos, tt.goarch)
			if bunAsset != tt.wantBunAsset {
				t.Fatalf("bun asset = %q, want %q", bunAsset, tt.wantBunAsset)
			}
			if got := entrySubpathFor(registry[AppBun], tt.goos, tt.goarch); got != tt.wantBunEntry {
				t.Fatalf("bun entry = %q, want %q", got, tt.wantBunEntry)
			}
			if got := zeroboxReleaseAsset(tt.goos, tt.goarch); got != tt.wantZerobox {
				t.Fatalf("zerobox asset = %q, want %q", got, tt.wantZerobox)
			}
		})
	}
}

func TestInstall_DownloadsBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	content := "#!/bin/sh\necho ows-fake\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()

	info := &ReleaseInfo{
		ID:       AppOWS,
		Latest:   "v0.5.0",
		AssetURL: srv.URL + "/ows",
	}
	if err := Install(AppOWS, info, nil); err != nil {
		t.Fatalf("install error: %v", err)
	}

	installedPath, err := InstalledPath(AppOWS)
	if err != nil {
		t.Fatalf("InstalledPath() error: %v", err)
	}
	wantInstalled, err := versionBinaryPath(registry[AppOWS], "v0.5.0")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if installedPath != wantInstalled {
		t.Fatalf("InstalledPath() = %q, want %q", installedPath, wantInstalled)
	}
	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("reading installed binary: %v", err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
	}

	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if _, err := os.Stat(stablePath); err != nil {
		t.Fatalf("expected active binary at %q: %v", stablePath, err)
	}
}

func TestCheckRelease_BunUsesDirectAssetURL(t *testing.T) {
	bunAsset, _ := bunReleasePlatform(runtime.GOOS, runtime.GOARCH)
	if bunAsset == "" {
		t.Skipf("bun has no asset mapping for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "bun-v1.2.3",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppBun, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Latest != "v1.2.3" {
		t.Fatalf("latest = %q, want v1.2.3", info.Latest)
	}
	want := fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-v1.2.3/%s.zip", bunAsset)
	if info.AssetURL != want {
		t.Fatalf("asset URL = %q, want %q", info.AssetURL, want)
	}
}

func TestCheckRelease_ZeroboxUsesDirectAssetURL(t *testing.T) {
	zeroboxAsset := zeroboxReleaseAsset(runtime.GOOS, runtime.GOARCH)
	if zeroboxAsset == "" {
		t.Skipf("zerobox has no asset mapping for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.2.6",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppZerobox, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("https://github.com/afshinm/zerobox/releases/download/v0.2.6/%s", zeroboxAsset)
	if info.AssetURL != want {
		t.Fatalf("asset URL = %q, want %q", info.AssetURL, want)
	}
}

func TestInstall_BunExtractsArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}
	entry := entrySubpath(registry[AppBun])
	if entry == "" {
		t.Skipf("bun has no entry mapping for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	archive := zipFixture(t, map[string]string{
		entry: "#!/bin/sh\necho '1.2.3'\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archive)))
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	info := &ReleaseInfo{
		ID:       AppBun,
		Latest:   "v1.2.3",
		AssetURL: srv.URL + "/bun.zip",
	}
	if err := Install(AppBun, info, nil); err != nil {
		t.Fatalf("install error: %v", err)
	}

	installedPath, err := InstalledPath(AppBun)
	if err != nil {
		t.Fatalf("InstalledPath() error: %v", err)
	}
	wantInstalled, err := versionBinaryPath(registry[AppBun], "v1.2.3")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if installedPath != wantInstalled {
		t.Fatalf("InstalledPath() = %q, want %q", installedPath, wantInstalled)
	}
	if _, err := os.Stat(installedPath); err != nil {
		t.Fatalf("expected extracted bun at %q: %v", installedPath, err)
	}
}

func TestUpgrade_InstallsManagedCopyWhenExternalBinaryIsAlreadyCurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	pathDir := t.TempDir()
	externalPath := filepath.Join(pathDir, "ows")
	externalContent := "#!/bin/sh\necho 'ows 0.5.0 (external)'\n"
	if err := os.WriteFile(externalPath, []byte(externalContent), 0o755); err != nil {
		t.Fatalf("write external binary: %v", err)
	}
	t.Setenv("PATH", pathDir)

	managedContent := "#!/bin/sh\necho 'ows 0.5.0 (managed)'\n"
	downloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(managedContent)))
		_, _ = w.Write([]byte(managedContent))
	}))
	defer downloadSrv.Close()

	releaseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assets := registry[AppOWS].ReleaseAssets("v0.5.0", runtime.GOOS, runtime.GOARCH)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets": []map[string]string{
				{"name": assets[0].Name, "browser_download_url": downloadSrv.URL + "/ows"},
			},
		})
	}))
	defer releaseSrv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string {
		return releaseSrv.URL
	}
	defer func() { ghReleaseURL = old }()

	before, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("StatusFor(before) error: %v", err)
	}
	if !before.Installed || before.Managed {
		t.Fatalf("before status = %#v, want external installed binary", before)
	}
	if before.ActivePath != externalPath {
		t.Fatalf("before active path = %q, want %q", before.ActivePath, externalPath)
	}

	info, err := Upgrade(AppOWS, nil)
	if err != nil {
		t.Fatalf("Upgrade() error: %v", err)
	}
	if !info.Available {
		t.Fatal("Upgrade() should report work performed when it stages a managed copy")
	}

	after, err := StatusFor(AppOWS)
	if err != nil {
		t.Fatalf("StatusFor(after) error: %v", err)
	}
	if !after.Managed {
		t.Fatalf("after status = %#v, want managed binary", after)
	}
	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if after.ActivePath != stablePath {
		t.Fatalf("after active path = %q, want %q", after.ActivePath, stablePath)
	}
	installedPath, err := InstalledPath(AppOWS)
	if err != nil {
		t.Fatalf("InstalledPath() error: %v", err)
	}
	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("read managed binary: %v", err)
	}
	if string(data) != managedContent {
		t.Fatalf("managed content = %q, want %q", string(data), managedContent)
	}
}

func TestCheckRelease_LimaUsesDirectAssetURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v1.2.3",
			"assets":   []map[string]string{},
		})
	}))
	defer srv.Close()

	old := ghReleaseURL
	ghReleaseURL = func(spec) string { return srv.URL }
	defer func() { ghReleaseURL = old }()

	info, err := CheckRelease(AppLima, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.AssetURL == "" {
		t.Fatal("expected primary Lima asset URL")
	}
	if len(info.ExtraAssetURLs) != 1 {
		t.Fatalf("extra asset urls = %d, want 1", len(info.ExtraAssetURLs))
	}
	if got, want := info.Latest, "v1.2.3"; got != want {
		t.Fatalf("latest = %q, want %q", got, want)
	}
}

func TestInstall_LimaExtractsArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	mainArchive := tarGzFixture(t, map[string]string{
		"bin/limactl": "#!/bin/sh\necho 'limactl version 1.2.3'\n",
	})
	extraArchive := tarGzFixture(t, map[string]string{
		"share/lima/guestagents/a.txt": "guestagent\n",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lima.tar.gz":
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(mainArchive)))
			_, _ = w.Write(mainArchive)
		case "/guestagents.tar.gz":
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(extraArchive)))
			_, _ = w.Write(extraArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info := &ReleaseInfo{
		ID:             AppLima,
		Latest:         "v1.2.3",
		AssetURL:       srv.URL + "/lima.tar.gz",
		ExtraAssetURLs: []string{srv.URL + "/guestagents.tar.gz"},
	}
	if err := Install(AppLima, info, nil); err != nil {
		t.Fatalf("install error: %v", err)
	}

	installedPath, err := InstalledPath(AppLima)
	if err != nil {
		t.Fatalf("InstalledPath() error: %v", err)
	}
	wantInstalled, err := versionBinaryPath(registry[AppLima], "v1.2.3")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if installedPath != wantInstalled {
		t.Fatalf("InstalledPath() = %q, want %q", installedPath, wantInstalled)
	}
	if _, err := os.Stat(installedPath); err != nil {
		t.Fatalf("expected extracted limactl at %q: %v", installedPath, err)
	}
	guestagent := filepath.Join(filepath.Dir(filepath.Dir(installedPath)), "share", "lima", "guestagents", "a.txt")
	if _, err := os.Stat(guestagent); err != nil {
		t.Fatalf("expected extracted guestagent asset at %q: %v", guestagent, err)
	}
}

func TestProgressReader(t *testing.T) {
	data := []byte("hello world, this is test data")
	r := &progressReader{
		r:     bytes.NewReader(data),
		total: int64(len(data)),
		fn: func(downloaded, total int64) {
			if total != int64(len(data)) {
				t.Fatalf("total = %d, want %d", total, len(data))
			}
		},
	}
	buf := make([]byte, 10)
	var totalRead int
	for {
		n, err := r.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}
	if totalRead != len(data) {
		t.Fatalf("read %d bytes, want %d", totalRead, len(data))
	}
}

func TestUninstall_RemovesManagedBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	s := registry[AppOWS]
	installedPath, err := versionBinaryPath(s, "v1.2.4")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(installedPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(installedPath, []byte("test"), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := writeCurrentMetadata(AppOWS, "v1.2.4"); err != nil {
		t.Fatalf("writeCurrentMetadata() error: %v", err)
	}
	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := ensureActiveBinary(installedPath, stablePath, true); err != nil {
		t.Fatalf("ensureActiveBinary() error: %v", err)
	}

	result, err := Uninstall(AppOWS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected removed=true")
	}
	if result.Path != installedPath {
		t.Fatalf("path = %q, want %q", result.Path, installedPath)
	}
	if _, err := os.Stat(installedPath); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err=%v", installedPath, err)
	}
	if _, err := os.Lstat(stablePath); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err=%v", stablePath, err)
	}
	if got, err := readCurrentMetadata(AppOWS); err != nil {
		t.Fatalf("readCurrentMetadata() error: %v", err)
	} else if got != "" {
		t.Fatalf("current metadata = %q, want empty", got)
	}
}

func TestUninstallWithAudit_WritesAuditLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	s := registry[AppOWS]
	installedPath, err := versionBinaryPath(s, "v1.2.4")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(installedPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(installedPath, []byte("test"), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := writeCurrentMetadata(AppOWS, "v1.2.4"); err != nil {
		t.Fatalf("writeCurrentMetadata() error: %v", err)
	}
	stablePath, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := ensureActiveBinary(installedPath, stablePath, true); err != nil {
		t.Fatalf("ensureActiveBinary() error: %v", err)
	}

	result, err := UninstallWithAudit(AppOWS, UninstallAuditInfo{
		Source:    "wallet.rpc",
		Method:    "wallet.uninstall",
		Transport: "http",
		Remote:    "127.0.0.1:9101",
	})
	if err != nil {
		t.Fatalf("UninstallWithAudit() error: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected removed=true")
	}

	events := readManagedAppAuditEvents(t)
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if events[0].Event != "uninstall_requested" {
		t.Fatalf("first event = %q, want uninstall_requested", events[0].Event)
	}
	if events[1].Event != "uninstall_completed" {
		t.Fatalf("second event = %q, want uninstall_completed", events[1].Event)
	}
	for i, event := range events {
		if event.Source != "wallet.rpc" {
			t.Fatalf("event %d source = %q, want wallet.rpc", i, event.Source)
		}
		if event.Method != "wallet.uninstall" {
			t.Fatalf("event %d method = %q, want wallet.uninstall", i, event.Method)
		}
		if event.Transport != "http" {
			t.Fatalf("event %d transport = %q, want http", i, event.Transport)
		}
		if event.Remote != "127.0.0.1:9101" {
			t.Fatalf("event %d remote = %q, want 127.0.0.1:9101", i, event.Remote)
		}
		if event.Process.PID != os.Getpid() {
			t.Fatalf("event %d pid = %d, want %d", i, event.Process.PID, os.Getpid())
		}
		if len(event.Process.Argv) == 0 {
			t.Fatalf("event %d argv should not be empty", i)
		}
	}
	if events[1].Path != installedPath {
		t.Fatalf("completed path = %q, want %q", events[1].Path, installedPath)
	}
	if events[1].Removed == nil || !*events[1].Removed {
		t.Fatal("completed event should report removed=true")
	}
}

func readManagedAppAuditEvents(t *testing.T) []managedAppAuditEvent {
	t.Helper()

	path, err := managedAppAuditLogPath()
	if err != nil {
		t.Fatalf("managedAppAuditLogPath() error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	events := make([]managedAppAuditEvent, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var event managedAppAuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("unmarshal audit event %q: %v", string(line), err)
		}
		events = append(events, event)
	}
	return events
}

func tarGzFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%q): %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("Write(%q): %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		hdr := &zip.FileHeader{Name: filepath.ToSlash(name)}
		hdr.SetMode(0o755)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("Write(%q): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}
