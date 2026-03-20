package opslog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocalOpsLog is a JSONL-backed local ops log with in-memory CRDT snapshot
// caching. It stores plaintext Entry values (encryption is for S3, not local
// disk) and materializes the current file tree via buildSnapshot
// (LWW-Register-Map CRDT).
//
// Thread-safe for concurrent access from WatcherHandler, Poller,
// OutboxWorker, and Reconciler.
type LocalOpsLog struct {
	// mu protects all mutable fields below.
	mu         sync.Mutex
	path       string    // path to ops.jsonl
	deviceID   string    // local device ID
	cache      *Snapshot // cached CRDT snapshot, nil = cold
	lastRemote int64     // max timestamp of entries from other devices
	seq        int       // per-device sequence counter for AppendLocal
}

// NewLocalOpsLog creates a local ops log backed by the file at path.
// The deviceID identifies the local device; entries from other devices
// contribute to LastRemoteOp.
func NewLocalOpsLog(path, deviceID string) *LocalOpsLog {
	return &LocalOpsLog{
		path:     path,
		deviceID: deviceID,
	}
}

// Append writes an entry to the JSONL file and incrementally updates the
// cached snapshot if warm. The entry must have all fields already set
// (Device, Timestamp, Seq, etc.).
func (l *LocalOpsLog) Append(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.appendToFile(entry); err != nil {
		return err
	}

	if entry.Device != l.deviceID && entry.Timestamp > l.lastRemote {
		l.lastRemote = entry.Timestamp
	}

	// Incrementally update cache if warm; otherwise leave cold
	// for next Snapshot() to rebuild from file.
	if l.cache != nil {
		l.cache = buildSnapshot(l.cache, []Entry{entry})
	}
	return nil
}

// AppendLocal creates a local entry with auto-assigned Device, Timestamp,
// and Seq fields, then appends it to the log. Used by components that
// generate local ops (e.g. WatcherHandler).
func (l *LocalOpsLog) AppendLocal(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	entry.Device = l.deviceID
	entry.Timestamp = time.Now().Unix()
	entry.Seq = l.seq

	if err := l.appendToFile(entry); err != nil {
		return err
	}

	// Local entries from our device — don't update lastRemote.
	if l.cache != nil {
		l.cache = buildSnapshot(l.cache, []Entry{entry})
	}
	return nil
}

// Snapshot returns the current CRDT snapshot. Rebuilds from file on first
// call or after InvalidateCache (crash recovery path).
func (l *LocalOpsLog) Snapshot() (*Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cache != nil {
		return l.cache, nil
	}
	return l.rebuildLocked()
}

// Lookup returns the FileInfo for a path from the snapshot.
// Returns false if the path doesn't exist or the snapshot can't be built.
func (l *LocalOpsLog) Lookup(path string) (FileInfo, bool) {
	snap, err := l.Snapshot()
	if err != nil {
		return FileInfo{}, false
	}
	return snap.Lookup(path)
}

// LastRemoteOp returns the max timestamp of entries from non-local devices.
// Used as the poller cursor for S3 ReadSince.
func (l *LocalOpsLog) LastRemoteOp() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastRemote
}

// SetLastRemoteOp advances the remote cursor. Only increases, never
// decreases. Called by the poller to advance past own-device ops on S3
// that aren't added to the local log.
func (l *LocalOpsLog) SetLastRemoteOp(ts int64) {
	l.mu.Lock()
	if ts > l.lastRemote {
		l.lastRemote = ts
	}
	l.mu.Unlock()
}

// InvalidateCache forces the next Snapshot call to rebuild from file.
func (l *LocalOpsLog) InvalidateCache() {
	l.mu.Lock()
	l.cache = nil
	l.mu.Unlock()
}

// rebuildLocked reads all entries from file and builds the snapshot.
// Also recomputes lastRemote from entries (preserving higher values
// from SetLastRemoteOp). Caller must hold mu.
func (l *LocalOpsLog) rebuildLocked() (*Snapshot, error) {
	entries, err := l.readAllLocked()
	if err != nil {
		return nil, fmt.Errorf("reading local ops log: %w", err)
	}

	l.cache = buildSnapshot(nil, entries)

	var maxRemote int64
	for _, e := range entries {
		if e.Device != l.deviceID && e.Timestamp > maxRemote {
			maxRemote = e.Timestamp
		}
		if e.Device == l.deviceID && e.Seq > l.seq {
			l.seq = e.Seq
		}
	}
	if maxRemote > l.lastRemote {
		l.lastRemote = maxRemote
	}

	return l.cache, nil
}

func (l *LocalOpsLog) appendToFile(entry Entry) error {
	dir := filepath.Dir(l.path)
	os.MkdirAll(dir, 0700)

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening local ops log: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling entry: %w", err)
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// readAllLocked reads all entries from the JSONL file. Caller must hold mu.
// Corrupt lines are silently skipped for crash tolerance.
func (l *LocalOpsLog) readAllLocked() ([]Entry, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}
