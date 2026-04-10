package collections

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// KVStore is the subset of the KV store used by collection primitives.
type KVStore interface {
	Set(ctx context.Context, key string, value []byte) error
	Get(key string) ([]byte, bool)
	Delete(ctx context.Context, key string) error
	List(prefix string) []string
}

// AppendLog is an append-only sequence of immutable values backed by unique KV
// keys below a shared prefix.
type AppendLog struct {
	store   KVStore
	prefix  string
	now     func() time.Time
	newUUID func() string
}

// AppendLogEntry is one immutable record in an AppendLog.
type AppendLogEntry struct {
	ID        string
	CreatedAt time.Time
	Value     []byte
}

// NewAppendLog creates an append-only log rooted at prefix.
func NewAppendLog(store KVStore, prefix string) *AppendLog {
	return &AppendLog{
		store:   store,
		prefix:  normalizeCollectionPrefix(prefix),
		now:     func() time.Time { return time.Now().UTC() },
		newUUID: func() string { return uuid.NewString() },
	}
}

// Append writes a new immutable log entry and returns its metadata.
func (l *AppendLog) Append(ctx context.Context, value []byte) (AppendLogEntry, error) {
	if l == nil || l.store == nil {
		return AppendLogEntry{}, fmt.Errorf("append log store is required")
	}

	now := l.now().UTC()
	id := appendLogID(now, l.newUUID())
	if err := l.store.Set(ctx, l.entryKey(id), value); err != nil {
		return AppendLogEntry{}, err
	}
	return AppendLogEntry{ID: id, CreatedAt: now, Value: cloneBytes(value)}, nil
}

// Get returns a single entry by ID.
func (l *AppendLog) Get(id string) (AppendLogEntry, bool, error) {
	if l == nil || l.store == nil {
		return AppendLogEntry{}, false, fmt.Errorf("append log store is required")
	}

	value, ok := l.store.Get(l.entryKey(id))
	if !ok {
		return AppendLogEntry{}, false, nil
	}
	createdAt, err := parseAppendLogID(id)
	if err != nil {
		return AppendLogEntry{}, false, err
	}
	return AppendLogEntry{ID: id, CreatedAt: createdAt, Value: cloneBytes(value)}, true, nil
}

// List returns all log entries in append order.
func (l *AppendLog) List() ([]AppendLogEntry, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("append log store is required")
	}

	keys := l.store.List(l.listPrefix())
	sort.Strings(keys)

	out := make([]AppendLogEntry, 0, len(keys))
	for _, key := range keys {
		id, ok := strings.CutPrefix(key, l.listPrefix())
		if !ok || id == "" {
			continue
		}
		entry, found, err := l.Get(id)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (l *AppendLog) entryKey(id string) string {
	return l.listPrefix() + id
}

func (l *AppendLog) listPrefix() string {
	return l.prefix + "/"
}

func normalizeCollectionPrefix(prefix string) string {
	return strings.TrimSuffix(strings.TrimSpace(prefix), "/")
}

func appendLogID(at time.Time, suffix string) string {
	if suffix == "" {
		suffix = uuid.NewString()
	}
	return fmt.Sprintf("%020d-%s", at.UnixNano(), suffix)
}

func parseAppendLogID(id string) (time.Time, error) {
	head, _, ok := strings.Cut(id, "-")
	if !ok {
		return time.Time{}, fmt.Errorf("invalid append log id %q", id)
	}
	unixNano, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing append log id %q: %w", id, err)
	}
	return time.Unix(0, unixNano).UTC(), nil
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp
}
