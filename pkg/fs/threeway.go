package fs

// SyncAction represents what to do with a file during three-way sync.
type SyncActionType int

const (
	ActionUpload       SyncActionType = iota // local add/modify → push to S3
	ActionDownload                           // remote add/modify → pull from S3
	ActionDeleteLocal                        // remote deleted → remove local file
	ActionDeleteRemote                       // local deleted → write delete op to S3
	ActionConflict                           // both sides changed
)

// SyncAction is a single action to execute during sync.
type SyncAction struct {
	Type     SyncActionType
	Path     string
	Reason   string // human-readable explanation
	LocalSum string // local checksum (for uploads)
	RemoteOp *Op    // remote op that triggered this (for downloads/deletes)
}

// ThreeWayDiff computes sync actions by comparing three sources:
//   - localFiles: current local filesystem scan (path → checksum)
//   - manifest: last known agreed state (path → SyncedFile)
//   - remoteOps: new ops from S3 since manifest.LastRemoteOp
//
// Returns a list of actions to execute.
func ThreeWayDiff(localFiles map[string]string, manifest *DriveManifest, remoteOps []Op) []SyncAction {
	var actions []SyncAction

	// Build remote changes map from ops (latest op per path wins)
	remoteChanges := make(map[string]*Op)
	for i := range remoteOps {
		op := &remoteOps[i]
		remoteChanges[op.Path] = op
	}

	// Track all paths we need to consider
	allPaths := make(map[string]bool)
	for p := range localFiles {
		allPaths[p] = true
	}
	for p := range manifest.Files {
		allPaths[p] = true
	}
	for p := range remoteChanges {
		allPaths[p] = true
	}

	for path := range allPaths {
		localSum, inLocal := localFiles[path]
		_, inManifest := manifest.Files[path]
		manifestSum := ""
		if inManifest {
			manifestSum = manifest.Files[path].Checksum
		}
		remoteOp, inRemote := remoteChanges[path]

		localChanged := false
		localAdded := false
		localDeleted := false
		remoteChanged := false
		remoteAdded := false
		remoteDeleted := false

		// Determine local change
		if inLocal && !inManifest {
			localAdded = true
		} else if !inLocal && inManifest {
			localDeleted = true
		} else if inLocal && inManifest && localSum != manifestSum {
			localChanged = true
		}

		// Determine remote change
		if inRemote {
			if remoteOp.Type == OpDelete {
				remoteDeleted = true
			} else if inManifest {
				remoteChanged = true
			} else {
				remoteAdded = true
			}
		}

		// Apply merge rules
		switch {
		// No change on either side
		case !localAdded && !localChanged && !localDeleted && !remoteAdded && !remoteChanged && !remoteDeleted:
			continue

		// Only local changes
		case (localAdded || localChanged) && !remoteAdded && !remoteChanged && !remoteDeleted:
			actions = append(actions, SyncAction{
				Type:     ActionUpload,
				Path:     path,
				Reason:   localReason(localAdded),
				LocalSum: localSum,
			})

		case localDeleted && !remoteAdded && !remoteChanged && !remoteDeleted:
			actions = append(actions, SyncAction{
				Type:   ActionDeleteRemote,
				Path:   path,
				Reason: "deleted locally",
			})

		// Only remote changes
		case (remoteAdded || remoteChanged) && !localAdded && !localChanged && !localDeleted:
			actions = append(actions, SyncAction{
				Type:     ActionDownload,
				Path:     path,
				Reason:   remoteReason(remoteAdded),
				RemoteOp: remoteOp,
			})

		case remoteDeleted && !localAdded && !localChanged && !localDeleted:
			actions = append(actions, SyncAction{
				Type:     ActionDeleteLocal,
				Path:     path,
				Reason:   "deleted on remote",
				RemoteOp: remoteOp,
			})

		// Conflicts
		case (localAdded || localChanged) && (remoteAdded || remoteChanged):
			actions = append(actions, SyncAction{
				Type:     ActionConflict,
				Path:     path,
				Reason:   "modified on both sides",
				LocalSum: localSum,
				RemoteOp: remoteOp,
			})

		case localDeleted && (remoteAdded || remoteChanged):
			// Remote wins — re-download
			actions = append(actions, SyncAction{
				Type:     ActionDownload,
				Path:     path,
				Reason:   "deleted locally but modified on remote",
				RemoteOp: remoteOp,
			})

		case (localAdded || localChanged) && remoteDeleted:
			// Local wins — re-upload
			actions = append(actions, SyncAction{
				Type:     ActionUpload,
				Path:     path,
				Reason:   "modified locally but deleted on remote",
				LocalSum: localSum,
			})

		case localDeleted && remoteDeleted:
			// Both deleted — nothing to do, just clean up manifest
			continue
		}
	}

	return actions
}

func localReason(added bool) string {
	if added {
		return "added locally"
	}
	return "modified locally"
}

func remoteReason(added bool) string {
	if added {
		return "added on remote"
	}
	return "modified on remote"
}
