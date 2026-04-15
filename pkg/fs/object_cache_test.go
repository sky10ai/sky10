package fs

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
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
