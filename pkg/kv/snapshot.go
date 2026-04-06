package kv

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SnapshotSummary is a compact causal summary used for anti-entropy.
type SnapshotSummary struct {
	Vector     VersionVector `json:"vector,omitempty"`
	Keys       int           `json:"keys"`
	Tombstones int           `json:"tombstones"`
}

// Snapshot is an immutable point-in-time view of the KV store.
// Produced by replaying log entries on top of an optional base.
type Snapshot struct {
	entries map[string]ValueInfo
	deleted map[string]TombstoneInfo
	vector  VersionVector
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
		v.Context = v.Context.Clone()
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

// Tombstones returns a copy of the tombstone map. Safe to mutate.
func (s *Snapshot) Tombstones() map[string]TombstoneInfo {
	cp := make(map[string]TombstoneInfo, len(s.deleted))
	for k, v := range s.deleted {
		v.Context = v.Context.Clone()
		cp[k] = v
	}
	return cp
}

// Vector returns a copy of the snapshot's causal summary.
func (s *Snapshot) Vector() VersionVector {
	return s.vector.Clone()
}

// Summary returns a compact description of the snapshot's causal state.
func (s *Snapshot) Summary() SnapshotSummary {
	if s == nil {
		return SnapshotSummary{}
	}
	return SnapshotSummary{
		Vector:     s.vector.Clone(),
		Keys:       len(s.entries),
		Tombstones: len(s.deleted),
	}
}

// HasState reports whether the snapshot carries any live or deleted key state.
func (s *Snapshot) HasState() bool {
	return s != nil && (len(s.entries) > 0 || len(s.deleted) > 0)
}

// DeltaSince returns the subset of state not already covered by the provided
// causal summary. The returned snapshot still carries the full local vector.
func (s *Snapshot) DeltaSince(seen VersionVector) *Snapshot {
	if s == nil {
		return &Snapshot{
			entries: make(map[string]ValueInfo),
			deleted: make(map[string]TombstoneInfo),
			vector:  make(VersionVector),
		}
	}

	delta := &Snapshot{
		entries: make(map[string]ValueInfo),
		deleted: make(map[string]TombstoneInfo),
		vector:  s.vector.Clone(),
		created: s.created,
		updated: s.updated,
	}
	if delta.vector == nil {
		delta.vector = make(VersionVector)
	}

	for key, vi := range s.entries {
		if seen.Dominates(CausalVersion(
			effectiveActor(vi.Actor, vi.Device),
			effectiveCounter(vi.Counter, vi.Seq),
			vi.Context,
		)) {
			continue
		}
		vi.Context = vi.Context.Clone()
		delta.entries[key] = vi
	}
	for key, tomb := range s.deleted {
		if seen.Dominates(CausalVersion(
			effectiveActor(tomb.Actor, tomb.Device),
			effectiveCounter(tomb.Counter, tomb.Seq),
			tomb.Context,
		)) {
			continue
		}
		tomb.Context = tomb.Context.Clone()
		delta.deleted[key] = tomb
	}

	return delta
}

// buildSnapshot materializes KV state from a base snapshot and new entries
// using LWW-Register-Map semantics. For each key, the entry with the
// highest clock wins. Processing order is irrelevant (commutative).
func buildSnapshot(base *Snapshot, entries []Entry) *Snapshot {
	snap := &Snapshot{
		entries: make(map[string]ValueInfo),
		deleted: make(map[string]TombstoneInfo),
		vector:  make(VersionVector),
		created: time.Now().UTC(),
		updated: time.Now().UTC(),
	}

	if base != nil {
		snap.created = base.created
		for k, v := range base.entries {
			v.Context = v.Context.Clone()
			snap.entries[k] = v
		}
		for k, v := range base.deleted {
			v.Context = v.Context.Clone()
			snap.deleted[k] = v
		}
		snap.vector = base.vector.Clone()
		if snap.vector == nil {
			snap.vector = make(VersionVector)
		}
	}

	for _, e := range entries {
		e = normalizeEntry(e)
		snap.vector.Merge(e.Context)
		snap.vector.Observe(e.Actor, e.Counter)

		switch e.Type {
		case Set:
			incoming := ValueInfo{
				Value:    e.Value,
				Modified: time.Unix(e.Timestamp, 0).UTC(),
				Device:   e.Device,
				Seq:      e.Seq,
				Actor:    e.Actor,
				Counter:  e.Counter,
				Context:  e.Context.Clone(),
			}
			if prev, ok := snap.entries[e.Key]; ok && !valueInfoBeats(incoming, prev) {
				continue
			}
			if tomb, ok := snap.deleted[e.Key]; ok && !valueBeatsTombstone(incoming, tomb) {
				continue
			}
			delete(snap.deleted, e.Key)
			snap.entries[e.Key] = incoming
		case Delete:
			incoming := TombstoneInfo{
				Modified: time.Unix(e.Timestamp, 0).UTC(),
				Device:   e.Device,
				Seq:      e.Seq,
				Actor:    e.Actor,
				Counter:  e.Counter,
				Context:  e.Context.Clone(),
			}
			if prev, ok := snap.entries[e.Key]; ok && !tombstoneBeatsValue(incoming, prev) {
				continue
			}
			if prev, ok := snap.deleted[e.Key]; ok && !tombstoneInfoBeats(incoming, prev) {
				continue
			}
			delete(snap.entries, e.Key)
			snap.deleted[e.Key] = incoming
		}
	}

	snap.updated = time.Now().UTC()
	return snap
}

// --- Serialization ---

// MarshalSnapshot serializes a Snapshot to JSON for sync.
func MarshalSnapshot(snap *Snapshot) ([]byte, error) {
	if snap == nil {
		return json.Marshal(snapshotJSON{Version: 2, Entries: map[string]valueInfoJSON{}})
	}

	m := snapshotJSON{
		Version: 2,
		Created: snap.created,
		Updated: time.Now().UTC(),
		Entries: make(map[string]valueInfoJSON, len(snap.entries)),
		Vector:  snap.vector.Clone(),
	}

	for key, vi := range snap.entries {
		m.Entries[key] = valueInfoJSON{
			Value:    vi.Value,
			Modified: vi.Modified,
			Device:   vi.Device,
			Seq:      vi.Seq,
			Actor:    vi.Actor,
			Counter:  vi.Counter,
			Context:  vi.Context.Clone(),
		}
	}

	if len(snap.deleted) > 0 {
		m.Tombstones = make(map[string]tombstoneInfoJSON, len(snap.deleted))
		for key, tomb := range snap.deleted {
			m.Tombstones[key] = tombstoneInfoJSON{
				Modified: tomb.Modified,
				Device:   tomb.Device,
				Seq:      tomb.Seq,
				Actor:    tomb.Actor,
				Counter:  tomb.Counter,
				Context:  tomb.Context.Clone(),
			}
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
		deleted: make(map[string]TombstoneInfo, len(m.Tombstones)),
		vector:  m.Vector.Clone(),
		created: m.Created,
		updated: m.Updated,
	}
	if snap.vector == nil {
		snap.vector = make(VersionVector)
	}

	for key, vi := range m.Entries {
		snap.entries[key] = ValueInfo{
			Value:    vi.Value,
			Modified: vi.Modified,
			Device:   vi.Device,
			Seq:      vi.Seq,
			Actor:    vi.Actor,
			Counter:  vi.Counter,
			Context:  vi.Context.Clone(),
		}
		snap.vector.Merge(vi.Context)
		snap.vector.Observe(effectiveActor(vi.Actor, vi.Device), effectiveCounter(vi.Counter, vi.Seq))
	}

	for key, tomb := range m.Tombstones {
		snap.deleted[key] = TombstoneInfo{
			Modified: tomb.Modified,
			Device:   tomb.Device,
			Seq:      tomb.Seq,
			Actor:    tomb.Actor,
			Counter:  tomb.Counter,
			Context:  tomb.Context.Clone(),
		}
		snap.vector.Merge(tomb.Context)
		snap.vector.Observe(effectiveActor(tomb.Actor, tomb.Device), effectiveCounter(tomb.Counter, tomb.Seq))
	}

	return snap, nil
}

// snapshotJSON is the wire format for KV snapshots.
type snapshotJSON struct {
	Version    int                          `json:"version"`
	Created    time.Time                    `json:"created"`
	Updated    time.Time                    `json:"updated"`
	Entries    map[string]valueInfoJSON     `json:"entries"`
	Tombstones map[string]tombstoneInfoJSON `json:"tombstones,omitempty"`
	Vector     VersionVector                `json:"vector,omitempty"`
}

type valueInfoJSON struct {
	Value    []byte        `json:"value"`
	Modified time.Time     `json:"modified"`
	Device   string        `json:"device,omitempty"`
	Seq      int           `json:"seq,omitempty"`
	Actor    string        `json:"actor,omitempty"`
	Counter  uint64        `json:"counter,omitempty"`
	Context  VersionVector `json:"context,omitempty"`
}

type tombstoneInfoJSON struct {
	Modified time.Time     `json:"modified"`
	Device   string        `json:"device,omitempty"`
	Seq      int           `json:"seq,omitempty"`
	Actor    string        `json:"actor,omitempty"`
	Counter  uint64        `json:"counter,omitempty"`
	Context  VersionVector `json:"context,omitempty"`
}

func normalizeEntry(e Entry) Entry {
	e.Actor = effectiveActor(e.Actor, e.Device)
	e.Counter = effectiveCounter(e.Counter, e.Seq)
	e.Context = e.Context.Clone()
	return e
}

func effectiveActor(actor, device string) string {
	if actor != "" {
		return actor
	}
	return device
}

func effectiveCounter(counter uint64, seq int) uint64 {
	if counter != 0 {
		return counter
	}
	if seq > 0 {
		return uint64(seq)
	}
	return 0
}

func clockCompare(a, b clockTuple) int {
	switch {
	case a.beats(b):
		return 1
	case b.beats(a):
		return -1
	default:
		return 0
	}
}

func valueClock(v ValueInfo) clockTuple {
	return clockTuple{ts: v.Modified.Unix(), device: v.Device, seq: v.Seq}
}

func tombstoneClock(v TombstoneInfo) clockTuple {
	return clockTuple{ts: v.Modified.Unix(), device: v.Device, seq: v.Seq}
}

func valueInfoBeats(a, b ValueInfo) bool {
	if causal := compareCausal(
		effectiveActor(a.Actor, a.Device), effectiveCounter(a.Counter, a.Seq), a.Context,
		effectiveActor(b.Actor, b.Device), effectiveCounter(b.Counter, b.Seq), b.Context,
	); causal != 0 {
		return causal > 0
	}
	return clockCompare(valueClock(a), valueClock(b)) > 0
}

func valueBeatsTombstone(a ValueInfo, b TombstoneInfo) bool {
	if causal := compareCausal(
		effectiveActor(a.Actor, a.Device), effectiveCounter(a.Counter, a.Seq), a.Context,
		effectiveActor(b.Actor, b.Device), effectiveCounter(b.Counter, b.Seq), b.Context,
	); causal != 0 {
		return causal > 0
	}
	return clockCompare(valueClock(a), tombstoneClock(b)) > 0
}

func tombstoneBeatsValue(a TombstoneInfo, b ValueInfo) bool {
	if causal := compareCausal(
		effectiveActor(a.Actor, a.Device), effectiveCounter(a.Counter, a.Seq), a.Context,
		effectiveActor(b.Actor, b.Device), effectiveCounter(b.Counter, b.Seq), b.Context,
	); causal != 0 {
		return causal > 0
	}
	return clockCompare(tombstoneClock(a), valueClock(b)) > 0
}

func tombstoneInfoBeats(a, b TombstoneInfo) bool {
	if causal := compareCausal(
		effectiveActor(a.Actor, a.Device), effectiveCounter(a.Counter, a.Seq), a.Context,
		effectiveActor(b.Actor, b.Device), effectiveCounter(b.Counter, b.Seq), b.Context,
	); causal != 0 {
		return causal > 0
	}
	return clockCompare(tombstoneClock(a), tombstoneClock(b)) > 0
}
