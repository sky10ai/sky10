package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestStoreUnicodeFilenames(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
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

			var buf bytes.Buffer
			if err := store.Get(ctx, path, &buf); err != nil {
				t.Fatalf("Get %q: %v", path, err)
			}

			if buf.String() != data {
				t.Errorf("data mismatch for %q", path)
			}
		})
	}
}

func TestStoreDeepPaths(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	path := "a/b/c/d/e/f/g/deep.md"
	data := "deeply nested"

	if err := store.Put(ctx, path, strings.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var buf bytes.Buffer
	if err := store.Get(ctx, path, &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if buf.String() != data {
		t.Errorf("got %q, want %q", buf.String(), data)
	}

	// Namespace should be the first directory
	entries, _ := store.List(ctx, "")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Namespace != "a" {
		t.Errorf("namespace = %q, want %q", entries[0].Namespace, "a")
	}
}

func TestStorePathsWithSpaces(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	path := "my docs/meeting notes/q4 review.md"
	data := "quarterly review"

	if err := store.Put(ctx, path, strings.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var buf bytes.Buffer
	if err := store.Get(ctx, path, &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if buf.String() != data {
		t.Errorf("got %q, want %q", buf.String(), data)
	}
}

func TestStoreBinaryFiles(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
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

	var buf bytes.Buffer
	if err := store.Get(ctx, "assets/photo.png", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("binary data mismatch")
	}
}

func TestStoreEmptyFile(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "empty.md", bytes.NewReader(nil)); err != nil {
		t.Fatalf("Put empty: %v", err)
	}

	var buf bytes.Buffer
	if err := store.Get(ctx, "empty.md", &buf); err != nil {
		t.Fatalf("Get empty: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected 0 bytes, got %d", buf.Len())
	}
}

func TestStoreExactChunkBoundary(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
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

	var buf bytes.Buffer
	if err := store.Get(ctx, "large/exact.bin", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("data mismatch for exact boundary file: got %d bytes, want %d", buf.Len(), len(data))
	}
}

func TestStoreMultipleNamespaces(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	namespaces := map[string]string{
		"journal/entry.md":    "journal entry",
		"financial/report.md": "financial report",
		"contacts/alice.vcf":  "alice contact",
		"notes.md":            "root note",
	}

	for path, content := range namespaces {
		if err := store.Put(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", path, err)
		}
	}

	// Verify each file is retrievable
	for path, want := range namespaces {
		var buf bytes.Buffer
		if err := store.Get(ctx, path, &buf); err != nil {
			t.Fatalf("Get %q: %v", path, err)
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
