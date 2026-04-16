package fs

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

const conflictCopyMarker = ".conflict-"

// conflictCopyPath generates a conflict copy filename.
// "docs/notes.md" -> "docs/notes.conflict-dev123-1711700000.md"
func conflictCopyPath(path, device string, ts int64) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s%s%s-%d%s", base, conflictCopyMarker, device, ts, ext)
}

func isConflictCopyPath(path string) bool {
	return strings.Contains(filepath.Base(path), conflictCopyMarker)
}

func snapshotConflictPaths(snap *opslog.Snapshot) []string {
	if snap == nil {
		return nil
	}
	conflicts := make([]string, 0)
	for path := range snap.Files() {
		if isConflictCopyPath(path) {
			conflicts = append(conflicts, path)
		}
	}
	sort.Strings(conflicts)
	return conflicts
}
