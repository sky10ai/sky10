package update

import (
	"path/filepath"
	"testing"
)

func TestPlatformAssetNames(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		goarch    string
		wantCLI   string
		wantMenu  string
		wantStage string
	}{
		{
			name:      "linux",
			goos:      "linux",
			goarch:    "amd64",
			wantCLI:   "sky10-linux-amd64",
			wantMenu:  "sky10-menu-linux-amd64",
			wantStage: "sky10",
		},
		{
			name:      "windows",
			goos:      "windows",
			goarch:    "arm64",
			wantCLI:   "sky10-windows-arm64.exe",
			wantMenu:  "sky10-menu-windows-arm64.exe",
			wantStage: "sky10.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cliAssetName(tt.goos, tt.goarch); got != tt.wantCLI {
				t.Fatalf("cliAssetName = %q, want %q", got, tt.wantCLI)
			}
			if got := menuAssetName(tt.goos, tt.goarch); got != tt.wantMenu {
				t.Fatalf("menuAssetName = %q, want %q", got, tt.wantMenu)
			}
			if got := cliBinaryName(tt.goos); got != tt.wantStage {
				t.Fatalf("cliBinaryName = %q, want %q", got, tt.wantStage)
			}
		})
	}
}

func TestMenuInstallPathUsesWindowsExecutableSuffix(t *testing.T) {
	home := filepath.Join("home", "sky")
	got := menuInstallPath(home, "windows")
	want := filepath.Join(home, ".bin", "sky10-menu.exe")
	if got != want {
		t.Fatalf("menuInstallPath = %q, want %q", got, want)
	}
}
