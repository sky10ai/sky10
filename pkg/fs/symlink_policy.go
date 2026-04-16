package fs

import (
	"fmt"
	"os"
	slashpath "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

type symlinkTargetKind string

const (
	symlinkTargetUnknown   symlinkTargetKind = "unknown"
	symlinkTargetFile      symlinkTargetKind = "file"
	symlinkTargetDirectory symlinkTargetKind = "directory"
)

type symlinkCapability struct {
	Supported bool
	Reason    string
}

var symlinkCapabilityDetector = detectPlatformSymlinkCapability
var symlinkCreator = createPlatformSymlink

func currentSymlinkCapability() symlinkCapability {
	if !windowsPathPolicyEnabled {
		return symlinkCapability{Supported: true}
	}
	status := symlinkCapabilityDetector()
	if !status.Supported && status.Reason == "" {
		status.Reason = "Windows symbolic links are unavailable on this machine"
	}
	return status
}

func snapshotSymlinkPaths(snap *opslog.Snapshot) []string {
	if snap == nil {
		return nil
	}
	paths := make([]string, 0)
	for path, fi := range snap.Files() {
		if fi.LinkTarget == "" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func inferSymlinkTargetKind(localRoot, linkPath, target string, snapshotFiles map[string]opslog.FileInfo, snapshotDirs map[string]opslog.DirInfo) symlinkTargetKind {
	if strings.HasSuffix(target, "/") || strings.HasSuffix(target, `\`) {
		return symlinkTargetDirectory
	}

	if resolved, ok := inferSymlinkLogicalTarget(linkPath, target); ok {
		if _, ok := snapshotDirs[resolved]; ok {
			return symlinkTargetDirectory
		}
		if _, ok := snapshotFiles[resolved]; ok {
			return symlinkTargetFile
		}
		prefix := resolved + "/"
		for path := range snapshotFiles {
			if strings.HasPrefix(path, prefix) {
				return symlinkTargetDirectory
			}
		}
		for path := range snapshotDirs {
			if path == resolved || strings.HasPrefix(path, prefix) {
				return symlinkTargetDirectory
			}
		}
	}

	if targetInfo, ok := statSymlinkTarget(localRoot, linkPath, target); ok {
		if targetInfo.IsDir() {
			return symlinkTargetDirectory
		}
		return symlinkTargetFile
	}

	return symlinkTargetUnknown
}

func inferSymlinkLogicalTarget(linkPath, target string) (string, bool) {
	if target == "" {
		return "", false
	}
	if filepath.IsAbs(target) {
		return "", false
	}
	target = strings.ReplaceAll(target, `\`, "/")
	if len(target) >= 2 && target[1] == ':' && isASCIIAlpha(target[0]) {
		return "", false
	}
	joined := slashpath.Clean(slashpath.Join(slashpath.Dir(linkPath), target))
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	canonical, err := canonicalLogicalPath(joined)
	if err != nil {
		return "", false
	}
	return canonical, true
}

func statSymlinkTarget(localRoot, linkPath, target string) (os.FileInfo, bool) {
	linkLocal, err := LogicalPathToLocal(localRoot, linkPath)
	if err != nil {
		return nil, false
	}
	targetPath := target
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(filepath.Dir(linkLocal), filepath.FromSlash(strings.ReplaceAll(targetPath, `\`, "/")))
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return nil, false
	}
	return info, true
}

func sortPathPolicyIssues(issues []pathPolicyIssue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		left := strings.Join(issues[i].Paths, "\x00")
		right := strings.Join(issues[j].Paths, "\x00")
		if left != right {
			return left < right
		}
		return issues[i].Reason < issues[j].Reason
	})
}

func unsupportedWindowsSymlinkIssue(snap *opslog.Snapshot) *pathPolicyIssue {
	return unsupportedWindowsSymlinkIssueForCapability(snap, currentSymlinkCapability())
}

func unsupportedWindowsSymlinkIssueForCapability(snap *opslog.Snapshot, capability symlinkCapability) *pathPolicyIssue {
	paths := snapshotSymlinkPaths(snap)
	if len(paths) == 0 {
		return nil
	}
	if capability.Supported {
		return nil
	}
	reason := capability.Reason
	if reason == "" {
		reason = fmt.Sprintf("%s", pathPolicyIssueReasonWindowsSymlinkUnsupported)
	}
	return &pathPolicyIssue{
		Kind:   pathPolicyIssueWindowsSymlinkUnsupported,
		Paths:  paths,
		Reason: reason,
	}
}
