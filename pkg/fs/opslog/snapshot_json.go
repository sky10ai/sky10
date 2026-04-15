package opslog

import (
	"encoding/json"
	"fmt"
	"time"
)

// MarshalSnapshot serializes a Snapshot to JSON. Only live files and
// directories are included — no tombstones. This is the format uploaded
// to S3 for snapshot exchange.
func MarshalSnapshot(snap *Snapshot) ([]byte, error) {
	if snap == nil {
		return json.Marshal(snapshotJSON{Version: 1, Tree: map[string]fileInfoJSON{}})
	}

	m := snapshotJSON{
		Version: 1,
		Created: snap.created,
		Updated: time.Now().UTC(),
		Tree:    make(map[string]fileInfoJSON, len(snap.files)),
	}

	for path, fi := range snap.files {
		// Skip chunkless puts with size>0 (upload still pending).
		// Empty files (size=0, chunks=nil) are valid and must be included.
		if fi.Chunks == nil && fi.LinkTarget == "" && fi.Size > 0 {
			continue
		}
		m.Tree[path] = fileInfoJSON{
			Chunks:     fi.Chunks,
			Size:       fi.Size,
			Modified:   fi.Modified,
			Checksum:   fi.Checksum,
			Namespace:  fi.Namespace,
			Device:     fi.Device,
			Seq:        fi.Seq,
			LinkTarget: fi.LinkTarget,
		}
	}

	if len(snap.dirs) > 0 {
		m.Dirs = make(map[string]dirInfoJSON, len(snap.dirs))
		for path, di := range snap.dirs {
			m.Dirs[path] = dirInfoJSON{
				Namespace: di.Namespace,
				Device:    di.Device,
				Seq:       di.Seq,
				Modified:  di.Modified,
			}
		}
	}

	return json.Marshal(m)
}

// MarshalPeerSnapshot serializes a Snapshot for peer metadata exchange.
// Unlike MarshalSnapshot, it includes tombstones so deletes retain their
// causal clocks across reconnects and compaction.
func MarshalPeerSnapshot(snap *Snapshot) ([]byte, error) {
	if snap == nil {
		return json.Marshal(peerSnapshotJSON{Version: 2, Tree: map[string]fileInfoJSON{}})
	}

	m := peerSnapshotJSON{
		Version: 2,
		Created: snap.created,
		Updated: time.Now().UTC(),
		Tree:    make(map[string]fileInfoJSON, len(snap.files)),
	}

	for path, fi := range snap.files {
		if fi.Chunks == nil && fi.LinkTarget == "" && fi.Size > 0 {
			continue
		}
		m.Tree[path] = fileInfoJSON{
			Chunks:     fi.Chunks,
			Size:       fi.Size,
			Modified:   fi.Modified,
			Checksum:   fi.Checksum,
			Namespace:  fi.Namespace,
			Device:     fi.Device,
			Seq:        fi.Seq,
			LinkTarget: fi.LinkTarget,
		}
	}

	if len(snap.dirs) > 0 {
		m.Dirs = make(map[string]dirInfoJSON, len(snap.dirs))
		for path, di := range snap.dirs {
			m.Dirs[path] = dirInfoJSON{
				Namespace: di.Namespace,
				Device:    di.Device,
				Seq:       di.Seq,
				Modified:  di.Modified,
			}
		}
	}
	if len(snap.deleted) > 0 {
		m.Deleted = make(map[string]tombstoneJSON, len(snap.deleted))
		for path, tomb := range snap.deleted {
			m.Deleted[path] = tombstoneJSON{
				Namespace: tomb.Namespace,
				Device:    tomb.Device,
				Seq:       tomb.Seq,
				Modified:  tomb.Modified,
			}
		}
	}
	if len(snap.deletedDirs) > 0 {
		m.DeletedDirs = make(map[string]tombstoneJSON, len(snap.deletedDirs))
		for path, tomb := range snap.deletedDirs {
			m.DeletedDirs[path] = tombstoneJSON{
				Namespace: tomb.Namespace,
				Device:    tomb.Device,
				Seq:       tomb.Seq,
				Modified:  tomb.Modified,
			}
		}
	}

	return json.Marshal(m)
}

// UnmarshalSnapshot deserializes a Snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*Snapshot, error) {
	var m snapshotJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing snapshot: %w", err)
	}

	snap := &Snapshot{
		files:       make(map[string]FileInfo, len(m.Tree)),
		dirs:        make(map[string]DirInfo, len(m.Dirs)),
		deleted:     make(map[string]TombstoneInfo),
		deletedDirs: make(map[string]TombstoneInfo),
		created:     m.Created,
		updated:     m.Updated,
	}

	for path, fi := range m.Tree {
		snap.files[path] = FileInfo{
			Chunks:     fi.Chunks,
			Size:       fi.Size,
			Modified:   fi.Modified,
			Checksum:   fi.Checksum,
			Namespace:  fi.Namespace,
			Device:     fi.Device,
			Seq:        fi.Seq,
			LinkTarget: fi.LinkTarget,
		}
	}

	for path, di := range m.Dirs {
		snap.dirs[path] = DirInfo{
			Namespace: di.Namespace,
			Device:    di.Device,
			Seq:       di.Seq,
			Modified:  di.Modified,
		}
	}

	return snap, nil
}

// UnmarshalPeerSnapshot deserializes a peer snapshot JSON payload,
// including file and directory tombstones.
func UnmarshalPeerSnapshot(data []byte) (*Snapshot, error) {
	var m peerSnapshotJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing peer snapshot: %w", err)
	}

	snap := &Snapshot{
		files:       make(map[string]FileInfo, len(m.Tree)),
		dirs:        make(map[string]DirInfo, len(m.Dirs)),
		deleted:     make(map[string]TombstoneInfo, len(m.Deleted)),
		deletedDirs: make(map[string]TombstoneInfo, len(m.DeletedDirs)),
		created:     m.Created,
		updated:     m.Updated,
	}

	for path, fi := range m.Tree {
		snap.files[path] = FileInfo{
			Chunks:     fi.Chunks,
			Size:       fi.Size,
			Modified:   fi.Modified,
			Checksum:   fi.Checksum,
			Namespace:  fi.Namespace,
			Device:     fi.Device,
			Seq:        fi.Seq,
			LinkTarget: fi.LinkTarget,
		}
	}
	for path, di := range m.Dirs {
		snap.dirs[path] = DirInfo{
			Namespace: di.Namespace,
			Device:    di.Device,
			Seq:       di.Seq,
			Modified:  di.Modified,
		}
	}
	for path, tomb := range m.Deleted {
		snap.deleted[path] = TombstoneInfo{
			Namespace: tomb.Namespace,
			Device:    tomb.Device,
			Seq:       tomb.Seq,
			Modified:  tomb.Modified,
		}
	}
	for path, tomb := range m.DeletedDirs {
		snap.deletedDirs[path] = TombstoneInfo{
			Namespace: tomb.Namespace,
			Device:    tomb.Device,
			Seq:       tomb.Seq,
			Modified:  tomb.Modified,
		}
	}

	return snap, nil
}

// snapshotJSON is the wire format, compatible with manifestJSON.
type snapshotJSON struct {
	Version int                     `json:"version"`
	Created time.Time               `json:"created"`
	Updated time.Time               `json:"updated"`
	Tree    map[string]fileInfoJSON `json:"tree"`
	Dirs    map[string]dirInfoJSON  `json:"dirs,omitempty"`
}

type peerSnapshotJSON struct {
	Version     int                      `json:"version"`
	Created     time.Time                `json:"created"`
	Updated     time.Time                `json:"updated"`
	Tree        map[string]fileInfoJSON  `json:"tree"`
	Dirs        map[string]dirInfoJSON   `json:"dirs,omitempty"`
	Deleted     map[string]tombstoneJSON `json:"deleted,omitempty"`
	DeletedDirs map[string]tombstoneJSON `json:"deleted_dirs,omitempty"`
}
