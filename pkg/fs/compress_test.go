package fs

import (
	"bytes"
	"testing"
)

func TestCompressDecompressText(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("hello world, this is compressible text. "), 1000)
	compressed := CompressChunk(data)

	// Should be smaller
	if len(compressed) >= len(data) {
		t.Errorf("compressed (%d) should be smaller than original (%d)", len(compressed), len(data))
	}

	// Should have zstd header
	if compressed[0] != compressZstd {
		t.Errorf("expected zstd header byte, got %x", compressed[0])
	}

	decompressed, err := DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("decompressed data doesn't match original")
	}
}

func TestCompressIncompressible(t *testing.T) {
	t.Parallel()

	// JPEG header
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	jpeg = append(jpeg, bytes.Repeat([]byte{0x42}, 1000)...)

	compressed := CompressChunk(jpeg)

	// Should have "none" header (skipped compression)
	if compressed[0] != compressNone {
		t.Errorf("JPEG should skip compression, got header %x", compressed[0])
	}

	decompressed, err := DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}
	if !bytes.Equal(decompressed, jpeg) {
		t.Error("decompressed doesn't match original")
	}
}

func TestCompressPNG(t *testing.T) {
	t.Parallel()

	png := []byte{0x89, 0x50, 0x4E, 0x47}
	png = append(png, bytes.Repeat([]byte{0x00}, 100)...)

	compressed := CompressChunk(png)
	if compressed[0] != compressNone {
		t.Error("PNG should skip compression")
	}
}

func TestCompressEmpty(t *testing.T) {
	t.Parallel()

	compressed := CompressChunk(nil)
	decompressed, err := DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}
	if len(decompressed) != 0 {
		t.Errorf("expected empty, got %d bytes", len(decompressed))
	}
}

func TestCompressSmallData(t *testing.T) {
	t.Parallel()

	// Small data that doesn't compress well — should store uncompressed
	data := []byte("tiny")
	compressed := CompressChunk(data)

	decompressed, err := DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("round-trip failed for small data")
	}
}

func TestDecompressLegacy(t *testing.T) {
	t.Parallel()

	// Legacy v1/v2 data has no header byte — first byte is actual data
	// Any byte that's not 0x00 or 0x01 is treated as legacy
	legacy := []byte("this is old uncompressed data from v1")
	decompressed, err := DecompressChunk(legacy)
	if err != nil {
		t.Fatalf("DecompressChunk legacy: %v", err)
	}
	if !bytes.Equal(decompressed, legacy) {
		t.Error("legacy data should pass through unchanged")
	}
}

func TestCompressNotWorse(t *testing.T) {
	t.Parallel()

	// Random data that doesn't compress — should fall back to uncompressed
	random := make([]byte, 10000)
	for i := range random {
		random[i] = byte(i*37 + i*i) // pseudo-random
	}

	compressed := CompressChunk(random)
	decompressed, err := DecompressChunk(compressed)
	if err != nil {
		t.Fatalf("DecompressChunk: %v", err)
	}
	if !bytes.Equal(decompressed, random) {
		t.Error("round-trip failed for random data")
	}
}

func TestIsIncompressible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, true},
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47}, true},
		{"ZIP", []byte{0x50, 0x4B, 0x03, 0x04}, true},
		{"GIF", []byte{0x47, 0x49, 0x46, 0x38}, true},
		{"GZIP", []byte{0x1F, 0x8B, 0x08, 0x00}, true},
		{"text", []byte("hello world"), false},
		{"short", []byte{0x01, 0x02}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isIncompressible(tt.data)
			if got != tt.want {
				t.Errorf("isIncompressible(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
