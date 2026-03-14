package skyfs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

const (
	// ChunkThreshold is the file size above which FastCDC splitting kicks in.
	ChunkThreshold = 1 << 20 // 1MB

	// MinChunkSize is the minimum chunk size for CDC.
	MinChunkSize = 256 << 10 // 256KB

	// AvgChunkSize is the target average chunk size for CDC.
	AvgChunkSize = 1 << 20 // 1MB

	// MaxChunkSize is the maximum chunk size for CDC. This is also the
	// memory ceiling — at most one MaxChunkSize buffer is held at a time.
	MaxChunkSize = 4 << 20 // 4MB
)

// Chunk is a content-addressed piece of a file.
type Chunk struct {
	Hash   string // hex-encoded SHA-256
	Data   []byte
	Offset int64
	Length int
}

// BlobKey returns the S3 key for this chunk using two-level prefix.
// Example: "blobs/ab/cd/abcdef1234...enc"
func (c *Chunk) BlobKey() string {
	h := c.Hash
	return "blobs/" + h[:2] + "/" + h[2:4] + "/" + h + ".enc"
}

// Chunker reads from an io.Reader and produces content-addressed chunks.
// Files smaller than ChunkThreshold are returned as a single chunk.
// Larger files are split using a simple fixed-size chunking strategy.
//
// TODO: Replace fixed-size chunking with FastCDC (jotfs/fastcdc-go) for
// content-defined boundaries. Fixed-size works correctly but doesn't give
// dedup benefits on insertions/edits within large files.
type Chunker struct {
	r      io.Reader
	offset int64
	done   bool
}

// NewChunker creates a chunker that reads from r.
func NewChunker(r io.Reader) *Chunker {
	return &Chunker{r: r}
}

// Next returns the next chunk. Returns io.EOF when all data has been read.
// Each call reads at most MaxChunkSize bytes into memory.
func (c *Chunker) Next() (Chunk, error) {
	if c.done {
		return Chunk{}, io.EOF
	}

	buf := make([]byte, MaxChunkSize)
	n, err := io.ReadFull(c.r, buf)

	if n == 0 {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			c.done = true
			return Chunk{}, io.EOF
		}
		return Chunk{}, err
	}

	buf = buf[:n]

	// If we got less than MaxChunkSize, this is the last chunk
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		c.done = true
	} else if err != nil {
		return Chunk{}, err
	}

	hash := sha256.Sum256(buf)
	chunk := Chunk{
		Hash:   hex.EncodeToString(hash[:]),
		Data:   buf,
		Offset: c.offset,
		Length: n,
	}
	c.offset += int64(n)

	return chunk, nil
}

// ContentHash computes the SHA-256 hash of data and returns it hex-encoded.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
