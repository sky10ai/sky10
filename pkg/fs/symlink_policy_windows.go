//go:build windows

package fs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var (
	windowsSymlinkCapabilityOnce sync.Once
	windowsSymlinkCapability     symlinkCapability
)

const windowsSymbolicLinkFlagAllowUnprivilegedCreate = 0x2

func detectPlatformSymlinkCapability() symlinkCapability {
	windowsSymlinkCapabilityOnce.Do(func() {
		windowsSymlinkCapability = probeWindowsSymlinkCapability()
	})
	return windowsSymlinkCapability
}

func probeWindowsSymlinkCapability() symlinkCapability {
	probeDir := filepath.Join(os.TempDir(), "sky10-symlink-probe")
	if err := os.MkdirAll(probeDir, 0700); err != nil {
		return symlinkCapability{
			Supported: false,
			Reason:    fmt.Sprintf("%s: %v", pathPolicyIssueReasonWindowsSymlinkUnsupported, err),
		}
	}

	target := filepath.Join(probeDir, fmt.Sprintf("target-%d.txt", time.Now().UnixNano()))
	link := filepath.Join(probeDir, fmt.Sprintf("link-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(target, []byte("ok"), 0600); err != nil {
		return symlinkCapability{
			Supported: false,
			Reason:    fmt.Sprintf("%s: %v", pathPolicyIssueReasonWindowsSymlinkUnsupported, err),
		}
	}
	defer os.Remove(target)
	defer os.Remove(link)

	if err := createPlatformSymlink(target, link, symlinkTargetFile); err != nil {
		return symlinkCapability{
			Supported: false,
			Reason:    fmt.Sprintf("%s: %v", pathPolicyIssueReasonWindowsSymlinkUnsupported, err),
		}
	}
	return symlinkCapability{Supported: true}
}

func createPlatformSymlink(target, linkPath string, kind symlinkTargetKind) error {
	flags := uint32(0)
	if kind == symlinkTargetDirectory {
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	}

	if err := createWindowsSymbolicLink(target, linkPath, flags|windowsSymbolicLinkFlagAllowUnprivilegedCreate); err == nil {
		return nil
	} else if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return err
	}

	return createWindowsSymbolicLink(target, linkPath, flags)
}

func createWindowsSymbolicLink(target, linkPath string, flags uint32) error {
	linkUTF16, err := windows.UTF16PtrFromString(linkPath)
	if err != nil {
		return &os.LinkError{Op: "symlink", Old: target, New: linkPath, Err: err}
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return &os.LinkError{Op: "symlink", Old: target, New: linkPath, Err: err}
	}
	if err := windows.CreateSymbolicLink(linkUTF16, targetUTF16, flags); err != nil {
		return &os.LinkError{Op: "symlink", Old: target, New: linkPath, Err: err}
	}
	return nil
}
