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
