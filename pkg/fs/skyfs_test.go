package fs

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

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

			var buf bytes.Buffer
			if err := store.Get(ctx, tt.path, &buf); err != nil {
				t.Fatalf("Get: %v", err)
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

	var buf bytes.Buffer
	if err := store.Get(ctx, "large/bigfile.bin", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("data mismatch for 10MB file: got %d bytes, want %d bytes", buf.Len(), len(data))
	}
}

func TestStoreGetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	err := store.Get(ctx, "nonexistent.md", io.Discard)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
	if err != ErrFileNotFound {
		t.Errorf("got %v, want ErrFileNotFound", err)
	}
}

func TestStoreList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	files := map[string]string{
		"journal/a.md":        "entry a",
		"journal/b.md":        "entry b",
		"financial/report.md": "q4 report",
		"notes.md":            "root notes",
	}

	for path, content := range files {
		if err := store.Put(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("Put %q: %v", path, err)
		}
	}

	tests := []struct {
		prefix string
		want   int
	}{
		{"journal/", 2},
		{"financial/", 1},
		{"", 4},
		{"nonexistent/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			entries, err := store.List(ctx, tt.prefix)
			if err != nil {
				t.Fatalf("List(%q): %v", tt.prefix, err)
			}
			if len(entries) != tt.want {
				t.Errorf("List(%q) returned %d entries, want %d", tt.prefix, len(entries), tt.want)
			}
		})
	}
}

func TestStoreRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "journal/test.md", strings.NewReader("content")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := store.Remove(ctx, "journal/test.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	err := store.Get(ctx, "journal/test.md", io.Discard)
	if err != ErrFileNotFound {
		t.Errorf("after Remove: got %v, want ErrFileNotFound", err)
	}

	// Remove nonexistent
	err = store.Remove(ctx, "nonexistent.md")
	if err != ErrFileNotFound {
		t.Errorf("Remove nonexistent: got %v, want ErrFileNotFound", err)
	}
}

func TestStoreOverwrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "file.md", strings.NewReader("version 1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	if err := store.Put(ctx, "file.md", strings.NewReader("version 2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	var buf bytes.Buffer
	if err := store.Get(ctx, "file.md", &buf); err != nil {
		t.Fatalf("Get: %v", err)
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

	// Both files should still return correct content
	for _, path := range []string{"file1.md", "file2.md"} {
		var buf bytes.Buffer
		if err := store.Get(ctx, path, &buf); err != nil {
			t.Fatalf("Get %s: %v", path, err)
		}
		if !bytes.Equal(buf.Bytes(), data) {
			t.Errorf("%s: data mismatch", path)
		}
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
	// journal + financial + default (used for ops encryption)
	if len(nsKeys) != 3 {
		t.Errorf("expected 3 namespace keys, got %d: %v", len(nsKeys), nsKeys)
	}
}

func TestStoreInfo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := newTestStore(t)

	if err := store.Put(ctx, "journal/a.md", strings.NewReader("aaa")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Put(ctx, "notes.md", strings.NewReader("bbb")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := store.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}

	if info.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", info.FileCount)
	}
	if info.TotalSize != 6 {
		t.Errorf("TotalSize = %d, want 6", info.TotalSize)
	}
	if !strings.HasPrefix(info.ID, "sky10q") {
		t.Errorf("ID = %q, want sky10q prefix", info.ID)
	}
}
