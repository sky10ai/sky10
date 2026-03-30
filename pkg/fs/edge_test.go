package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestStoreUnicodeFilenames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	paths := []string{
		"journal/日記.md",
		"journal/Ünïcödé.md",
		"journal/emoji-📝.md",
		"путь/файл.txt",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data := "content for " + path
			if err := store.Put(ctx, path, strings.NewReader(data)); err != nil {
				t.Fatalf("Put %q: %v", path, err)
			}

			res := store.LastPutResult()
			if res == nil {
				t.Fatal("LastPutResult returned nil")
			}

			ns := NamespaceFromPath(path)
			var buf bytes.Buffer
			if err := store.GetChunks(ctx, res.Chunks, ns, &buf); err != nil {
				t.Fatalf("GetChunks %q: %v", path, err)
			}

			if buf.String() != data {
				t.Errorf("data mismatch for %q", path)
			}
		})
	}
}

func TestStoreDeepPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	path := "a/b/c/d/e/f/g/deep.md"
	data := "deeply nested"

	if err := store.Put(ctx, path, strings.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "a", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}

	if buf.String() != data {
		t.Errorf("got %q, want %q", buf.String(), data)
	}

	// Verify namespace derivation
	ns := NamespaceFromPath(path)
	if ns != "a" {
		t.Errorf("NamespaceFromPath(%q) = %q, want %q", path, ns, "a")
	}
}

func TestStorePathsWithSpaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	path := "my docs/meeting notes/q4 review.md"
	data := "quarterly review"

	if err := store.Put(ctx, path, strings.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "my docs", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}

	if buf.String() != data {
		t.Errorf("got %q, want %q", buf.String(), data)
	}
}

func TestStoreBinaryFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	// Simulate a PNG header + random binary data
	data := make([]byte, 4096)
	data[0] = 0x89
	data[1] = 0x50
	data[2] = 0x4E
	data[3] = 0x47
	for i := 4; i < len(data); i++ {
		data[i] = byte(i * 7 % 256)
	}

	if err := store.Put(ctx, "assets/photo.png", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "assets", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("binary data mismatch")
	}
}

func TestStoreEmptyFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "empty.md", bytes.NewReader(nil)); err != nil {
		t.Fatalf("Put empty: %v", err)
	}

	res := store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult returned nil")
	}

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, res.Chunks, "default", &buf); err != nil {
		t.Fatalf("GetChunks empty: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected 0 bytes, got %d", buf.Len())
	}
}

func TestStoreExactChunkBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	// Exactly 2x MaxChunkSize — should produce exactly 2 chunks
	data := make([]byte, 2*MaxChunkSize)
	for i := range data {
		data[i] = byte(i % 199)
	}

	if err := store.Put(ctx, "large/exact.bin", bytes.NewReader(data)); err != nil {
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
		t.Errorf("data mismatch for exact boundary file: got %d bytes, want %d", buf.Len(), len(data))
	}
}

func TestStoreMultipleNamespaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	namespaces := map[string]string{
		"journal/entry.md":    "journal entry",
		"financial/report.md": "financial report",
		"contacts/alice.vcf":  "alice contact",
		"notes.md":            "root note",
	}

	// Put all files and record their chunks
	type putInfo struct {
		chunks []string
		ns     string
	}
	results := make(map[string]putInfo)
	for path, content := range namespaces {
		if err := store.Put(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", path, err)
		}
		res := store.LastPutResult()
		if res == nil {
			t.Fatalf("LastPutResult nil after Put %q", path)
		}
		results[path] = putInfo{chunks: res.Chunks, ns: NamespaceFromPath(path)}
	}

	// Verify each file is retrievable via GetChunks
	for path, want := range namespaces {
		info := results[path]
		var buf bytes.Buffer
		if err := store.GetChunks(ctx, info.chunks, info.ns, &buf); err != nil {
			t.Fatalf("GetChunks %q: %v", path, err)
		}
		if buf.String() != want {
			t.Errorf("%s: got %q, want %q", path, buf.String(), want)
		}
	}
}

func TestStoreNamespaceKeyCaching(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Put two files in the same namespace
	if err := store.Put(ctx, "journal/a.md", strings.NewReader("aaa")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := store.Put(ctx, "journal/b.md", strings.NewReader("bbb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	// Only the journal namespace key (default key no longer created
	// since S3 ops log was removed).
	keys, _ := backend.List(ctx, "keys/namespaces/")
	if len(keys) != 1 {
		t.Errorf("expected 1 namespace key (journal), got %d: %v", len(keys), keys)
	}
}
