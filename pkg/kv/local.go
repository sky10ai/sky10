package kv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocalLog is a JSONL-backed local KV ops log with in-memory snapshot
// caching. Stores plaintext Entry values (encryption is for S3, not
// local disk) and materializes state via buildSnapshot (LWW-Register-Map).
//
// Thread-safe for concurrent access from Store, Uploader, and Poller.
type LocalLog struct {
	// mu protects all mutable fields below.
	mu       sync.Mutex
	path     string    // path to kv-ops.jsonl
	deviceID string    // local device ID
	actorID  string    // stable per-device actor ID for causal metadata
	cache    *Snapshot // cached snapshot, nil = cold
	seq      int       // per-device sequence counter
}

// NewLocalLog creates a local KV ops log backed by the file at path.
func NewLocalLog(path, deviceID string) *LocalLog {
	return NewLocalLogWithActor(path, deviceID, deviceID)
}

// NewLocalLogWithActor creates a local KV ops log with an explicit actor ID.
func NewLocalLogWithActor(path, deviceID, actorID string) *LocalLog {
	if actorID == "" {
		actorID = deviceID
	}
	return &LocalLog{
		path:     path,
		deviceID: deviceID,
		actorID:  actorID,
	}
}

// Append writes an entry to the JSONL file and incrementally updates the
// cached snapshot if warm. The entry must have all fields already set.
func (l *LocalLog) Append(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.appendToFile(entry); err != nil {
		return err
	}

	if l.cache != nil {
		l.cache = buildSnapshot(l.cache, []Entry{entry})
	}
	return nil
}

// AppendLocal creates a local entry with auto-assigned Device, Timestamp,
// and Seq, then appends it.
func (l *LocalLog) AppendLocal(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cache == nil {
		if _, err := l.rebuildLocked(); err != nil {
			return err
		}
	}

	context := l.cache.Vector()
	l.seq++
	entry.Device = l.deviceID
	entry.Actor = l.actorID
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().Unix()
	}
	entry.Seq = l.seq
	entry.Counter = uint64(l.seq)
	entry.Context = context

	if err := l.appendToFile(entry); err != nil {
		return err
	}

	if l.cache != nil {
		l.cache = buildSnapshot(l.cache, []Entry{entry})
	}
	return nil
}

// Snapshot returns the current CRDT snapshot. Rebuilds from file on first
// call or after InvalidateCache.
func (l *LocalLog) Snapshot() (*Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cache != nil {
		return l.cache, nil
	}
	return l.rebuildLocked()
}

// Lookup returns the ValueInfo for a key from the snapshot.
func (l *LocalLog) Lookup(key string) (ValueInfo, bool) {
	snap, err := l.Snapshot()
	if err != nil {
		return ValueInfo{}, false
	}
	return snap.Lookup(key)
}

// DeviceID returns the local device ID.
func (l *LocalLog) DeviceID() string { return l.deviceID }

// Compact rewrites the JSONL file with one synthetic set per live key.
// Uses atomic write (temp file + rename) for crash safety.
func (l *LocalLog) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cache == nil {
		if _, err := l.rebuildLocked(); err != nil {
			return err
		}
	}

	tmpPath := l.path + ".compact"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating compact file: %w", err)
	}

	for key, vi := range l.cache.entries {
		e := Entry{
			Type:      Set,
			Key:       key,
			Value:     vi.Value,
			Device:    vi.Device,
			Timestamp: vi.Modified.Unix(),
			Seq:       vi.Seq,
			Actor:     vi.Actor,
			Counter:   vi.Counter,
			Context:   vi.Context.Clone(),
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
	for key, tomb := range l.cache.deleted {
		e := Entry{
			Type:      Delete,
			Key:       key,
			Device:    tomb.Device,
			Timestamp: tomb.Modified.Unix(),
			Seq:       tomb.Seq,
			Actor:     tomb.Actor,
			Counter:   tomb.Counter,
			Context:   tomb.Context.Clone(),
		}
		data, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("marshaling tombstone entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	f.Close()

	if err := os.Rename(tmpPath, l.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replacing kv log: %w", err)
	}

	l.cache = nil
	return nil
}

// InvalidateCache forces the next Snapshot call to rebuild from file.
func (l *LocalLog) InvalidateCache() {
	l.mu.Lock()
	l.cache = nil
	l.mu.Unlock()
}

// rebuildLocked reads all entries from file and builds the snapshot.
// Caller must hold mu.
func (l *LocalLog) rebuildLocked() (*Snapshot, error) {
	entries, err := l.readAllLocked()
	if err != nil {
		return nil, fmt.Errorf("reading kv log: %w", err)
	}

	l.cache = buildSnapshot(nil, entries)

	for _, e := range entries {
		if e.Device == l.deviceID && e.Seq > l.seq {
			l.seq = e.Seq
		}
	}

	return l.cache, nil
}

func (l *LocalLog) appendToFile(entry Entry) error {
	dir := filepath.Dir(l.path)
	os.MkdirAll(dir, 0700)

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening kv log: %w", err)
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
func (l *LocalLog) readAllLocked() ([]Entry, error) {
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
