package fs

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

var windowsPathPolicyEnabled = runtime.GOOS == "windows"

type pathPolicyIssueKind string

const (
	pathPolicyIssueWindowsInvalid  pathPolicyIssueKind = "windows_invalid_path"
	pathPolicyIssueCaseCollision   pathPolicyIssueKind = "windows_case_collision"
	pathPolicyIssueReasonCollision                     = "paths collide on Windows case-insensitive filesystem"
)

type pathPolicyIssue struct {
	Kind   pathPolicyIssueKind `json:"kind"`
	Paths  []string            `json:"paths"`
	Reason string              `json:"reason"`
}

func detectWindowsPathIssues(paths []string) []pathPolicyIssue {
	if len(paths) == 0 {
		return nil
	}

	unique := make(map[string]bool, len(paths))
	caseGroups := make(map[string][]string)
	issues := make([]pathPolicyIssue, 0)

	for _, path := range paths {
		if unique[path] {
			continue
		}
		unique[path] = true

		canonical, err := canonicalLogicalPath(path)
		if err != nil {
			issues = append(issues, pathPolicyIssue{
				Kind:   pathPolicyIssueWindowsInvalid,
				Paths:  []string{path},
				Reason: err.Error(),
			})
			continue
		}
		if err := ValidateWindowsLogicalPath(canonical); err != nil {
			issues = append(issues, pathPolicyIssue{
				Kind:   pathPolicyIssueWindowsInvalid,
				Paths:  []string{canonical},
				Reason: err.Error(),
			})
			continue
		}

		key := windowsCaseFoldKey(canonical)
		caseGroups[key] = append(caseGroups[key], canonical)
	}

	for _, group := range caseGroups {
		if len(group) <= 1 {
			continue
		}
		sort.Strings(group)
		issues = append(issues, pathPolicyIssue{
			Kind:   pathPolicyIssueCaseCollision,
			Paths:  group,
			Reason: pathPolicyIssueReasonCollision,
		})
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		left := strings.Join(issues[i].Paths, "\x00")
		right := strings.Join(issues[j].Paths, "\x00")
		return left < right
	})

	return issues
}

func detectWindowsSnapshotPathIssues(snap *opslog.Snapshot) []pathPolicyIssue {
	if snap == nil {
		return nil
	}
	paths := make([]string, 0, len(snap.Files())+len(snap.Dirs()))
	for path := range snap.Files() {
		paths = append(paths, path)
	}
	for path := range snap.Dirs() {
		paths = append(paths, path)
	}
	return detectWindowsPathIssues(paths)
}

func activeSnapshotPathIssues(snap *opslog.Snapshot) []pathPolicyIssue {
	if !windowsPathPolicyEnabled {
		return nil
	}
	return detectWindowsSnapshotPathIssues(snap)
}

func summarizePathPolicyIssues(issues []pathPolicyIssue) (int, string) {
	if len(issues) == 0 {
		return 0, ""
	}
	first := issues[0]
	if len(first.Paths) == 1 {
		return len(issues), fmt.Sprintf("%s: %s", first.Paths[0], first.Reason)
	}
	return len(issues), fmt.Sprintf("%s (%s)", strings.Join(first.Paths, ", "), first.Reason)
}

func pathIssueBlocksPath(path string, issues []pathPolicyIssue) bool {
	if len(issues) == 0 {
		return false
	}
	for _, issue := range issues {
		for _, issuePath := range issue.Paths {
			if path == issuePath || strings.HasPrefix(path, issuePath+"/") {
				return true
			}
		}
	}
	return false
}

func windowsCaseFoldKey(path string) string {
	return strings.ToLower(path)
}
