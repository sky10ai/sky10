package opslog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}
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

// LastRemoteOp is a deprecated no-op stub. Cursor tracking was removed
// in the snapshot-exchange architecture. Kept so integration tests compile.
func (l *LocalOpsLog) LastRemoteOp() int64 { return 0 }

// DeviceID returns the local device ID.
func (l *LocalOpsLog) DeviceID() string {
	return l.deviceID
}

// Compact rewrites the ops.jsonl file with one synthetic entry per current
// file, directory, and surviving tombstone. Intermediate superseded ops are
// dropped, but the clocks needed to keep deletes authoritative are preserved.
//
// Uses atomic write (temp file + rename) so a crash mid-compaction
// doesn't lose data.
func (l *LocalOpsLog) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure we have a snapshot
	if l.cache == nil {
		if _, err := l.rebuildLocked(); err != nil {
			return err
		}
	}

	// Convert snapshot files to entries
	tmpPath := l.path + ".compact"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating compact file: %w", err)
	}

	filePaths := make([]string, 0, len(l.cache.files))
	for path := range l.cache.files {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)
	for _, path := range filePaths {
		fi := l.cache.files[path]
		// Strip chunkless Puts — they are local tracking state ("upload
		// pending") that should not persist through compaction. The
		// integrity sweep will re-queue them from disk if needed.
		// Symlinks are legitimately chunkless and must be kept.
		if len(fi.Chunks) == 0 && fi.LinkTarget == "" {
			continue
		}
		entryType := Put
		if fi.LinkTarget != "" {
			entryType = Symlink
		}
		e := Entry{
			Type:       entryType,
			Path:       path,
			Chunks:     fi.Chunks,
			Size:       fi.Size,
			Checksum:   fi.Checksum,
			LinkTarget: fi.LinkTarget,
			Namespace:  fi.Namespace,
			Device:     fi.Device,
			Timestamp:  fi.Modified.Unix(),
			Seq:        fi.Seq,
		}
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshaling entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	dirPaths := make([]string, 0, len(l.cache.dirs))
	for path := range l.cache.dirs {
		dirPaths = append(dirPaths, path)
	}
	sort.Strings(dirPaths)
	for _, path := range dirPaths {
		di := l.cache.dirs[path]
		e := Entry{
			Type:      CreateDir,
			Path:      path,
			Namespace: di.Namespace,
			Device:    di.Device,
			Timestamp: di.Modified.Unix(),
			Seq:       di.Seq,
		}
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshaling entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	deletedPaths := make([]string, 0, len(l.cache.deleted))
	for path := range l.cache.deleted {
		deletedPaths = append(deletedPaths, path)
	}
	sort.Strings(deletedPaths)
	for _, path := range deletedPaths {
		tomb := l.cache.deleted[path]
		e := Entry{
			Type:      Delete,
			Path:      path,
			Namespace: tomb.Namespace,
			Device:    tomb.Device,
			Timestamp: tomb.Modified.Unix(),
			Seq:       tomb.Seq,
		}
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshaling entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	deletedDirPaths := make([]string, 0, len(l.cache.deletedDirs))
	for path := range l.cache.deletedDirs {
		deletedDirPaths = append(deletedDirPaths, path)
	}
	sort.Strings(deletedDirPaths)
	for _, path := range deletedDirPaths {
		tomb := l.cache.deletedDirs[path]
		e := Entry{
			Type:      DeleteDir,
			Path:      path,
			Namespace: tomb.Namespace,
			Device:    tomb.Device,
			Timestamp: tomb.Modified.Unix(),
			Seq:       tomb.Seq,
		}
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshaling entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	f.Close()

	// Atomic replace
	if err := os.Rename(tmpPath, l.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replacing ops log: %w", err)
	}

	// Invalidate cache — compaction may have stripped chunkless entries,
	// so the in-memory snapshot no longer matches the file on disk.
	l.cache = nil
	return nil
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
