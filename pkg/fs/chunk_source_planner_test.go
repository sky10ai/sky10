package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStoreGetChunksDegradesPeerAfterFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	if err := store.Put(ctx, "shared.md", strings.NewReader("peer degrade")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	res := store.LastPutResult()
	if res == nil || len(res.Chunks) != 1 {
		t.Fatal("expected single chunk from Put")
	}

	nsID, _, err := store.resolveNamespaceState(ctx, "default")
	if err != nil {
		t.Fatalf("resolveNamespaceState: %v", err)
	}
	chunkHash := res.Chunks[0]
	chunkPath, err := localBlobPath(nsID, chunkHash)
	if err != nil {
		t.Fatalf("localBlobPath: %v", err)
	}

	raw := readBackendBlob(t, backend, store.blobKeyFor(chunkHash))
	counted := &countingBackend{Backend: backend}
	peer := &stubPeerChunkFetcher{raw: raw, err: errors.New("peer unavailable")}
	store.backend = counted
	store.SetPeerChunkFetcher(peer)

	now := time.Unix(100, 0)
	store.planner.now = func() time.Time { return now }
	store.planner.retryBase = time.Minute
	store.planner.retryMax = time.Minute

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks first: %v", err)
	}
	if got := buf.String(); got != "peer degrade" {
		t.Fatalf("content = %q", got)
	}
	if got := peer.calls.Load(); got != 1 {
		t.Fatalf("peer calls after first read = %d, want 1", got)
	}
	if got := counted.getCalls.Load(); got != 1 {
		t.Fatalf("backend Get calls after first read = %d, want 1", got)
	}

	if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove local blob: %v", err)
	}

	buf.Reset()
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks second: %v", err)
	}
	if got := buf.String(); got != "peer degrade" {
		t.Fatalf("second content = %q", got)
	}
	if got := peer.calls.Load(); got != 1 {
		t.Fatalf("peer calls after degraded retry = %d, want 1", got)
	}
	if got := counted.getCalls.Load(); got != 2 {
		t.Fatalf("backend Get calls after degraded retry = %d, want 2", got)
	}
}

func TestStoreGetChunksRetriesPeerAfterBackoffExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	if err := store.Put(ctx, "shared.md", strings.NewReader("peer retry")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	res := store.LastPutResult()
	if res == nil || len(res.Chunks) != 1 {
		t.Fatal("expected single chunk from Put")
	}

	nsID, _, err := store.resolveNamespaceState(ctx, "default")
	if err != nil {
		t.Fatalf("resolveNamespaceState: %v", err)
	}
	chunkHash := res.Chunks[0]
	chunkPath, err := localBlobPath(nsID, chunkHash)
	if err != nil {
		t.Fatalf("localBlobPath: %v", err)
	}

	raw := readBackendBlob(t, backend, store.blobKeyFor(chunkHash))
	counted := &countingBackend{Backend: backend}
	peer := &stubPeerChunkFetcher{raw: raw, err: errors.New("peer unavailable")}
	store.backend = counted
	store.SetPeerChunkFetcher(peer)

	now := time.Unix(200, 0)
	store.planner.now = func() time.Time { return now }
	store.planner.retryBase = 10 * time.Second
	store.planner.retryMax = 10 * time.Second

	if err := store.GetChunks(ctx, res.Chunks, "default", io.Discard); err != nil {
		t.Fatalf("GetChunks first: %v", err)
	}
	if got := peer.calls.Load(); got != 1 {
		t.Fatalf("peer calls after first read = %d, want 1", got)
	}
	if got := counted.getCalls.Load(); got != 1 {
		t.Fatalf("backend Get calls after first read = %d, want 1", got)
	}

	if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove local blob: %v", err)
	}

	now = now.Add(11 * time.Second)
	peer.err = nil

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks second: %v", err)
	}
	if got := buf.String(); got != "peer retry" {
		t.Fatalf("second content = %q", got)
	}
	if got := peer.calls.Load(); got != 2 {
		t.Fatalf("peer calls after backoff expiry = %d, want 2", got)
	}
	if got := counted.getCalls.Load(); got != 1 {
		t.Fatalf("backend Get calls after peer recovery = %d, want 1", got)
	}
}
