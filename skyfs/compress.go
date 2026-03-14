package skyfs

import (
	"github.com/klauspost/compress/zstd"
)

// Compression header byte — first byte of stored chunk data.
const (
	compressNone byte = 0x00
	compressZstd byte = 0x01
)

var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	zstdDecoder, _ = zstd.NewReader(nil)
)

// CompressChunk compresses data with zstd if beneficial.
// Returns [header_byte | data]. Skips compression for incompressible formats.
func CompressChunk(data []byte) []byte {
	if len(data) == 0 {
		return []byte{compressNone}
	}

	if isIncompressible(data) {
		out := make([]byte, 1+len(data))
		out[0] = compressNone
		copy(out[1:], data)
		return out
	}

	compressed := zstdEncoder.EncodeAll(data, nil)

	// Only use compression if it actually saves space
	if len(compressed) >= len(data) {
		out := make([]byte, 1+len(data))
		out[0] = compressNone
		copy(out[1:], data)
		return out
	}

	out := make([]byte, 1+len(compressed))
	out[0] = compressZstd
	copy(out[1:], compressed)
	return out
}

// DecompressChunk decompresses data produced by CompressChunk.
// Handles both compressed and uncompressed data via the header byte.
// Also handles legacy data with no header (v1/v2 backward compatibility).
func DecompressChunk(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	switch data[0] {
	case compressNone:
		return data[1:], nil
	case compressZstd:
		return zstdDecoder.DecodeAll(data[1:], nil)
	default:
		// No header byte — legacy uncompressed data (v1/v2)
		return data, nil
	}
}

// isIncompressible checks if data is already compressed or an incompressible
// format by inspecting magic bytes.
func isIncompressible(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	// JPEG
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return true
	}
	// PNG
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return true
	}
	// ZIP / DOCX / XLSX / JAR
	if data[0] == 0x50 && data[1] == 0x4B && data[2] == 0x03 && data[3] == 0x04 {
		return true
	}
	// GIF
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return true
	}
	// GZIP
	if data[0] == 0x1F && data[1] == 0x8B {
		return true
	}
	// MP4 / MOV (ftyp box)
	if len(data) >= 8 && data[4] == 0x66 && data[5] == 0x74 && data[6] == 0x79 && data[7] == 0x70 {
		return true
	}
	// WEBP (RIFF....WEBP)
	if len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return true
	}
	// Zstd (already compressed)
	if data[0] == 0x28 && data[1] == 0xB5 && data[2] == 0x2F && data[3] == 0xFD {
		return true
	}

	return false
}
