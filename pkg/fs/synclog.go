package fs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SyncOpSource indicates whether the op originated locally or remotely.
type SyncOpSource string

const (
	SourceLocal  SyncOpSource = "local"
	SourceRemote SyncOpSource = "remote"
)

// OutboxEntry is a local change waiting to be pushed to S3.
type OutboxEntry struct {
	Op        OpType `json:"op"`
	Path      string `json:"path"`
	Checksum  string `json:"checksum,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Timestamp int64  `json:"ts"`
}

// InboxEntry is a remote change waiting to be applied locally.
type InboxEntry struct {
	Op        OpType   `json:"op"`
	Path      string   `json:"path"`
	Checksum  string   `json:"checksum,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Device    string   `json:"device,omitempty"`
	Chunks    []string `json:"chunks,omitempty"` // chunk hashes for direct download
	Timestamp int64    `json:"ts"`
}

// SyncLog is an append-only JSONL file with atomic read/write/remove.
// Used for both inbox and outbox.
type SyncLog[T any] struct {
	mu   sync.Mutex
	path string
}

// NewSyncLog creates a sync log at the given path.
func NewSyncLog[T any](path string) *SyncLog[T] {
	return &SyncLog[T]{path: path}
}

// Append adds an entry to the log.
func (l *SyncLog[T]) Append(entry T) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	dir := filepath.Dir(l.path)
	os.MkdirAll(dir, 0700)

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening sync log: %w", err)
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

// ReadAll returns all entries in the log.
func (l *SyncLog[T]) ReadAll() ([]T, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry T
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip corrupt lines
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// Remove deletes a specific entry by matching timestamp and path.
// Rewrites the file without the matching entry.
func (l *SyncLog[T]) Remove(matchFn func(T) bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entries, err := l.readAllUnlocked()
	if err != nil {
		return err
	}

	var kept []T
	for _, e := range entries {
		if !matchFn(e) {
			kept = append(kept, e)
		}
	}

	return l.writeAllUnlocked(kept)
}

// Len returns the number of entries.
func (l *SyncLog[T]) Len() int {
	entries, _ := l.ReadAll()
	return len(entries)
}

// Clear removes all entries.
func (l *SyncLog[T]) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return os.Remove(l.path)
}

func (l *SyncLog[T]) readAllUnlocked() ([]T, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry T
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

func (l *SyncLog[T]) writeAllUnlocked(entries []T) error {
	dir := filepath.Dir(l.path)
	os.MkdirAll(dir, 0700)

	f, err := os.Create(l.path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		f.Write(data)
		f.Write([]byte{'\n'})
	}
	return nil
}

// Helper to create a timestamped outbox entry.
func NewOutboxPut(path, checksum, namespace, localPath string) OutboxEntry {
	return OutboxEntry{
		Op:        OpPut,
		Path:      path,
		Checksum:  checksum,
		Namespace: namespace,
		LocalPath: localPath,
		Timestamp: time.Now().Unix(),
	}
}

func NewOutboxDelete(path, checksum, namespace string) OutboxEntry {
	return OutboxEntry{
		Op:        OpDelete,
		Path:      path,
		Checksum:  checksum,
		Namespace: namespace,
		Timestamp: time.Now().Unix(),
	}
}

func NewInboxPut(path, checksum, namespace, device string, chunks []string) InboxEntry {
	return InboxEntry{
		Op:        OpPut,
		Path:      path,
		Checksum:  checksum,
		Namespace: namespace,
		Device:    device,
		Chunks:    chunks,
		Timestamp: time.Now().Unix(),
	}
}

func NewInboxDelete(path, device string) InboxEntry {
	return InboxEntry{
		Op:        OpDelete,
		Path:      path,
		Device:    device,
		Timestamp: time.Now().Unix(),
	}
}
