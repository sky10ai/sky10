package apps

import (
	"bytes"
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
}

func TestStatusFor_ManagedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("PATH", "")

	path, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "#!/bin/sh\necho v0.5.0\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
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
	if status.ActivePath != path {
		t.Fatalf("active path = %q, want %q", status.ActivePath, path)
	}
	if status.Version != "v0.5.0" {
		t.Fatalf("version = %q, want %q", status.Version, "v0.5.0")
	}
}

func TestCheckRelease_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.5.0",
			"assets": []map[string]string{
				{"name": registry[AppOWS].AssetName(runtime.GOOS, runtime.GOARCH), "browser_download_url": "https://example.com/ows"},
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

func TestInstall_DownloadsBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	content := "#!/bin/sh\necho ows-fake\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.Write([]byte(content))
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

	dest, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading installed binary: %v", err)
	}
	if string(data) != content {
		t.Fatalf("content = %q, want %q", string(data), content)
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

	dest, err := ManagedPath(AppOWS)
	if err != nil {
		t.Fatalf("ManagedPath() error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("test"), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	result, err := Uninstall(AppOWS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Removed {
		t.Fatal("expected removed=true")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err=%v", dest, err)
	}
}
