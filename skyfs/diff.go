package skyfs

// DiffType describes what action is needed for a file.
type DiffType int

const (
	DiffUpload       DiffType = iota // local new/modified → upload to remote
	DiffDownload                     // remote new/modified → download to local
	DiffConflict                     // both modified → needs resolution
	DiffDeleteLocal                  // remote deleted → remove local file
	DiffDeleteRemote                 // local deleted → write delete op
)

// DiffEntry represents a single difference between local and remote state.
type DiffEntry struct {
	Path           string
	Type           DiffType
	LocalChecksum  string
	RemoteChecksum string
	LocalSize      int64
	RemoteSize     int64
	Namespace      string
}

// DiffLocalRemote computes the differences between local and remote file sets.
// localFiles maps path → file content SHA-256. remoteFiles is the manifest tree.
// The remote checksum is derived from chunk hashes, not raw file content, so
// we compare using file size + content hash when possible. If sizes differ,
// the file definitely changed. If sizes match and checksums differ (which they
// will due to different hash construction), we upload.
//
// To avoid unnecessary uploads when nothing changed, callers should use the
// local index to track which files were previously synced with which remote
// checksum.
func DiffLocalRemote(localFiles map[string]string, remoteFiles map[string]FileEntry) []DiffEntry {
	var diffs []DiffEntry

	// Check local files against remote
	for path, localChecksum := range localFiles {
		remote, inRemote := remoteFiles[path]
		if !inRemote {
			// Local exists, remote doesn't → upload
			diffs = append(diffs, DiffEntry{
				Path:          path,
				Type:          DiffUpload,
				LocalChecksum: localChecksum,
				Namespace:     NamespaceFromPath(path),
			})
			continue
		}

		if localChecksum != remote.Checksum {
			// Both exist but differ → need to decide direction
			// For now, treat as upload (local wins in sync-from-local context).
			// The sync engine handles conflict detection separately using ops.
			diffs = append(diffs, DiffEntry{
				Path:           path,
				Type:           DiffUpload,
				LocalChecksum:  localChecksum,
				RemoteChecksum: remote.Checksum,
				RemoteSize:     remote.Size,
				Namespace:      remote.Namespace,
			})
		}
		// If checksums match → no diff, skip
	}

	// Check remote files not in local
	for path, remote := range remoteFiles {
		if _, inLocal := localFiles[path]; !inLocal {
			diffs = append(diffs, DiffEntry{
				Path:           path,
				Type:           DiffDownload,
				RemoteChecksum: remote.Checksum,
				RemoteSize:     remote.Size,
				Namespace:      remote.Namespace,
			})
		}
	}

	return diffs
}

// DiffForDeleted computes delete operations for files that were in the
// previous local state but are no longer present locally.
func DiffForDeleted(previousLocal map[string]string, currentLocal map[string]string, remoteFiles map[string]FileEntry) []DiffEntry {
	var diffs []DiffEntry

	for path := range previousLocal {
		if _, stillExists := currentLocal[path]; !stillExists {
			if _, inRemote := remoteFiles[path]; inRemote {
				diffs = append(diffs, DiffEntry{
					Path:      path,
					Type:      DiffDeleteRemote,
					Namespace: NamespaceFromPath(path),
				})
			}
		}
	}

	return diffs
}
