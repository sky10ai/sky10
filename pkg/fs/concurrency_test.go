package fs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// The namespace key mutex must not block concurrent operations.
// Before fix: getOrCreateNamespaceKey held the lock during S3 calls,
// so a slow S3 response would block ALL other RPC requests.
func TestNamespaceKeyLookupDoesNotBlockConcurrentOps(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Prime the store — create the namespace key
	if err := store.Put(ctx, "init.md", strings.NewReader("init")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Run many concurrent reads and writes — none should deadlock or timeout
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	done := make(chan struct{})

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				// Writer
				err := store.Put(ctx, "concurrent.md", strings.NewReader("data"))
				if err != nil {
					errs <- err
				}
			} else {
				// Reader
				var buf bytes.Buffer
				err := store.Get(ctx, "init.md", &buf)
				if err != nil {
					errs <- err
				}
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines completed
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent operations timed out — mutex likely held during I/O")
	}

	close(errs)
	for err := range errs {
		t.Errorf("concurrent op error: %v", err)
	}
}

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

// Concurrent reads during a slow key fetch must not block each other.
// Each goroutine should independently resolve the key.
func TestConcurrentKeyFetchNonBlocking(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	// A creates namespace key
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "file.md", strings.NewReader("data"))

	// Wrap for B
	simulateApprove(t, ctx, backend, idA, idB)

	// B fetches the key from S3 concurrently from multiple goroutines
	var wg sync.WaitGroup
	done := make(chan struct{})
	errs := make(chan error, 20)

	storeB := NewWithDevice(backend, idB, "device-b")

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			if err := storeB.Get(ctx, "file.md", &buf); err != nil {
				errs <- err
				return
			}
			if buf.String() != "data" {
				errs <- fmt.Errorf("got %q", buf.String())
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent key fetch timed out")
	}

	close(errs)
	for err := range errs {
		t.Errorf("concurrent fetch error: %v", err)
	}
}
