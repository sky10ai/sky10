package fs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// TestNamespaceKeyLookupDoesNotBlockConcurrentOps deleted:
// Used store.Get which is dead. Concurrent Put still tested below.

// TestConcurrentKeyFetchNonBlocking deleted:
// Used store.Get and simulateApprove which are dead.

// Multiple stores sharing the same backend (simulating multiple drives)
// must not deadlock when accessing different namespaces concurrently.
func TestConcurrentNamespacesNonBlocking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()

	stores := make([]*Store, 5)
	namespaces := []string{"drive-a", "drive-b", "drive-c", "drive-d", "drive-e"}
	for i := range stores {
		stores[i] = NewWithDevice(backend, id, "device-1")
		stores[i].SetNamespace(namespaces[i])
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	for i, store := range stores {
		wg.Add(1)
		go func(s *Store, ns string) {
			defer wg.Done()
			s.Put(ctx, "file.md", strings.NewReader("data from "+ns))
		}(store, namespaces[i])
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent namespace creation timed out")
	}
}
