package skyfs

import (
	"bytes"
	"io"
	"testing"
)

func TestChunkerSmallFile(t *testing.T) {
	t.Parallel()

	data := []byte("small file content")
	chunker := NewChunker(bytes.NewReader(data))

	chunk, err := chunker.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	if !bytes.Equal(chunk.Data, data) {
		t.Error("chunk data doesn't match input")
	}
	if chunk.Offset != 0 {
		t.Errorf("offset = %d, want 0", chunk.Offset)
	}
	if chunk.Length != len(data) {
		t.Errorf("length = %d, want %d", chunk.Length, len(data))
	}
	if chunk.Hash == "" {
		t.Error("hash is empty")
	}
	if chunk.Hash != ContentHash(data) {
		t.Error("chunk hash doesn't match ContentHash")
	}

	_, err = chunker.Next()
	if err != io.EOF {
		t.Errorf("second Next: got %v, want io.EOF", err)
	}
}

func TestChunkerLargeFile(t *testing.T) {
	t.Parallel()

	// 10MB file should produce multiple chunks
	size := 10 * 1024 * 1024
	data := bytes.Repeat([]byte("abcdefghij"), size/10)
	chunker := NewChunker(bytes.NewReader(data))

	var chunks []Chunk
	for {
		chunk, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for %dMB file, got %d", size/(1024*1024), len(chunks))
	}

	// Verify offsets are sequential
	expectedOffset := int64(0)
	for i, c := range chunks {
		if c.Offset != expectedOffset {
			t.Errorf("chunk %d: offset = %d, want %d", i, c.Offset, expectedOffset)
		}
		expectedOffset += int64(c.Length)
	}

	// Verify total size
	totalSize := int64(0)
	for _, c := range chunks {
		totalSize += int64(c.Length)
	}
	if totalSize != int64(len(data)) {
		t.Errorf("total size = %d, want %d", totalSize, len(data))
	}

	// Verify reassembly
	var reassembled bytes.Buffer
	for _, c := range chunks {
		reassembled.Write(c.Data)
	}
	if !bytes.Equal(reassembled.Bytes(), data) {
		t.Error("reassembled data doesn't match original")
	}

	// Each chunk should be at most MaxChunkSize
	for i, c := range chunks {
		if c.Length > MaxChunkSize {
			t.Errorf("chunk %d: length %d exceeds max %d", i, c.Length, MaxChunkSize)
		}
	}
}

func TestChunkerEmpty(t *testing.T) {
	t.Parallel()

	chunker := NewChunker(bytes.NewReader(nil))
	_, err := chunker.Next()
	if err != io.EOF {
		t.Errorf("got %v, want io.EOF", err)
	}
}

func TestChunkerDeterministic(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("deterministic"), 100000)

	chunks1 := allChunks(t, data)
	chunks2 := allChunks(t, data)

	if len(chunks1) != len(chunks2) {
		t.Fatalf("chunk count differs: %d vs %d", len(chunks1), len(chunks2))
	}

	for i := range chunks1 {
		if chunks1[i].Hash != chunks2[i].Hash {
			t.Errorf("chunk %d hash differs", i)
		}
	}
}

func TestChunkerExactMaxSize(t *testing.T) {
	t.Parallel()

	// Exactly MaxChunkSize should be one chunk
	data := make([]byte, MaxChunkSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	chunks := allChunks(t, data)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exactly MaxChunkSize, got %d", len(chunks))
	}
}

func TestContentHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"hello", []byte("hello")},
		{"binary", []byte{0x00, 0xFF}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h1 := ContentHash(tt.data)
			h2 := ContentHash(tt.data)
			if h1 != h2 {
				t.Error("same data produced different hashes")
			}
			if len(h1) != 64 { // SHA-256 hex = 64 chars
				t.Errorf("hash length = %d, want 64", len(h1))
			}
		})
	}

	// Different data → different hash
	if ContentHash([]byte("a")) == ContentHash([]byte("b")) {
		t.Error("different data produced same hash")
	}
}

func TestBlobKey(t *testing.T) {
	t.Parallel()

	c := Chunk{Hash: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"}
	got := c.BlobKey()
	want := "blobs/ab/cd/abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890.enc"
	if got != want {
		t.Errorf("BlobKey() = %q, want %q", got, want)
	}
}

func allChunks(t *testing.T, data []byte) []Chunk {
	t.Helper()
	chunker := NewChunker(bytes.NewReader(data))
	var chunks []Chunk
	for {
		chunk, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}
