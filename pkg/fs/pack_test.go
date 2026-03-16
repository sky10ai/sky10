package fs

import (
	"bytes"
	"context"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestPackWriterAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	index := NewPackIndex()

	pw := NewPackWriter(backend, id, index)

	// Add some small encrypted chunks
	chunks := map[string][]byte{
		"hash1": bytes.Repeat([]byte("a"), 1000),
		"hash2": bytes.Repeat([]byte("b"), 2000),
		"hash3": bytes.Repeat([]byte("c"), 500),
	}

	for hash, data := range chunks {
		packed, err := pw.Add(ctx, hash, data)
		if err != nil {
			t.Fatalf("Add %s: %v", hash, err)
		}
		if !packed {
			t.Errorf("%s should be packed (small enough)", hash)
		}
	}

	// Flush remaining
	if err := pw.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify index has entries
	if len(index.Entries) != 3 {
		t.Fatalf("index has %d entries, want 3", len(index.Entries))
	}

	// Read each chunk back via range request
	for hash, original := range chunks {
		loc, ok := index.Entries[hash]
		if !ok {
			t.Fatalf("hash %s not in index", hash)
		}

		data, err := ReadPackedChunk(ctx, backend, loc)
		if err != nil {
			t.Fatalf("ReadPackedChunk %s: %v", hash, err)
		}

		if !bytes.Equal(data, original) {
			t.Errorf("chunk %s: got %d bytes, want %d", hash, len(data), len(original))
		}
	}
}

func TestPackWriterLargeChunkRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	index := NewPackIndex()

	pw := NewPackWriter(backend, id, index)

	// Chunk larger than PackChunkThreshold should be rejected
	large := make([]byte, PackChunkThreshold+1)
	packed, err := pw.Add(ctx, "large-hash", large)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if packed {
		t.Error("large chunk should not be packed")
	}
}

func TestPackIndexSaveLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	idx := NewPackIndex()
	idx.Entries["abc123"] = PackLocation{Pack: "packs/pack_0001.enc", Offset: 0, Length: 1000}
	idx.Entries["def456"] = PackLocation{Pack: "packs/pack_0001.enc", Offset: 1000, Length: 2000}

	if err := SavePackIndex(ctx, backend, idx, encKey); err != nil {
		t.Fatalf("SavePackIndex: %v", err)
	}

	loaded, err := LoadPackIndex(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("LoadPackIndex: %v", err)
	}

	if len(loaded.Entries) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(loaded.Entries))
	}

	for hash, want := range idx.Entries {
		got, ok := loaded.Entries[hash]
		if !ok {
			t.Errorf("missing entry: %s", hash)
			continue
		}
		if got.Pack != want.Pack || got.Offset != want.Offset || got.Length != want.Length {
			t.Errorf("%s: got %+v, want %+v", hash, got, want)
		}
	}
}

func TestPackIndexEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	idx, err := LoadPackIndex(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("LoadPackIndex: %v", err)
	}
	if len(idx.Entries) != 0 {
		t.Errorf("expected empty index, got %d entries", len(idx.Entries))
	}
}

func TestPackWriterAutoFlush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	index := NewPackIndex()

	pw := NewPackWriter(backend, id, index)

	// Add chunks until we exceed PackTargetSize
	chunkSize := 1 << 20 // 1MB
	chunk := make([]byte, chunkSize)
	for i := 0; i < 20; i++ {
		chunk[0] = byte(i) // make each chunk unique
		_, err := pw.Add(ctx, ContentHash(chunk), chunk)
		if err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	if err := pw.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Should have created multiple pack files
	packs, _ := backend.List(ctx, "packs/")
	if len(packs) < 2 {
		t.Errorf("expected multiple pack files for 20MB of data, got %d", len(packs))
	}

	// All chunks should be in index
	if len(index.Entries) != 20 {
		t.Errorf("index has %d entries, want 20", len(index.Entries))
	}
}
