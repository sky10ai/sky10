package mailbox

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

func TestSealedPayloadStoreRoundTrip(t *testing.T) {
	t.Parallel()

	recipient, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	store := NewSealedPayloadStore(newMemoryObjectStore(), "")

	ref, err := store.PutForRecipient(context.Background(), recipient.Address(), []byte("payment proof"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != "sealed_object" {
		t.Fatalf("ref kind = %s, want sealed_object", ref.Kind)
	}

	opened, err := store.Open(context.Background(), ref, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "payment proof" {
		t.Fatalf("opened payload = %q, want %q", opened, "payment proof")
	}
}

func TestSealedPayloadStoreRejectsWrongRecipient(t *testing.T) {
	t.Parallel()

	recipient, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	other, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	store := NewSealedPayloadStore(newMemoryObjectStore(), "")

	ref, err := store.PutForRecipient(context.Background(), recipient.Address(), []byte("receipt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(context.Background(), ref, other); err == nil {
		t.Fatal("expected wrong recipient to fail")
	}
}

type memoryObjectStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{data: make(map[string][]byte)}
}

func (s *memoryObjectStore) Put(_ context.Context, key string, r io.Reader, size int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrUnexpectedEOF
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), data...)
	return nil
}

func (s *memoryObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[key]
	if !ok {
		return nil, adapter.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}

func (s *memoryObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *memoryObjectStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for key := range s.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}
	return out, nil
}

func (s *memoryObjectStore) Head(_ context.Context, key string) (adapter.ObjectMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[key]
	if !ok {
		return adapter.ObjectMeta{}, adapter.ErrNotFound
	}
	return adapter.ObjectMeta{Key: key, Size: int64(len(data)), LastModified: time.Now().UTC()}, nil
}

func (s *memoryObjectStore) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[key]
	if !ok {
		return nil, adapter.ErrNotFound
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	end := offset + length
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data[offset:end]...))), nil
}
