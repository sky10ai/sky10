package fs

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

type gatedCountingBackend struct {
	adapter.Backend
	gate        chan struct{}
	entered     chan struct{}
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func newGatedCountingBackend(backend adapter.Backend) *gatedCountingBackend {
	return &gatedCountingBackend{
		Backend: backend,
		gate:    make(chan struct{}, 128),
		entered: make(chan struct{}, 128),
	}
}

func (b *gatedCountingBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.enter()
	defer b.leave()
	return b.Backend.Get(ctx, key)
}

func (b *gatedCountingBackend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	b.enter()
	defer b.leave()
	return b.Backend.GetRange(ctx, key, offset, length)
}

func (b *gatedCountingBackend) enter() {
	inFlight := b.inFlight.Add(1)
	for {
		max := b.maxInFlight.Load()
		if inFlight <= max || b.maxInFlight.CompareAndSwap(max, inFlight) {
			break
		}
	}
	b.entered <- struct{}{}
	<-b.gate
}

func (b *gatedCountingBackend) leave() {
	b.inFlight.Add(-1)
}

func (b *gatedCountingBackend) release(n int) {
	for i := 0; i < n; i++ {
		b.gate <- struct{}{}
	}
}

func (b *gatedCountingBackend) MaxInFlight() int {
	return int(b.maxInFlight.Load())
}

func waitForBackendEntries(t *testing.T, entered <-chan struct{}, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatalf("timed out waiting for %d backend entries", n)
		}
	}
}
