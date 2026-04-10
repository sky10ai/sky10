package agent

import (
	"context"
	"sync"
	"testing"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

func newTestMailboxStore(t *testing.T) *agentmailbox.Store {
	t.Helper()

	store, err := agentmailbox.NewStore(context.Background(), agentmailbox.NewPrivateKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatal(err)
	}
	return store
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
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return append([]byte(nil), value...), ok
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
