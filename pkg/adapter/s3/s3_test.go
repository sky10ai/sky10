package s3

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

func TestMemoryBackend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := NewMemory()

	t.Run("put and get", func(t *testing.T) {
		t.Parallel()
		data := []byte("hello sky10")
		if err := b.Put(ctx, "test/file1.txt", bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("Put: %v", err)
		}

		rc, err := b.Get(ctx, "test/file1.txt")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer rc.Close()

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("got %q, want %q", got, data)
		}
	})

	t.Run("get not found", func(t *testing.T) {
		t.Parallel()
		_, err := b.Get(ctx, "nonexistent")
		if err != adapter.ErrNotFound {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("head", func(t *testing.T) {
		t.Parallel()
		data := []byte("metadata test")
		if err := b.Put(ctx, "test/meta.txt", bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("Put: %v", err)
		}

		meta, err := b.Head(ctx, "test/meta.txt")
		if err != nil {
			t.Fatalf("Head: %v", err)
		}
		if meta.Size != int64(len(data)) {
			t.Errorf("size = %d, want %d", meta.Size, len(data))
		}
		if meta.Key != "test/meta.txt" {
			t.Errorf("key = %q, want %q", meta.Key, "test/meta.txt")
		}
	})

	t.Run("head not found", func(t *testing.T) {
		t.Parallel()
		_, err := b.Head(ctx, "nonexistent")
		if err != adapter.ErrNotFound {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		data := []byte("delete me")
		if err := b.Put(ctx, "test/delete.txt", bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if err := b.Delete(ctx, "test/delete.txt"); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		_, err := b.Get(ctx, "test/delete.txt")
		if err != adapter.ErrNotFound {
			t.Errorf("after delete: got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete not found", func(t *testing.T) {
		t.Parallel()
		err := b.Delete(ctx, "nonexistent")
		if err != adapter.ErrNotFound {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		b := NewMemory()
		ctx := context.Background()

		files := []string{"a/1.txt", "a/2.txt", "b/1.txt"}
		for _, f := range files {
			data := []byte(f)
			if err := b.Put(ctx, f, bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatalf("Put %q: %v", f, err)
			}
		}

		tests := []struct {
			name   string
			prefix string
			want   []string
		}{
			{"all", "", []string{"a/1.txt", "a/2.txt", "b/1.txt"}},
			{"prefix a", "a/", []string{"a/1.txt", "a/2.txt"}},
			{"prefix b", "b/", []string{"b/1.txt"}},
			{"no match", "c/", nil},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := b.List(ctx, tt.prefix)
				if err != nil {
					t.Fatalf("List(%q): %v", tt.prefix, err)
				}
				if len(got) != len(tt.want) {
					t.Fatalf("List(%q) = %v, want %v", tt.prefix, got, tt.want)
				}
				for i, k := range got {
					if k != tt.want[i] {
						t.Errorf("List(%q)[%d] = %q, want %q", tt.prefix, i, k, tt.want[i])
					}
				}
			})
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		b := NewMemory()
		ctx := context.Background()

		v1 := []byte("version 1")
		if err := b.Put(ctx, "file.txt", bytes.NewReader(v1), int64(len(v1))); err != nil {
			t.Fatalf("Put v1: %v", err)
		}

		v2 := []byte("version 2")
		if err := b.Put(ctx, "file.txt", bytes.NewReader(v2), int64(len(v2))); err != nil {
			t.Fatalf("Put v2: %v", err)
		}

		rc, err := b.Get(ctx, "file.txt")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer rc.Close()

		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, v2) {
			t.Errorf("got %q, want %q", got, v2)
		}
	})
}

// Regression: cancelOnClose.Close must be idempotent. The transfer.Reader's
// idle timeout closes the underlying reader to unblock a stuck Read goroutine.
// Then downloadChunks calls rc.Close() in its cleanup path. If Close releases
// the semaphore unconditionally, the second release tries to receive from an
// empty channel — deadlocking the goroutine forever. The reconciler hangs on
// a single stalled chunk download and never processes another file.
func TestCancelOnCloseIdempotent(t *testing.T) {
	t.Parallel()

	sem := make(chan struct{}, 5)
	sem <- struct{}{} // acquire 1 slot

	body := io.NopCloser(bytes.NewReader([]byte("data")))
	_, cancel := context.WithCancel(context.Background())

	coc := &cancelOnClose{
		ReadCloser: body,
		cancel:     cancel,
		release:    func() { <-sem },
	}

	// First close — simulates transfer.Reader's idle timeout closing the reader
	if err := coc.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second close — simulates downloadChunks' explicit rc.Close()
	// Must not deadlock on <-sem with an empty channel.
	done := make(chan struct{})
	go func() {
		coc.Close()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("second Close() deadlocked — semaphore double-release on empty channel")
	}

	// Semaphore should have 0 items (1 acquired, 1 released by first Close)
	if len(sem) != 0 {
		t.Errorf("semaphore has %d items after double close, want 0", len(sem))
	}
}
