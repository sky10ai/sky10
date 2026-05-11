package devkit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func TestManagedPathUsesDevkitBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	p, err := ManagedPath(ToolGo)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	want := filepath.Join(home, "devkit", "bin", goExecutableFor(runtime.GOOS, runtime.GOARCH))
	if p != want {
		t.Fatalf("ManagedPath() = %q, want %q", p, want)
	}
}

func TestDevkitPlatformMappings(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		goarch    string
		wantBun   string
		wantEntry string
	}{
		{
			name:      "darwin arm64",
			goos:      "darwin",
			goarch:    "arm64",
			wantBun:   "bun-darwin-aarch64",
			wantEntry: "bun-darwin-aarch64/bun",
		},
		{
			name:      "darwin amd64",
			goos:      "darwin",
			goarch:    "amd64",
			wantBun:   "bun-darwin-x64",
			wantEntry: "bun-darwin-x64/bun",
		},
		{
			name:      "linux arm64",
			goos:      "linux",
			goarch:    "arm64",
			wantBun:   "bun-linux-aarch64",
			wantEntry: "bun-linux-aarch64/bun",
		},
		{
			name:      "linux amd64",
			goos:      "linux",
			goarch:    "amd64",
			wantBun:   "bun-linux-x64",
			wantEntry: "bun-linux-x64/bun",
		},
		{
			name:      "windows arm64",
			goos:      "windows",
			goarch:    "arm64",
			wantBun:   "bun-windows-aarch64",
			wantEntry: "bun-windows-aarch64/bun.exe",
		},
		{
			name:      "windows amd64",
			goos:      "windows",
			goarch:    "amd64",
			wantBun:   "bun-windows-x64",
			wantEntry: "bun-windows-x64/bun.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bunAsset, _ := bunReleasePlatform(tt.goos, tt.goarch)
			if bunAsset != tt.wantBun {
				t.Fatalf("bun asset = %q, want %q", bunAsset, tt.wantBun)
			}
			if got := bunEntrySubpath(tt.goos, tt.goarch); got != tt.wantEntry {
				t.Fatalf("bun entry = %q, want %q", got, tt.wantEntry)
			}
			if got := goEntrySubpath(tt.goos, tt.goarch); got != "go/bin/"+goExecutableFor(tt.goos, tt.goarch) {
				t.Fatalf("go entry = %q", got)
			}
		})
	}
}

func TestCheckGoReleaseUsesArchiveAndChecksum(t *testing.T) {
	oldManifest := goManifestURL
	oldBase := goDownloadBaseURL
	defer func() {
		goManifestURL = oldManifest
		goDownloadBaseURL = oldBase
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `[{
			"version":"go1.26.3",
			"stable":true,
			"files":[{
				"filename":"go1.26.3.%s-%s.%s",
				"os":"%s",
				"arch":"%s",
				"version":"go1.26.3",
				"sha256":"abc123",
				"kind":"archive",
				"size":123
			}]
		}]`, runtime.GOOS, runtime.GOARCH, goArchiveExt(), runtime.GOOS, runtime.GOARCH)
	}))
	defer srv.Close()

	goManifestURL = srv.URL + "/manifest"
	goDownloadBaseURL = srv.URL + "/dl/"

	info, err := CheckRelease(ToolGo, "", "1.26.3")
	if err != nil {
		t.Fatalf("CheckRelease() error: %v", err)
	}
	if info.Latest != "go1.26.3" {
		t.Fatalf("latest = %q, want go1.26.3", info.Latest)
	}
	if info.SHA256 != "abc123" {
		t.Fatalf("sha256 = %q, want abc123", info.SHA256)
	}
	wantName := fmt.Sprintf("go1.26.3.%s-%s.%s", runtime.GOOS, runtime.GOARCH, goArchiveExt())
	if info.AssetName != wantName {
		t.Fatalf("asset name = %q, want %q", info.AssetName, wantName)
	}
	if info.AssetURL != srv.URL+"/dl/"+wantName {
		t.Fatalf("asset URL = %q", info.AssetURL)
	}
}

func TestInstallGoExtractsArchiveAndActivates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	archive := tarGzFixture(t, map[string]string{
		goEntrySubpath(runtime.GOOS, runtime.GOARCH): "#!/bin/sh\necho 'go version go1.26.3 test/test'\n",
	})
	sum := sha256.Sum256(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(archive)))
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	info := &ReleaseInfo{
		ID:        ToolGo,
		Latest:    "go1.26.3",
		AssetName: "go1.26.3.test.tar.gz",
		AssetURL:  srv.URL + "/go.tar.gz",
		SHA256:    hex.EncodeToString(sum[:]),
	}
	if err := Install(ToolGo, info, nil); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	installedPath, err := InstalledPath(ToolGo)
	if err != nil {
		t.Fatalf("InstalledPath() error: %v", err)
	}
	wantInstalled, err := versionBinaryPath(registry[ToolGo], "go1.26.3")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if installedPath != wantInstalled {
		t.Fatalf("InstalledPath() = %q, want %q", installedPath, wantInstalled)
	}

	status, err := StatusFor(ToolGo)
	if err != nil {
		t.Fatalf("StatusFor() error: %v", err)
	}
	if !status.Installed || !status.Managed {
		t.Fatalf("status = %#v, want managed install", status)
	}
	if status.Version != "go1.26.3" {
		t.Fatalf("version = %q, want go1.26.3", status.Version)
	}
}

func TestBunMigratesLegacyAppsInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	oldEntry := bunEntrySubpath(runtime.GOOS, runtime.GOARCH)
	oldInstalled := filepath.Join(home, "apps", "bun", "versions", "v1.2.3", filepath.FromSlash(oldEntry))
	if err := os.MkdirAll(filepath.Dir(oldInstalled), 0o755); err != nil {
		t.Fatalf("mkdir legacy bun: %v", err)
	}
	if err := os.WriteFile(oldInstalled, []byte("#!/bin/sh\necho '1.2.3'\n"), 0o755); err != nil {
		t.Fatalf("write legacy bun: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "apps", "bun"), 0o755); err != nil {
		t.Fatalf("mkdir legacy metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "apps", "bun", "current.json"), []byte(`{"current":"v1.2.3"}`), 0o644); err != nil {
		t.Fatalf("write legacy metadata: %v", err)
	}

	status, err := StatusFor(ToolBun)
	if err != nil {
		t.Fatalf("StatusFor() error: %v", err)
	}
	if !status.Installed || !status.Managed {
		t.Fatalf("status = %#v, want managed migrated bun", status)
	}
	if status.Version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", status.Version)
	}
	wantInstalled, err := versionBinaryPath(registry[ToolBun], "v1.2.3")
	if err != nil {
		t.Fatalf("versionBinaryPath() error: %v", err)
	}
	if status.ManagedPath != wantInstalled {
		t.Fatalf("managed path = %q, want %q", status.ManagedPath, wantInstalled)
	}
	if _, err := os.Stat(filepath.Join(home, "apps", "bun", "current.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy current metadata still exists or stat failed: %v", err)
	}
}

func goArchiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
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
