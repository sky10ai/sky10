package update

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

func withStageEnv(t *testing.T) (home string, execPath string) {
	t.Helper()
	oldHome := updateUserHomeDir
	oldExec := updateExecutablePathFunc

	home = t.TempDir()
	execPath = filepath.Join(t.TempDir(), "sky10")
	if err := os.WriteFile(execPath, []byte("old-cli"), 0755); err != nil {
		t.Fatalf("write exec: %v", err)
	}

	updateUserHomeDir = func() (string, error) { return home, nil }
	updateExecutablePathFunc = func() (string, error) { return execPath, nil }
	t.Cleanup(func() {
		updateUserHomeDir = oldHome
		updateExecutablePathFunc = oldExec
	})

	return home, execPath
}

func TestStageStatusInstallLifecycle(t *testing.T) {
	home, execPath := withStageEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli":
			fmt.Fprint(w, "new-cli")
		case "/menu":
			fmt.Fprint(w, "new-menu")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info := &Info{
		Current:       "v1.0.0",
		Latest:        "v2.0.0",
		Available:     true,
		CLIAvailable:  true,
		MenuAvailable: true,
		AssetURL:      srv.URL + "/cli",
		MenuAssetURL:  srv.URL + "/menu",
	}

	staged, err := Stage(info, nil)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if staged == nil {
		t.Fatal("expected staged update")
	}

	status, err := Status("v1.0.0")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Ready || !status.CLIStaged || !status.MenuStaged {
		t.Fatalf("unexpected status: %+v", status)
	}

	installed, err := InstallStaged()
	if err != nil {
		t.Fatalf("InstallStaged: %v", err)
	}
	if installed.Latest != "v2.0.0" {
		t.Fatalf("installed latest = %q, want %q", installed.Latest, "v2.0.0")
	}

	cliData, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("ReadFile cli: %v", err)
	}
	if string(cliData) != "new-cli" {
		t.Fatalf("cli contents = %q, want %q", string(cliData), "new-cli")
	}

	menuPath := menuInstallPath(home, runtime.GOOS)
	menuData, err := os.ReadFile(menuPath)
	if err != nil {
		t.Fatalf("ReadFile menu: %v", err)
	}
	if string(menuData) != "new-menu" {
		t.Fatalf("menu contents = %q, want %q", string(menuData), "new-menu")
	}

	status, err = Status("v2.0.0")
	if err != nil {
		t.Fatalf("Status after install: %v", err)
	}
	if status.Ready {
		t.Fatalf("expected cleared status after install: %+v", status)
	}
}

func TestStageReusesExistingDownload(t *testing.T) {
	withStageEnv(t)

	var cliDownloads atomic.Int32
	var menuDownloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli":
			cliDownloads.Add(1)
			fmt.Fprint(w, "new-cli")
		case "/menu":
			menuDownloads.Add(1)
			fmt.Fprint(w, "new-menu")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	info := &Info{
		Current:       "v1.0.0",
		Latest:        "v2.0.0",
		Available:     true,
		CLIAvailable:  true,
		MenuAvailable: true,
		AssetURL:      srv.URL + "/cli",
		MenuAssetURL:  srv.URL + "/menu",
	}

	if _, err := Stage(info, nil); err != nil {
		t.Fatalf("Stage first: %v", err)
	}
	if _, err := Stage(info, nil); err != nil {
		t.Fatalf("Stage second: %v", err)
	}

	if cliDownloads.Load() != 1 {
		t.Fatalf("cli downloads = %d, want 1", cliDownloads.Load())
	}
	if menuDownloads.Load() != 1 {
		t.Fatalf("menu downloads = %d, want 1", menuDownloads.Load())
	}
}

func TestCheckReportsMenuUpdateWhenMenuBinaryIsMissing(t *testing.T) {
	withStageEnv(t)

	asset := cliAssetName(runtime.GOOS, runtime.GOARCH)
	menuAsset := menuAssetName(runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":%q,"browser_download_url":"https://example.com/%s"},{"name":%q,"browser_download_url":"https://example.com/%s"}]}`, asset, asset, menuAsset, menuAsset)
	}))
	defer srv.Close()

	oldCheck := checkURL
	checkURL = srv.URL
	defer func() { checkURL = oldCheck }()

	info, err := Check("v1.0.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !info.MenuAvailable {
		t.Fatal("expected menu update when local menu binary is missing")
	}
	if !info.Available {
		t.Fatal("expected overall update availability when menu binary is missing")
	}
	if info.CLIAvailable {
		t.Fatal("did not expect CLI update for matching versions")
	}
}
