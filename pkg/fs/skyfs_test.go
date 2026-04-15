package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func newTestStore(t *testing.T) (*Store, *s3adapter.MemoryBackend) {
	t.Helper()
	backend := s3adapter.NewMemory()
	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return New(backend, id), backend
}

func TestStorePutGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	tests := []struct {
		name string
		path string
		data []byte
	}{
		{"small text", "journal/test.md", []byte("hello sky10")},
		{"empty", "journal/empty.md", []byte{}},
		{"binary", "assets/image.bin", []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF}},
		{"1KB", "journal/medium.md", bytes.Repeat([]byte("x"), 1024)},
		{"root file", "notes.md", []byte("root namespace")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.Put(ctx, tt.path, bytes.NewReader(tt.data)); err != nil {
				t.Fatalf("Put: %v", err)
			}

			res := store.LastPutResult()
			if res == nil {
				t.Fatal("LastPutResult returned nil")
			}

			ns := NamespaceFromPath(tt.path)
			var buf bytes.Buffer
			if err := store.GetChunks(ctx, res.Chunks, ns, &buf); err != nil {
				t.Fatalf("GetChunks: %v", err)
			}

			if !bytes.Equal(buf.Bytes(), tt.data) {
				t.Errorf("data mismatch: got %d bytes, want %d bytes", buf.Len(), len(tt.data))
			}
		})
	}
}

func TestStorePutGetLargeFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	// 10MB file — forces multiple chunks
	size := 10 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251) // prime to avoid patterns
	}

	if err := store.Put(ctx, "large/bigfile.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "large", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("data mismatch for 10MB file: got %d bytes, want %d bytes", buf.Len(), len(data))
	}
}

func TestStoreOverwrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "file.md", strings.NewReader("version 1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	v1 := store.LastPutResult()
	if v1 == nil {
		t.Fatal("LastPutResult nil after v1")
	}

	if err := store.Put(ctx, "file.md", strings.NewReader("version 2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	v2 := store.LastPutResult()
	if v2 == nil {
		t.Fatal("LastPutResult nil after v2")
	}

	if v1.Checksum == v2.Checksum {
		t.Errorf("v1 and v2 checksums should differ, both are %q", v1.Checksum)
	}

	// Verify v2 content is retrievable
	var buf bytes.Buffer
	if err := store.GetChunks(ctx, v2.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks v2: %v", err)
	}
	if buf.String() != "version 2" {
		t.Errorf("got %q, want %q", buf.String(), "version 2")
	}
}

func TestStoreDedup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, backend := newTestStore(t)

	data := []byte("identical content")

	if err := store.Put(ctx, "file1.md", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put file1: %v", err)
	}
	if err := store.Put(ctx, "file2.md", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put file2: %v", err)
	}

	// Count blob objects — should have only one blob for identical content
	blobs, err := backend.List(context.Background(), "blobs/")
	if err != nil {
		t.Fatalf("List blobs: %v", err)
	}
	if len(blobs) != 1 {
		t.Errorf("expected 1 blob (dedup), got %d", len(blobs))
	}
}

func TestStoreEncryptedAtRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, backend := newTestStore(t)

	secret := "this is highly sensitive data that must be encrypted"
	if err := store.Put(ctx, "journal/secret.md", strings.NewReader(secret)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Check all blobs — none should contain plaintext
	blobs, err := backend.List(ctx, "blobs/")
	if err != nil {
		t.Fatalf("List blobs: %v", err)
	}

	for _, blobKey := range blobs {
		rc, err := backend.Get(ctx, blobKey)
		if err != nil {
			t.Fatalf("Get blob %s: %v", blobKey, err)
		}
		raw, _ := io.ReadAll(rc)
		rc.Close()

		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("blob %s contains plaintext secret", blobKey)
		}
	}
}

func TestStoreNamespaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, backend := newTestStore(t)

	if err := store.Put(ctx, "journal/a.md", strings.NewReader("journal entry")); err != nil {
		t.Fatalf("Put journal: %v", err)
	}
	if err := store.Put(ctx, "financial/b.md", strings.NewReader("financial data")); err != nil {
		t.Fatalf("Put financial: %v", err)
	}

	// Should have two namespace keys
	nsKeys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		t.Fatalf("List namespace keys: %v", err)
	}
	// journal + financial (default key no longer created — S3 ops log removed)
	if len(nsKeys) != 2 {
		t.Errorf("expected 2 namespace keys, got %d: %v", len(nsKeys), nsKeys)
	}
}

// failAfterNBackend wraps a backend and returns an error on the Nth
// Get or GetRange call (1-indexed). All other methods pass through.
type failAfterNBackend struct {
	adapter.Backend
	failAt  int
	calls   atomic.Int32
	failErr error
}

func (f *failAfterNBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	n := int(f.calls.Add(1))
	if n >= f.failAt {
		return nil, f.failErr
	}
	return f.Backend.Get(ctx, key)
}

func (f *failAfterNBackend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	n := int(f.calls.Add(1))
	if n >= f.failAt {
		return nil, f.failErr
	}
	return f.Backend.GetRange(ctx, key, offset, length)
}

type countingBackend struct {
	adapter.Backend
	getCalls      atomic.Int32
	getRangeCalls atomic.Int32
}

func (c *countingBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	c.getCalls.Add(1)
	return c.Backend.Get(ctx, key)
}

func (c *countingBackend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	c.getRangeCalls.Add(1)
	return c.Backend.GetRange(ctx, key, offset, length)
}

type stubPeerChunkFetcher struct {
	raw   []byte
	err   error
	calls atomic.Int32
}

func (s *stubPeerChunkFetcher) GetChunk(ctx context.Context, nsID, chunkHash string) ([]byte, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return append([]byte(nil), s.raw...), nil
}

func readBackendBlob(t *testing.T, backend adapter.Backend, key string) []byte {
	t.Helper()
	rc, err := backend.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	defer rc.Close()

	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll %s: %v", key, err)
	}
	return raw
}

func TestDownloadChunksCancellation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	// 5MB file — multiple chunks to exercise the parallel path.
	data := make([]byte, 5*1024*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := store.Put(ctx, "big.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	// GetChunks with an already-cancelled context.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	err := store.GetChunks(cancelled, res.Chunks, "default", io.Discard)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDownloadChunksErrorMidStream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, backend := newTestStore(t)

	// 10MB file — forces ~10 chunks.
	data := make([]byte, 10*1024*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := store.Put(ctx, "big.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	// Swap backend to one that fails on the 3rd Get/GetRange.
	injectedErr := fmt.Errorf("injected S3 error")
	store.backend = &failAfterNBackend{
		Backend: backend,
		failAt:  3,
		failErr: injectedErr,
	}

	err := store.GetChunks(ctx, res.Chunks, "default", io.Discard)
	if err == nil {
		t.Fatal("expected error from failing backend")
	}
	if !errors.Is(err, injectedErr) {
		// The error is wrapped by fetchChunk, so check the message.
		if !strings.Contains(err.Error(), "injected S3 error") {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestDownloadChunksOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	// 20MB file — many chunks to make ordering bugs likely.
	size := 20 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i * 7) % 256) // varied pattern
	}

	if err := store.Put(ctx, "ordered.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("data mismatch: got %d bytes, want %d", buf.Len(), size)
		// Find first differing byte for debugging.
		for i := range data {
			if i >= buf.Len() || buf.Bytes()[i] != data[i] {
				t.Errorf("first difference at byte %d (chunk ~%d)", i, i/(1024*1024))
				break
			}
		}
	}
}

func TestStoreGetChunksPrefersPeerBeforeBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	if err := store.Put(ctx, "shared.md", strings.NewReader("peer before s3")); err != nil {
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

	raw := readBackendBlob(t, backend, store.blobKeyFor(res.Chunks[0]))
	peer := &stubPeerChunkFetcher{raw: raw}
	counted := &countingBackend{Backend: backend}
	store.backend = counted
	store.SetPeerChunkFetcher(peer)

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}
	if got := buf.String(); got != "peer before s3" {
		t.Fatalf("content = %q", got)
	}
	if got := peer.calls.Load(); got != 1 {
		t.Fatalf("peer calls = %d, want 1", got)
	}
	if got := counted.getCalls.Load(); got != 0 {
		t.Fatalf("backend Get calls = %d, want 0", got)
	}
	if got := counted.getRangeCalls.Load(); got != 0 {
		t.Fatalf("backend GetRange calls = %d, want 0", got)
	}
	if !localBlobExists(nsID, res.Chunks[0]) {
		t.Fatal("expected peer-fetched blob to be cached locally")
	}
}

func TestStoreGetChunksFallsBackFromCorruptLocalCacheToBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	if err := store.Put(ctx, "recover.md", strings.NewReader("repair local cache")); err != nil {
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
	blobKey := store.blobKeyFor(chunkHash)
	goodRaw := readBackendBlob(t, backend, blobKey)
	if err := writeLocalBlob(nsID, chunkHash, []byte("corrupt")); err != nil {
		t.Fatalf("writeLocalBlob corrupt: %v", err)
	}

	counted := &countingBackend{Backend: backend}
	store.backend = counted
	store.SetPeerChunkFetcher(nil)

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}
	if got := buf.String(); got != "repair local cache" {
		t.Fatalf("content = %q", got)
	}
	if got := counted.getCalls.Load(); got == 0 {
		t.Fatal("expected backend Get to be used after corrupt local cache")
	}

	repairedRaw, err := readLocalBlob(nsID, chunkHash)
	if err != nil {
		t.Fatalf("readLocalBlob repaired: %v", err)
	}
	if !bytes.Equal(repairedRaw, goodRaw) {
		t.Fatal("expected local cache to be refreshed with backend blob")
	}
}

func TestStoreDownloadChunksBoundsRemoteFetchConcurrency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	data := make([]byte, 10*1024*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := store.Put(ctx, "bounded.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	res := store.LastPutResult()
	if res == nil || len(res.Chunks) < 3 {
		t.Fatalf("expected multiple chunks from Put, got %#v", res)
	}

	gated := newGatedCountingBackend(backend)
	store.backend = gated
	store.chunkPrefetch = 8
	store.remoteFetchSem = make(chan struct{}, 2)

	done := make(chan error, 1)
	go func() {
		done <- store.GetChunks(ctx, res.Chunks, "default", io.Discard)
	}()

	waitForBackendEntries(t, gated.entered, 2)
	gated.release(len(res.Chunks) + 4)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("GetChunks: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for bounded GetChunks")
	}

	if got := gated.MaxInFlight(); got != 2 {
		t.Fatalf("max concurrent remote fetches = %d, want 2", got)
	}
}
