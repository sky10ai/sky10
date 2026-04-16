package fs

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestStorePutGetChunksNilBackendUsesLocalObjectCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	store := New(nil, id)
	store.SetNamespace("agents")

	ctx := context.Background()
	if err := store.Put(ctx, "profile.md", bytes.NewReader([]byte("hello from local cache"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	result := store.LastPutResult()
	if result == nil || len(result.Chunks) == 0 {
		t.Fatal("expected chunk metadata from Put")
	}

	opsPath := filepath.Join(t.TempDir(), "ops.jsonl")
	_ = opsPath

	var buf bytes.Buffer
	if err := store.GetChunks(ctx, result.Chunks, "agents", &buf); err != nil {
		t.Fatalf("GetChunks: %v", err)
	}
	if got := buf.String(); got != "hello from local cache" {
		t.Fatalf("content = %q", got)
	}
}

func TestStorePutWithBackendPopulatesLocalObjectCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	store := New(s3adapter.NewMemory(), id)
	store.SetNamespace("shared")

	ctx := context.Background()
	if err := store.Put(ctx, "note.md", bytes.NewReader([]byte("hello from hybrid cache"))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	result := store.LastPutResult()
	if result == nil || len(result.Chunks) == 0 {
		t.Fatal("expected chunk metadata from Put")
	}

	nsID, _, err := store.resolveNamespaceState(ctx, "shared")
	if err != nil {
		t.Fatalf("resolveNamespaceState: %v", err)
	}
	if !localBlobExists(nsID, result.Chunks[0]) {
		t.Fatal("expected uploaded chunk to be cached locally for peer serving")
	}
}
