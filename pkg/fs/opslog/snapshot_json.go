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

// UnmarshalSnapshot deserializes a Snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*Snapshot, error) {
	var m snapshotJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing snapshot: %w", err)
	}

	snap := &Snapshot{
		files:   make(map[string]FileInfo, len(m.Tree)),
		dirs:    make(map[string]DirInfo, len(m.Dirs)),
		deleted: make(map[string]bool),
		created: m.Created,
		updated: m.Updated,
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

// snapshotJSON is the wire format, compatible with manifestJSON.
type snapshotJSON struct {
	Version int                     `json:"version"`
	Created time.Time               `json:"created"`
	Updated time.Time               `json:"updated"`
	Tree    map[string]fileInfoJSON `json:"tree"`
	Dirs    map[string]dirInfoJSON  `json:"dirs,omitempty"`
}
