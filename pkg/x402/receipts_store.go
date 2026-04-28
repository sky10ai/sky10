package x402

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ReceiptStore persists settled-call receipts so they survive a
// daemon restart. The Budget package loads existing receipts on
// construction and appends new ones on every Charge.
//
// Implementations:
//
//   - FileReceiptStore — append-only JSONL on disk; the production
//     wiring in commands/serve_x402.go uses this
//   - MemoryReceiptStore — in-memory; tests use this when they want
//     to assert what was persisted
//   - nil is allowed; Budget treats it as "no persistence"
type ReceiptStore interface {
	Load() ([]Receipt, error)
	Append(Receipt) error
}

// FileReceiptStore appends one JSON-encoded receipt per line to a
// single file. Loads the whole file on Load; appends are atomic per
// line via O_APPEND. Concurrent appends serialize on an internal
// mutex.
type FileReceiptStore struct {
	mu   sync.Mutex
	path string
}

// NewFileReceiptStore constructs a receipt store backed by the
// supplied path. The directory is created on first append if it
// does not already exist.
func NewFileReceiptStore(path string) *FileReceiptStore {
	return &FileReceiptStore{path: path}
}

// Load reads every receipt from the file. A missing file returns an
// empty slice rather than an error: a fresh installation has no
// history to load. Malformed lines are skipped (logged at the call
// site, not here).
func (s *FileReceiptStore) Load() ([]Receipt, error) {
	if s == nil {
		return nil, errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", s.path, err)
	}
	defer f.Close()

	var out []Receipt
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Receipt
		if err := json.Unmarshal(line, &r); err != nil {
			// Skip malformed lines; the receipt log is a best-
			// effort audit trail, not a transactional record.
			continue
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("read %s: %w", s.path, err)
	}
	return out, nil
}

// Append writes one JSON-encoded receipt as a new line. The file
// is created (and parent directory created) on first call.
func (s *FileReceiptStore) Append(r Receipt) error {
	if s == nil {
		return errors.New("nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := openAppend(s.path)
	if err != nil {
		return err
	}
	defer f.Close()

	buf, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	return f.Sync()
}

// openAppend opens path for append, creating parent directories as
// needed. Shared with the test-side raw appender.
func openAppend(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// MemoryReceiptStore is the test-side store. It records appends in
// an in-memory slice and replays them on Load.
type MemoryReceiptStore struct {
	mu       sync.Mutex
	receipts []Receipt
}

// NewMemoryReceiptStore constructs an empty store.
func NewMemoryReceiptStore() *MemoryReceiptStore {
	return &MemoryReceiptStore{}
}

// Load returns the in-memory receipts.
func (s *MemoryReceiptStore) Load() ([]Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Receipt, len(s.receipts))
	copy(out, s.receipts)
	return out, nil
}

// Append records a receipt.
func (s *MemoryReceiptStore) Append(r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts = append(s.receipts, r)
	return nil
}
