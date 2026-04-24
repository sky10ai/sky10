package update

import (
	"fmt"
	"path/filepath"
)

func cliAssetName(goos, goarch string) string {
	return fmt.Sprintf("sky10-%s-%s%s", goos, goarch, executableSuffix(goos))
}

func menuAssetName(goos, goarch string) string {
	return fmt.Sprintf("sky10-menu-%s-%s%s", goos, goarch, executableSuffix(goos))
}

func cliBinaryName(goos string) string {
	return "sky10" + executableSuffix(goos)
}

func menuBinaryName(goos string) string {
	return "sky10-menu" + executableSuffix(goos)
}

func menuInstallPath(home, goos string) string {
	return filepath.Join(home, ".bin", menuBinaryName(goos))
}

func executableSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}
