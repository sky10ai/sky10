package kv

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Snapshot is an immutable point-in-time view of the KV store.
// Produced by replaying log entries on top of an optional base.
type Snapshot struct {
	entries map[string]ValueInfo
	deleted map[string]bool // keys with an explicit delete as last op
	created time.Time
	updated time.Time
}

// Lookup returns the value for a key, or false if not present.
func (s *Snapshot) Lookup(key string) (ValueInfo, bool) {
	vi, ok := s.entries[key]
	return vi, ok
}

// Keys returns all keys in sorted order.
func (s *Snapshot) Keys() []string {
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// KeysWithPrefix returns all keys with the given prefix, sorted.
func (s *Snapshot) KeysWithPrefix(prefix string) []string {
	var keys []string
	for k := range s.entries {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Len returns the number of live entries.
func (s *Snapshot) Len() int { return len(s.entries) }

// Entries returns a copy of the entries map. Safe to mutate.
func (s *Snapshot) Entries() map[string]ValueInfo {
	cp := make(map[string]ValueInfo, len(s.entries))
	for k, v := range s.entries {
		cp[k] = v
	}
	return cp
}

// DeletedKeys returns the set of keys that have an explicit delete as
// their last op.
func (s *Snapshot) DeletedKeys() map[string]bool {
	cp := make(map[string]bool, len(s.deleted))
	for k := range s.deleted {
		cp[k] = true
	}
	return cp
}

// buildSnapshot materializes KV state from a base snapshot and new entries
// using LWW-Register-Map semantics. For each key, the entry with the
// highest clock wins. Processing order is irrelevant (commutative).
func buildSnapshot(base *Snapshot, entries []Entry) *Snapshot {
	snap := &Snapshot{
		entries: make(map[string]ValueInfo),
		deleted: make(map[string]bool),
		created: time.Now().UTC(),
		updated: time.Now().UTC(),
	}

	// Per-key clock tracking. Entries (including deletes) must beat
	// the existing clock to take effect.
	clocks := make(map[string]clockTuple)

	if base != nil {
		snap.created = base.created
		for k, v := range base.entries {
			snap.entries[k] = v
			clocks[k] = clockTuple{ts: v.Modified.Unix(), device: v.Device, seq: v.Seq}
		}
		for k := range base.deleted {
			snap.deleted[k] = true
		}
	}

	for _, e := range entries {
		ec := clockTuple{ts: e.Timestamp, device: e.Device, seq: e.Seq}

		switch e.Type {
		case Set:
			if prev, ok := clocks[e.Key]; ok && !ec.beats(prev) {
				continue
			}
			clocks[e.Key] = ec
			delete(snap.deleted, e.Key)
			snap.entries[e.Key] = ValueInfo{
				Value:    e.Value,
				Modified: time.Unix(e.Timestamp, 0).UTC(),
				Device:   e.Device,
				Seq:      e.Seq,
			}
		case Delete:
			if prev, ok := clocks[e.Key]; ok && !ec.beats(prev) {
				continue
			}
			clocks[e.Key] = ec
			delete(snap.entries, e.Key)
			snap.deleted[e.Key] = true
		}
	}

	snap.updated = time.Now().UTC()
	return snap
}

// --- Serialization ---

// MarshalSnapshot serializes a Snapshot to JSON for S3 upload.
// Only live entries are included — no tombstones.
func MarshalSnapshot(snap *Snapshot) ([]byte, error) {
	if snap == nil {
		return json.Marshal(snapshotJSON{Version: 1, Entries: map[string]valueInfoJSON{}})
	}

	m := snapshotJSON{
		Version: 1,
		Created: snap.created,
		Updated: time.Now().UTC(),
		Entries: make(map[string]valueInfoJSON, len(snap.entries)),
	}

	for key, vi := range snap.entries {
		m.Entries[key] = valueInfoJSON{
			Value:    vi.Value,
			Modified: vi.Modified,
			Device:   vi.Device,
			Seq:      vi.Seq,
		}
	}

	return json.Marshal(m)
}

// UnmarshalSnapshot deserializes a Snapshot from JSON.
func UnmarshalSnapshot(data []byte) (*Snapshot, error) {
	var m snapshotJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing kv snapshot: %w", err)
	}

	snap := &Snapshot{
		entries: make(map[string]ValueInfo, len(m.Entries)),
		deleted: make(map[string]bool),
		created: m.Created,
		updated: m.Updated,
	}

	for key, vi := range m.Entries {
		snap.entries[key] = ValueInfo{
			Value:    vi.Value,
			Modified: vi.Modified,
			Device:   vi.Device,
			Seq:      vi.Seq,
		}
	}

	return snap, nil
}

// snapshotJSON is the wire format for KV snapshots.
type snapshotJSON struct {
	Version int                      `json:"version"`
	Created time.Time                `json:"created"`
	Updated time.Time                `json:"updated"`
	Entries map[string]valueInfoJSON `json:"entries"`
}

type valueInfoJSON struct {
	Value    []byte    `json:"value"`
	Modified time.Time `json:"modified"`
	Device   string    `json:"device,omitempty"`
	Seq      int       `json:"seq,omitempty"`
}
