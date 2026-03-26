package fs

import (
	"crypto/sha3"
	"encoding/hex"
	"io"
	"sync"

	fastcdc "github.com/jotfs/fastcdc-go"
)

// chunkerTableMu serializes writes to fastcdc's package-level lookup
// table. NewChunker mutates a global table (table[i] ^= seed) on every
// call — a data race when multiple goroutines create chunkers or call
// Next() concurrently. With Seed=0 the XOR is a no-op, but the race
// detector correctly flags the unsynchronized writes.
var chunkerTableMu sync.RWMutex

const (
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
	Hash   string // hex-encoded SHA3-256
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
// It uses FastCDC (content-defined chunking) to find split points based
// on content. This means edits in the middle of a large file only affect
// nearby chunks — everything else deduplicates automatically.
type Chunker struct {
	cdc    *fastcdc.Chunker
	offset int64
	done   bool
}

// NewChunker creates a chunker that reads from r using FastCDC.
func NewChunker(r io.Reader) *Chunker {
	opts := fastcdc.Options{
		MinSize:     MinChunkSize,
		AverageSize: AvgChunkSize,
		MaxSize:     MaxChunkSize,
	}
	chunkerTableMu.Lock()
	cdc, _ := fastcdc.NewChunker(r, opts)
	chunkerTableMu.Unlock()
	return &Chunker{cdc: cdc}
}

// Next returns the next chunk. Returns io.EOF when all data has been read.
// Each call reads at most MaxChunkSize bytes into memory.
func (c *Chunker) Next() (Chunk, error) {
	if c.done {
		return Chunk{}, io.EOF
	}

	chunkerTableMu.RLock()
	cdcChunk, err := c.cdc.Next()
	chunkerTableMu.RUnlock()
	if err != nil {
		if err == io.EOF {
			c.done = true
		}
		return Chunk{}, err
	}

	if len(cdcChunk.Data) == 0 {
		c.done = true
		return Chunk{}, io.EOF
	}

	// Copy data — the CDC library reuses its internal buffer between calls.
	data := make([]byte, len(cdcChunk.Data))
	copy(data, cdcChunk.Data)

	hash := sha3.Sum256(data)
	chunk := Chunk{
		Hash:   hex.EncodeToString(hash[:]),
		Data:   data,
		Offset: c.offset,
		Length: len(data),
	}
	c.offset += int64(len(data))

	return chunk, nil
}

// ContentHash computes the SHA3-256 hash of data and returns it hex-encoded.
func ContentHash(data []byte) string {
	h := sha3.Sum256(data)
	return hex.EncodeToString(h[:])
}
