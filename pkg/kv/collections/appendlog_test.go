package collections

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAppendLogAppendAndList(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	log := NewAppendLog(store, "mailbox/events")
	base := time.Unix(1_700_000_000, 123).UTC()
	var seq int
	log.now = func() time.Time {
		ts := base.Add(time.Duration(seq) * time.Nanosecond)
		seq++
		return ts
	}

	first, err := log.Append(context.Background(), []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := log.Append(context.Background(), []byte("two"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := log.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || string(got[0].Value) != "one" {
		t.Fatalf("first entry = %+v, want id=%s value=one", got[0], first.ID)
	}
	if got[1].ID != second.ID || string(got[1].Value) != "two" {
		t.Fatalf("second entry = %+v, want id=%s value=two", got[1], second.ID)
	}
}

func TestAppendLogGet(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	log := NewAppendLog(store, "mailbox/events")
	log.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	wrote, err := log.Append(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := log.Get(wrote.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("entry not found")
	}
	if got.ID != wrote.ID || string(got.Value) != "hello" {
		t.Fatalf("got %+v, want %+v", got, wrote)
	}
}

func TestAppendLogListRebuildsFromStore(t *testing.T) {
	t.Parallel()

	store := newMemoryKVStore()
	log := NewAppendLog(store, "mailbox/events")

	for i := 0; i < 3; i++ {
		log.now = func(i int) func() time.Time {
			return func() time.Time { return time.Unix(1_700_000_000, int64(i)).UTC() }
		}(i)
		if _, err := log.Append(context.Background(), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	other := NewAppendLog(store, "mailbox/events")
	got, err := other.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

type memoryKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryKVStore() *memoryKVStore {
	return &memoryKVStore{data: make(map[string][]byte)}
}

func (s *memoryKVStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = cloneBytes(value)
	return nil
}

func (s *memoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return cloneBytes(value), ok
}

func (s *memoryKVStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *memoryKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0)
	for key := range s.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}
	return out
}
