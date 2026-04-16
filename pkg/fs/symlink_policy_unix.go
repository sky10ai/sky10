//go:build !windows

package fs

import "os"

func detectPlatformSymlinkCapability() symlinkCapability {
	return symlinkCapability{Supported: true}
}

func createPlatformSymlink(target, linkPath string, _ symlinkTargetKind) error {
	return os.Symlink(target, linkPath)
}
