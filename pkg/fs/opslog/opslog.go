// Package opslog provides an append-only encrypted operations log backed
// by a storage backend. It is the single source of truth for file state
// in skyfs: every put and delete is an entry in the log, and the current
// file tree is the result of replaying all entries.
//
// The log is encrypted at rest. Each entry is wrapped in an OpEnvelope
// that carries schema version and device metadata in plaintext so
// compatibility can be checked without decryption.
//
// Snapshots are periodic materializations of the full state, stored
// alongside the log. They accelerate Snapshot() by allowing the log to
// be replayed from a recent checkpoint instead of from the beginning.
package opslog

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

// SchemaVersion must match the schema major version written in ops.
// Ops with a higher major version are rejected.
const SchemaVersion = "1.1.0"

// EntryType is the kind of operation.
type EntryType string

const (
	Put       EntryType = "put"
	Delete    EntryType = "delete"
	DeleteDir EntryType = "delete_dir"
	CreateDir EntryType = "create_dir"
	Symlink   EntryType = "symlink"
)

// Entry is a single operation in the append-only log.
// JSON tags are wire-compatible with the existing ops/ format on S3.
type Entry struct {
	Type         EntryType `json:"op"`
	Path         string    `json:"path"`
	Chunks       []string  `json:"chunks,omitempty"`
	Size         int64     `json:"size,omitempty"`
	Checksum     string    `json:"checksum,omitempty"`
	PrevChecksum string    `json:"prev_checksum,omitempty"`
	LinkTarget   string    `json:"link_target,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Device       string    `json:"device"`
	Timestamp    int64     `json:"timestamp"`
	Seq          int       `json:"seq"`
	Client       string    `json:"client,omitempty"`
}

// entryKey returns the S3 key for this entry.
// Format: ops/{timestamp}-{device}-{seq}.enc
func (e *Entry) entryKey() string {
	return fmt.Sprintf("ops/%d-%s-%04d.enc", e.Timestamp, e.Device, e.Seq)
}

// FileInfo describes a file at a point in time.
// Device and Seq preserve the LWW clock through snapshot save/load cycles.
// When LinkTarget is non-empty, this entry represents a symlink — Chunks
// will be nil and the target path is stored instead of content.
type FileInfo struct {
	Chunks       []string  `json:"chunks"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
	Checksum     string    `json:"checksum"`
	PrevChecksum string    `json:"prev_checksum,omitempty"`
	Namespace    string    `json:"namespace"`
	Device       string    `json:"device,omitempty"`
	Seq          int       `json:"seq,omitempty"`
	LinkTarget   string    `json:"link_target,omitempty"`
}

// clockTuple is the comparison key for LWW-Register-Map conflict resolution.
// For each path, the entry with the highest clock wins.
// Comparison order: timestamp, then device ID (lexicographic), then seq.
type clockTuple struct {
	ts     int64
	device string
	seq    int
}

// beats returns true if c is strictly greater than other.
func (c clockTuple) beats(other clockTuple) bool {
	if c.ts != other.ts {
		return c.ts > other.ts
	}
	if c.device != other.device {
		return c.device > other.device
	}
	return c.seq > other.seq
}

// Clock represents the LWW clock for a file entry.
type Clock struct {
	Ts     int64
	Device string
	Seq    int
}

// Beats returns true if c is strictly greater than other.
func (c Clock) Beats(other Clock) bool {
	if c.Ts != other.Ts {
		return c.Ts > other.Ts
	}
	if c.Device != other.Device {
		return c.Device > other.Device
	}
	return c.Seq > other.Seq
}

// ClockTuple extracts the LWW clock from a FileInfo.
func ClockTuple(fi FileInfo) Clock {
	return Clock{Ts: fi.Modified.Unix(), Device: fi.Device, Seq: fi.Seq}
}

func entryDescendsFile(e Entry, fi FileInfo) bool {
	return e.PrevChecksum != "" && fi.Checksum != "" && e.PrevChecksum == fi.Checksum
}

func fileDescendsEntry(fi FileInfo, e Entry) bool {
	return fi.PrevChecksum != "" && e.Checksum != "" && fi.PrevChecksum == e.Checksum
}

func tombstoneDescendsEntry(t TombstoneInfo, e Entry) bool {
	return t.PrevChecksum != "" && e.Checksum != "" && t.PrevChecksum == e.Checksum
}

// DirInfo describes an explicitly created directory.
// Device and Seq preserve the LWW clock through snapshot save/load cycles.
type DirInfo struct {
	Namespace string    `json:"namespace,omitempty"`
	Device    string    `json:"device,omitempty"`
	Seq       int       `json:"seq,omitempty"`
	Modified  time.Time `json:"modified"`
}

// TombstoneInfo preserves the delete clock for a path or directory
// through snapshot save/load cycles and compaction.
type TombstoneInfo struct {
	Namespace    string    `json:"namespace,omitempty"`
	Device       string    `json:"device,omitempty"`
	Seq          int       `json:"seq,omitempty"`
	Modified     time.Time `json:"modified"`
	PrevChecksum string    `json:"prev_checksum,omitempty"`
}

// Snapshot is an immutable point-in-time view of the file tree.
// It is produced by replaying log entries on top of an optional base.
type Snapshot struct {
	files       map[string]FileInfo
	dirs        map[string]DirInfo       // explicitly created directories
	deleted     map[string]TombstoneInfo // paths with an explicit delete as last op
	deletedDirs map[string]TombstoneInfo // directories with an explicit delete_dir tombstone
	created     time.Time
	updated     time.Time
}

// Lookup returns the FileInfo for path, or false if not present.
func (s *Snapshot) Lookup(path string) (FileInfo, bool) {
	fi, ok := s.files[path]
	return fi, ok
}

// Paths returns all file paths in sorted order.
func (s *Snapshot) Paths() []string {
	paths := make([]string, 0, len(s.files))
	for p := range s.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// Len returns the number of files.
func (s *Snapshot) Len() int { return len(s.files) }

// Files returns a copy of the file map. Safe to mutate.
func (s *Snapshot) Files() map[string]FileInfo {
	cp := make(map[string]FileInfo, len(s.files))
	for k, v := range s.files {
		cp[k] = v
	}
	return cp
}

// Dirs returns a copy of the explicitly created directories map.
func (s *Snapshot) Dirs() map[string]DirInfo {
	cp := make(map[string]DirInfo, len(s.dirs))
	for k, v := range s.dirs {
		cp[k] = v
	}
	return cp
}

// DeletedFiles returns the set of paths that have an explicit delete
// as their last op. Used by the reconciler to distinguish "never tracked"
// (don't delete) from "explicitly deleted by remote" (do delete).
func (s *Snapshot) DeletedFiles() map[string]bool {
	cp := make(map[string]bool, len(s.deleted))
	for k := range s.deleted {
		cp[k] = true
	}
	return cp
}

// Tombstones returns a copy of the file tombstone map. Safe to mutate.
func (s *Snapshot) Tombstones() map[string]TombstoneInfo {
	cp := make(map[string]TombstoneInfo, len(s.deleted))
	for k, v := range s.deleted {
		cp[k] = v
	}
	return cp
}

// DeletedDirs returns the set of directories that have an explicit
// delete_dir tombstone as their last op.
func (s *Snapshot) DeletedDirs() map[string]bool {
	cp := make(map[string]bool, len(s.deletedDirs))
	for k := range s.deletedDirs {
		cp[k] = true
	}
	return cp
}

// DirTombstones returns a copy of the directory tombstone map. Safe to mutate.
func (s *Snapshot) DirTombstones() map[string]TombstoneInfo {
	cp := make(map[string]TombstoneInfo, len(s.deletedDirs))
	for k, v := range s.deletedDirs {
		cp[k] = v
	}
	return cp
}

// Created returns when the earliest snapshot base was created.
func (s *Snapshot) Created() time.Time { return s.created }

// Updated returns when this snapshot was materialized.
func (s *Snapshot) Updated() time.Time { return s.updated }

// CompactResult contains stats from a compaction run.
type CompactResult struct {
	OpsCompacted     int
	OpsDeleted       int
	SnapshotsKept    int
	SnapshotsDeleted int
}

// OpsLog is an append-only encrypted operations log backed by a storage
// backend. It provides the core abstraction for skyfs state: append entries,
// read entries, and materialize snapshots.
type OpsLog struct {
	backend  adapter.Backend
	encKey   []byte
	deviceID string
	clientID string
	now      func() time.Time // clock function, defaults to time.Now

	mu    sync.Mutex
	seq   int       // per-session sequence counter
	cache *Snapshot // cached result of Snapshot()
}

// New creates a new OpsLog.
func New(backend adapter.Backend, encKey []byte, deviceID, clientID string) *OpsLog {
	return &OpsLog{
		backend:  backend,
		encKey:   encKey,
		deviceID: deviceID,
		clientID: clientID,
		now:      time.Now,
	}
}

// Append encrypts and writes an entry to the log.
// It sets the Device, Timestamp, Seq, and Client fields automatically.
func (l *OpsLog) Append(ctx context.Context, e *Entry) error {
	l.mu.Lock()
	l.seq++
	e.Seq = l.seq
	l.cache = nil // invalidate
	l.mu.Unlock()

	e.Device = l.deviceID
	e.Timestamp = l.now().Unix()
	e.Client = l.clientID

	return writeEntry(ctx, l.backend, e, l.encKey)
}

// ReadSince returns all entries with timestamps strictly after since,
// sorted by (timestamp, device, seq) for deterministic replay.
func (l *OpsLog) ReadSince(ctx context.Context, since int64) ([]Entry, error) {
	return readEntries(ctx, l.backend, since, l.encKey)
}

// ReadBatched lists ops after since and calls fn with batches of up to
// batchSize entries. The fn callback is invoked after each batch is
// downloaded and decrypted, allowing callers to process ops incrementally
// instead of waiting for the full set. Returns the total number of entries
// processed.
func (l *OpsLog) ReadBatched(ctx context.Context, since int64, batchSize int, fn func([]Entry)) (int, error) {
	keys, err := l.backend.List(ctx, "ops/")
	if err != nil {
		return 0, fmt.Errorf("listing ops: %w", err)
	}

	// Filter and sort keys by timestamp
	var filtered []string
	for _, key := range keys {
		ts := parseEntryTimestamp(key)
		if ts >= since {
			filtered = append(filtered, key)
		}
	}
	sort.Strings(filtered)

	// Process in chunks of batchSize, fetching ops within each chunk
	// in parallel (up to 10 concurrent downloads).
	total := 0
	for i := 0; i < len(filtered); i += batchSize {
		if ctx.Err() != nil {
			break
		}

		end := i + batchSize
		if end > len(filtered) {
			end = len(filtered)
		}
		chunk := filtered[i:end]

		// Fetch all ops in this chunk in parallel.
		type result struct {
			idx   int
			entry Entry
			err   error
		}
		results := make([]result, len(chunk))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 10)

		for j, key := range chunk {
			wg.Add(1)
			sem <- struct{}{}
			go func(j int, key string) {
				defer wg.Done()
				defer func() { <-sem }()

				rc, err := l.backend.Get(ctx, key)
				if err != nil {
					results[j] = result{idx: j, err: fmt.Errorf("downloading %s: %w", key, err)}
					return
				}
				raw, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					results[j] = result{idx: j, err: fmt.Errorf("reading %s: %w", key, err)}
					return
				}

				encrypted, schemaVer, isNew := parseEnvelope(raw)
				if isNew {
					codeMajor := semverMajor(SchemaVersion)
					if int(schemaVer[0]) > codeMajor {
						results[j] = result{idx: j, err: fmt.Errorf("entry %s requires v%d.x (have v%s) — upgrade",
							key, schemaVer[0], SchemaVersion)}
						return
					}
				}

				data, err := skykey.Decrypt(encrypted, l.encKey)
				if err != nil {
					results[j] = result{idx: j, err: fmt.Errorf("decrypting %s: %w", key, err)}
					return
				}

				var e Entry
				if err := json.Unmarshal(data, &e); err != nil {
					results[j] = result{idx: j, err: fmt.Errorf("parsing %s: %w", key, err)}
					return
				}
				results[j] = result{idx: j, entry: e}
			}(j, key)
		}
		wg.Wait()

		// Collect entries in order, fail on first error.
		batch := make([]Entry, 0, len(chunk))
		for _, r := range results {
			if r.err != nil {
				return total, r.err
			}
			batch = append(batch, r.entry)
		}

		sortEntries(batch)
		fn(batch)
		total += len(batch)
	}

	return total, nil
}

// Snapshot returns the current state by loading the latest stored snapshot
// and replaying any entries written after it. The result is cached until
// the next Append or InvalidateCache call.
func (l *OpsLog) Snapshot(ctx context.Context) (*Snapshot, error) {
	l.mu.Lock()
	if l.cache != nil {
		cached := l.cache
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	base, baseTS, err := l.LoadLatestSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading snapshot: %w", err)
	}

	entries, err := readEntries(ctx, l.backend, baseTS, l.encKey)
	if err != nil {
		return nil, fmt.Errorf("reading entries: %w", err)
	}

	snap := buildSnapshot(base, entries)

	l.mu.Lock()
	l.cache = snap
	l.mu.Unlock()
	return snap, nil
}

// Compact saves a new snapshot from the current state, deletes all ops
// (now captured in the snapshot), and prunes old snapshots keeping the
// last maxSnapshots.
//
// Compaction is idempotent: two devices compacting simultaneously read
// the same entries, replay in the same order, and produce logically
// identical snapshots.
func (l *OpsLog) Compact(ctx context.Context, maxSnapshots int) (*CompactResult, error) {
	if maxSnapshots < 1 {
		maxSnapshots = 3
	}

	snap, err := l.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}

	// Save snapshot
	if err := l.saveSnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("saving snapshot: %w", err)
	}

	result := &CompactResult{}

	// List and delete all ops in parallel (snapshot captures them all).
	// No need to re-read entries — just list keys and delete.
	opsKeys, err := l.backend.List(ctx, "ops/")
	if err != nil {
		return nil, fmt.Errorf("listing ops: %w", err)
	}
	result.OpsCompacted = len(opsKeys)

	// Parallel delete (up to 10 concurrent)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	var deleteCount int64
	for _, key := range opsKeys {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(key string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := l.backend.Delete(ctx, key); err == nil {
				atomic.AddInt64(&deleteCount, 1)
			}
		}(key)
	}
	wg.Wait()
	result.OpsDeleted = int(deleteCount)

	// Prune old snapshots
	snapKeys, err := l.backend.List(ctx, "manifests/snapshot-")
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	sort.Strings(snapKeys)

	if len(snapKeys) > maxSnapshots {
		for _, key := range snapKeys[:len(snapKeys)-maxSnapshots] {
			if err := l.backend.Delete(ctx, key); err != nil {
				return nil, fmt.Errorf("deleting old snapshot %s: %w", key, err)
			}
			result.SnapshotsDeleted++
		}
	}

	remaining, _ := l.backend.List(ctx, "manifests/snapshot-")
	result.SnapshotsKept = len(remaining)

	// Invalidate cache so next Snapshot() sees the new baseline
	l.InvalidateCache()

	return result, nil
}

// InvalidateCache forces the next Snapshot call to reload from the backend.
func (l *OpsLog) InvalidateCache() {
	l.mu.Lock()
	l.cache = nil
	l.mu.Unlock()
}

// --- Snapshot serialization ---
//
// Snapshots are stored as encrypted JSON manifests at
// manifests/snapshot-{timestamp}.enc, wire-compatible with the existing
// fs.Manifest format.

// manifestJSON is the on-disk format for snapshots, kept compatible with
// the existing fs.Manifest.
type manifestJSON struct {
	Version     int                      `json:"version"`
	Created     time.Time                `json:"created"`
	Updated     time.Time                `json:"updated"`
	Tree        map[string]fileInfoJSON  `json:"tree"`
	Dirs        map[string]dirInfoJSON   `json:"dirs,omitempty"`
	Deleted     map[string]tombstoneJSON `json:"deleted,omitempty"`
	DeletedDirs map[string]tombstoneJSON `json:"deleted_dirs,omitempty"`
}

type dirInfoJSON struct {
	Namespace string    `json:"namespace,omitempty"`
	Device    string    `json:"device,omitempty"`
	Seq       int       `json:"seq,omitempty"`
	Modified  time.Time `json:"modified"`
}

type fileInfoJSON struct {
	Chunks       []string  `json:"chunks"`
	Size         int64     `json:"size"`
	Modified     time.Time `json:"modified"`
	Checksum     string    `json:"checksum"`
	PrevChecksum string    `json:"prev_checksum,omitempty"`
	Namespace    string    `json:"namespace"`
	Device       string    `json:"device,omitempty"`
	Seq          int       `json:"seq,omitempty"`
	LinkTarget   string    `json:"link_target,omitempty"`
}

type tombstoneJSON struct {
	Namespace    string    `json:"namespace,omitempty"`
	Device       string    `json:"device,omitempty"`
	Seq          int       `json:"seq,omitempty"`
	Modified     time.Time `json:"modified"`
	PrevChecksum string    `json:"prev_checksum,omitempty"`
}

func (l *OpsLog) saveSnapshot(ctx context.Context, snap *Snapshot) error {
	m := manifestJSON{
		Version: 1,
		Created: snap.created,
		Updated: time.Now().UTC(),
		Tree:    make(map[string]fileInfoJSON, len(snap.files)),
	}
	for path, fi := range snap.files {
		// Strip chunkless Puts — they are local tracking state ("upload
		// pending") that should not be baked into S3 snapshots. If
		// preserved, other machines see permanently-broken entries that
		// can never be downloaded, causing infinite reconciler retries.
		// Symlinks are legitimately chunkless and must be kept.
		if len(fi.Chunks) == 0 && fi.LinkTarget == "" {
			continue
		}
		m.Tree[path] = fileInfoJSON{
			Chunks:       fi.Chunks,
			Size:         fi.Size,
			Modified:     fi.Modified,
			Checksum:     fi.Checksum,
			PrevChecksum: fi.PrevChecksum,
			Namespace:    fi.Namespace,
			Device:       fi.Device,
			Seq:          fi.Seq,
			LinkTarget:   fi.LinkTarget,
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
				Namespace:    tomb.Namespace,
				Device:       tomb.Device,
				Seq:          tomb.Seq,
				Modified:     tomb.Modified,
				PrevChecksum: tomb.PrevChecksum,
			}
		}
	}
	if len(snap.deletedDirs) > 0 {
		m.DeletedDirs = make(map[string]tombstoneJSON, len(snap.deletedDirs))
		for path, tomb := range snap.deletedDirs {
			m.DeletedDirs[path] = tombstoneJSON{
				Namespace:    tomb.Namespace,
				Device:       tomb.Device,
				Seq:          tomb.Seq,
				Modified:     tomb.Modified,
				PrevChecksum: tomb.PrevChecksum,
			}
		}
	}

	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling snapshot: %w", err)
	}

	encrypted, err := skykey.Encrypt(data, l.encKey)
	if err != nil {
		return fmt.Errorf("encrypting snapshot: %w", err)
	}

	key := fmt.Sprintf("manifests/snapshot-%d.enc", l.now().Unix())
	r := bytes.NewReader(encrypted)
	return l.backend.Put(ctx, key, r, int64(len(encrypted)))
}

// LoadLatestSnapshot downloads and decrypts the most recent S3 snapshot from
// manifests/. Returns (nil, 0, nil) if no snapshots exist. The int64 is the
// snapshot timestamp parsed from the key name.
func (l *OpsLog) LoadLatestSnapshot(ctx context.Context) (*Snapshot, int64, error) {
	keys, err := l.backend.List(ctx, "manifests/snapshot-")
	if err != nil {
		return nil, 0, fmt.Errorf("listing snapshots: %w", err)
	}

	if len(keys) == 0 {
		return nil, 0, nil
	}

	sort.Strings(keys)
	latestKey := keys[len(keys)-1]

	rc, err := l.backend.Get(ctx, latestKey)
	if err != nil {
		return nil, 0, fmt.Errorf("downloading %s: %w", latestKey, err)
	}
	defer rc.Close()

	encrypted, err := io.ReadAll(rc)
	if err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", latestKey, err)
	}

	data, err := skykey.Decrypt(encrypted, l.encKey)
	if err != nil {
		return nil, 0, fmt.Errorf("decrypting %s: %w", latestKey, err)
	}

	var m manifestJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, 0, fmt.Errorf("parsing %s: %w", latestKey, err)
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
			Chunks:       fi.Chunks,
			Size:         fi.Size,
			Modified:     fi.Modified,
			Checksum:     fi.Checksum,
			PrevChecksum: fi.PrevChecksum,
			Namespace:    fi.Namespace,
			Device:       fi.Device,
			Seq:          fi.Seq,
			LinkTarget:   fi.LinkTarget,
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
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Seq:          tomb.Seq,
			Modified:     tomb.Modified,
			PrevChecksum: tomb.PrevChecksum,
		}
	}
	for path, tomb := range m.DeletedDirs {
		snap.deletedDirs[path] = TombstoneInfo{
			Namespace:    tomb.Namespace,
			Device:       tomb.Device,
			Seq:          tomb.Seq,
			Modified:     tomb.Modified,
			PrevChecksum: tomb.PrevChecksum,
		}
	}

	ts := parseSnapshotTimestamp(latestKey)
	return snap, ts, nil
}

// --- Entry read/write ---

// OpEnvelope wire format (22 bytes):
//
//	[0:3]   magic     "OPS"
//	[3]     format    envelope format version (currently 1)
//	[4:7]   schema    skyfs schema version [major, minor, patch]
//	[7:15]  timestamp unix timestamp (big-endian int64)
//	[15:21] device    first 6 bytes of device ID
//	[21]    op_type   0=put, 1=delete
//	[22:]   encrypted entry payload
const envelopeSize = 22

var opMagic = [3]byte{'O', 'P', 'S'}

func writeEntry(ctx context.Context, backend adapter.Backend, e *Entry, encKey []byte) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshaling entry: %w", err)
	}

	encrypted, err := skykey.Encrypt(data, encKey)
	if err != nil {
		return fmt.Errorf("encrypting entry: %w", err)
	}

	blob := makeEnvelope(e, encrypted)
	r := bytes.NewReader(blob)
	return backend.Put(ctx, e.entryKey(), r, int64(len(blob)))
}

func makeEnvelope(e *Entry, encrypted []byte) []byte {
	sv := parseSemver(SchemaVersion)
	buf := make([]byte, envelopeSize+len(encrypted))

	copy(buf[0:3], opMagic[:])
	buf[3] = 1 // format version
	buf[4] = sv[0]
	buf[5] = sv[1]
	buf[6] = sv[2]
	binary.BigEndian.PutUint64(buf[7:15], uint64(e.Timestamp))

	devBytes := []byte(e.Device)
	n := 6
	if len(devBytes) < n {
		n = len(devBytes)
	}
	copy(buf[15:21], devBytes[:n])

	switch e.Type {
	case Delete:
		buf[21] = 1
	case DeleteDir:
		buf[21] = 2
	case CreateDir:
		buf[21] = 3
	case Symlink:
		buf[21] = 4
	}

	copy(buf[envelopeSize:], encrypted)
	return buf
}

func parseEnvelope(data []byte) (encrypted []byte, schemaVer [3]byte, isNew bool) {
	if len(data) >= envelopeSize &&
		data[0] == opMagic[0] && data[1] == opMagic[1] && data[2] == opMagic[2] {
		var sv [3]byte
		sv[0] = data[4]
		sv[1] = data[5]
		sv[2] = data[6]
		return data[envelopeSize:], sv, true
	}
	return data, [3]byte{}, false
}

func readEntries(ctx context.Context, backend adapter.Backend, since int64, encKey []byte) ([]Entry, error) {
	keys, err := backend.List(ctx, "ops/")
	if err != nil {
		return nil, fmt.Errorf("listing ops: %w", err)
	}

	// Filter keys by timestamp
	var filtered []string
	for _, key := range keys {
		ts := parseEntryTimestamp(key)
		if ts >= since {
			filtered = append(filtered, key)
		}
	}

	// Fetch all ops in parallel (up to 10 concurrent)
	type result struct {
		entry Entry
		err   error
	}
	results := make([]result, len(filtered))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for i, key := range filtered {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, key string) {
			defer wg.Done()
			defer func() { <-sem }()

			rc, err := backend.Get(ctx, key)
			if err != nil {
				results[i] = result{err: fmt.Errorf("downloading %s: %w", key, err)}
				return
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				results[i] = result{err: fmt.Errorf("reading %s: %w", key, err)}
				return
			}

			encrypted, schemaVer, isNew := parseEnvelope(raw)
			if isNew {
				codeMajor := semverMajor(SchemaVersion)
				if int(schemaVer[0]) > codeMajor {
					results[i] = result{err: fmt.Errorf("entry %s requires v%d.x (have v%s) — upgrade",
						key, schemaVer[0], SchemaVersion)}
					return
				}
			}

			data, err := skykey.Decrypt(encrypted, encKey)
			if err != nil {
				results[i] = result{err: fmt.Errorf("decrypting %s: %w", key, err)}
				return
			}

			var e Entry
			if err := json.Unmarshal(data, &e); err != nil {
				results[i] = result{err: fmt.Errorf("parsing %s: %w", key, err)}
				return
			}
			results[i] = result{entry: e}
		}(i, key)
	}
	wg.Wait()

	var entries []Entry
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		entries = append(entries, r.entry)
	}

	sortEntries(entries)
	return entries, nil
}

// --- State materialization (LWW-Register-Map CRDT) ---

// buildSnapshot materializes a file tree from a base snapshot and entries.
// For file paths, a direct causal successor (prev_checksum matches the
// current checksum) beats its ancestor even if wall-clock ordering disagrees.
// When no causal relation is known, the path falls back to LWW using
// (timestamp, device, seq). This keeps the CRDT deterministic while reducing
// reliance on raw timestamps for sequential edits.
//
// Deletes are tracked as tombstones in the clock map so an older put
// cannot resurrect a file that was deleted with a higher clock.
func buildSnapshot(base *Snapshot, entries []Entry) *Snapshot {
	snap := &Snapshot{
		files:       make(map[string]FileInfo),
		dirs:        make(map[string]DirInfo),
		deleted:     make(map[string]TombstoneInfo),
		deletedDirs: make(map[string]TombstoneInfo),
		created:     time.Now().UTC(),
		updated:     time.Now().UTC(),
	}

	// Per-path clock tracking. Entries (including deletes) must beat the
	// existing clock to take effect.
	clocks := make(map[string]clockTuple)

	// Directory delete tombstones. A Put/CreateDir under a deleted directory
	// is rejected unless its clock beats the tombstone. This ensures
	// DeleteDir is order-independent (commutative).
	dirTombstones := make(map[string]clockTuple)

	// Per-directory clock tracking for CreateDir/DeleteDir.
	dirClocks := make(map[string]clockTuple)

	if base != nil {
		snap.created = base.created
		for k, v := range base.files {
			snap.files[k] = v
			clocks[k] = clockTuple{ts: v.Modified.Unix(), device: v.Device, seq: v.Seq}
		}
		for k, v := range base.dirs {
			snap.dirs[k] = v
			dirClocks[k] = clockTuple{ts: v.Modified.Unix(), device: v.Device, seq: v.Seq}
		}
		for k, v := range base.deleted {
			snap.deleted[k] = v
			clocks[k] = tombstoneClock(v)
		}
		for k, v := range base.deletedDirs {
			snap.deletedDirs[k] = v
			tombClock := tombstoneClock(v)
			dirTombstones[k] = tombClock
			if prev, ok := dirClocks[k]; !ok || tombClock.beats(prev) {
				dirClocks[k] = tombClock
			}
		}
	}

	for _, e := range entries {
		ec := clockTuple{ts: e.Timestamp, device: e.Device, seq: e.Seq}

		switch e.Type {
		case Put, Symlink:
			if local, ok := snap.files[e.Path]; ok {
				if fileDescendsEntry(local, e) {
					continue
				}
				if !entryDescendsFile(e, local) {
					if prev, ok := clocks[e.Path]; ok && !ec.beats(prev) {
						continue
					}
				}
			} else if tomb, ok := snap.deleted[e.Path]; ok {
				if tombstoneDescendsEntry(tomb, e) {
					continue
				}
				if prev, ok := clocks[e.Path]; ok && !ec.beats(prev) {
					continue
				}
			} else if prev, ok := clocks[e.Path]; ok && !ec.beats(prev) {
				continue
			}
			if coveredByDirTombstone(e.Path, ec, dirTombstones) {
				continue
			}
			clocks[e.Path] = ec
			delete(snap.deleted, e.Path) // re-created file is no longer deleted
			snap.files[e.Path] = FileInfo{
				Chunks:       e.Chunks,
				Size:         e.Size,
				Modified:     time.Unix(e.Timestamp, 0).UTC(),
				Checksum:     e.Checksum,
				PrevChecksum: e.PrevChecksum,
				Namespace:    e.Namespace,
				Device:       e.Device,
				Seq:          e.Seq,
				LinkTarget:   e.LinkTarget,
			}
		case Delete:
			if local, ok := snap.files[e.Path]; ok {
				if !entryDescendsFile(e, local) {
					if prev, ok := clocks[e.Path]; ok && !ec.beats(prev) {
						continue
					}
				}
			} else if prev, ok := clocks[e.Path]; ok && !ec.beats(prev) {
				continue
			}
			clocks[e.Path] = ec
			delete(snap.files, e.Path)
			snap.deleted[e.Path] = tombstoneInfoFromEntry(e)
		case CreateDir:
			if prev, ok := dirClocks[e.Path]; ok && !ec.beats(prev) {
				continue
			}
			// Check exact tombstone and ancestor tombstones
			if ts, ok := dirTombstones[e.Path]; ok && !ec.beats(ts) {
				continue
			}
			if coveredByDirTombstone(e.Path, ec, dirTombstones) {
				continue
			}
			dirClocks[e.Path] = ec
			delete(snap.deletedDirs, e.Path)
			snap.dirs[e.Path] = DirInfo{
				Namespace: e.Namespace,
				Device:    e.Device,
				Seq:       e.Seq,
				Modified:  time.Unix(e.Timestamp, 0).UTC(),
			}
		case DeleteDir:
			// Record tombstone so future puts/creates under this prefix
			// are rejected unless they have a higher clock.
			if prev, ok := dirTombstones[e.Path]; !ok || ec.beats(prev) {
				dirTombstones[e.Path] = ec
				snap.deletedDirs[e.Path] = tombstoneInfoFromEntry(e)
			}
			// Remove existing files under this directory.
			prefix := e.Path + "/"
			for path := range snap.files {
				if strings.HasPrefix(path, prefix) {
					if prev, ok := clocks[path]; !ok || ec.beats(prev) {
						delete(snap.files, path)
						snap.deleted[path] = tombstoneInfoFromEntry(e)
						clocks[path] = ec
					}
				}
			}
			// Remove this directory and sub-directories.
			if prev, ok := dirClocks[e.Path]; !ok || ec.beats(prev) {
				delete(snap.dirs, e.Path)
				dirClocks[e.Path] = ec
			}
			for dir := range snap.dirs {
				if strings.HasPrefix(dir, prefix) {
					if prev, ok := dirClocks[dir]; !ok || ec.beats(prev) {
						delete(snap.dirs, dir)
						dirClocks[dir] = ec
					}
				}
			}
		}
	}

	snap.updated = time.Now().UTC()
	return snap
}

func tombstoneInfoFromEntry(e Entry) TombstoneInfo {
	return TombstoneInfo{
		Namespace:    e.Namespace,
		Device:       e.Device,
		Seq:          e.Seq,
		Modified:     time.Unix(e.Timestamp, 0).UTC(),
		PrevChecksum: e.PrevChecksum,
	}
}

func tombstoneClock(t TombstoneInfo) clockTuple {
	return clockTuple{
		ts:     t.Modified.Unix(),
		device: t.Device,
		seq:    t.Seq,
	}
}

// coveredByDirTombstone returns true if path is under a directory that
// was deleted with a clock that beats ec. Walks up the path hierarchy
// checking each ancestor.
func coveredByDirTombstone(path string, ec clockTuple, tombstones map[string]clockTuple) bool {
	dir := path
	for {
		i := strings.LastIndex(dir, "/")
		if i < 0 {
			return false
		}
		dir = dir[:i]
		if ts, ok := tombstones[dir]; ok && !ec.beats(ts) {
			return true
		}
	}
}

// --- Sorting ---

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Timestamp != entries[j].Timestamp {
			return entries[i].Timestamp < entries[j].Timestamp
		}
		if entries[i].Device != entries[j].Device {
			return entries[i].Device < entries[j].Device
		}
		return entries[i].Seq < entries[j].Seq
	})
}

// --- Key parsing helpers ---

func parseEntryTimestamp(key string) int64 {
	name := strings.TrimPrefix(key, "ops/")
	name = strings.TrimSuffix(name, ".enc")
	parts := strings.SplitN(name, "-", 2)
	if len(parts) < 1 {
		return 0
	}
	var ts int64
	fmt.Sscanf(parts[0], "%d", &ts)
	return ts
}

func parseSnapshotTimestamp(key string) int64 {
	name := strings.TrimPrefix(key, "manifests/snapshot-")
	name = strings.TrimSuffix(name, ".enc")
	var ts int64
	fmt.Sscanf(name, "%d", &ts)
	return ts
}

func parseSemver(v string) [3]byte {
	parts := strings.SplitN(v, ".", 3)
	var sv [3]byte
	if len(parts) >= 1 {
		n, _ := strconv.Atoi(parts[0])
		sv[0] = byte(n)
	}
	if len(parts) >= 2 {
		n, _ := strconv.Atoi(parts[1])
		sv[1] = byte(n)
	}
	if len(parts) >= 3 {
		n, _ := strconv.Atoi(parts[2])
		sv[2] = byte(n)
	}
	return sv
}

func semverMajor(v string) int {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[0])
	return n
}
