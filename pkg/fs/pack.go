package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/sky10/sky10/pkg/adapter"
)

const (
	// PackTargetSize is the target size for a pack file before flushing.
	PackTargetSize = 16 << 20 // 16MB

	// PackChunkThreshold is the max chunk size that gets packed.
	// Chunks larger than this are stored as individual blobs.
	PackChunkThreshold = 4 << 20 // 4MB

	packIndexKey = "pack-index.enc"
)

// PackLocation describes where a chunk lives within a pack file.
type PackLocation struct {
	Pack   string `json:"pack"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// PackIndex maps chunk hashes to their location in pack files.
type PackIndex struct {
	Entries map[string]PackLocation `json:"entries"`
}

// NewPackIndex creates an empty pack index.
func NewPackIndex() *PackIndex {
	return &PackIndex{Entries: make(map[string]PackLocation)}
}

// LoadPackIndex downloads and decrypts the pack index.
// Returns an empty index if none exists.
func LoadPackIndex(ctx context.Context, backend adapter.Backend, encKey []byte) (*PackIndex, error) {
	rc, err := backend.Get(ctx, packIndexKey)
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			return NewPackIndex(), nil
		}
		return nil, fmt.Errorf("downloading pack index: %w", err)
	}
	defer rc.Close()

	encrypted, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading pack index: %w", err)
	}

	data, err := Decrypt(encrypted, encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting pack index: %w", err)
	}

	var idx PackIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing pack index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = make(map[string]PackLocation)
	}
	return &idx, nil
}

// SavePackIndex encrypts and uploads the pack index.
func SavePackIndex(ctx context.Context, backend adapter.Backend, idx *PackIndex, encKey []byte) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("marshaling pack index: %w", err)
	}

	encrypted, err := Encrypt(data, encKey)
	if err != nil {
		return fmt.Errorf("encrypting pack index: %w", err)
	}

	r := bytes.NewReader(encrypted)
	return backend.Put(ctx, packIndexKey, r, int64(len(encrypted)))
}

// PackWriter accumulates encrypted chunks and writes them as pack files.
type PackWriter struct {
	backend  adapter.Backend
	identity *DeviceKey
	index    *PackIndex

	mu      sync.Mutex
	buf     []byte
	entries []pendingEntry
	packSeq int
}

type pendingEntry struct {
	hash   string
	offset int64
	length int64
}

// NewPackWriter creates a pack writer that will flush packs to the backend.
func NewPackWriter(backend adapter.Backend, identity *DeviceKey, index *PackIndex) *PackWriter {
	return &PackWriter{
		backend:  backend,
		identity: identity,
		index:    index,
	}
}

// Add adds an encrypted chunk to the current pack buffer.
// Returns true if the chunk was packed, false if it's too large (caller
// should store as individual blob).
func (pw *PackWriter) Add(ctx context.Context, chunkHash string, encryptedData []byte) (bool, error) {
	if len(encryptedData) > PackChunkThreshold {
		return false, nil
	}

	pw.mu.Lock()
	defer pw.mu.Unlock()

	offset := int64(len(pw.buf))
	pw.buf = append(pw.buf, encryptedData...)
	pw.entries = append(pw.entries, pendingEntry{
		hash:   chunkHash,
		offset: offset,
		length: int64(len(encryptedData)),
	})

	if len(pw.buf) >= PackTargetSize {
		return true, pw.flush(ctx)
	}

	return true, nil
}

// Flush writes any remaining buffered chunks as a pack file.
func (pw *PackWriter) Flush(ctx context.Context) error {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return pw.flush(ctx)
}

func (pw *PackWriter) flush(ctx context.Context) error {
	if len(pw.buf) == 0 {
		return nil
	}

	pw.packSeq++
	packName := fmt.Sprintf("packs/pack_%04d.enc", pw.packSeq)

	r := bytes.NewReader(pw.buf)
	if err := pw.backend.Put(ctx, packName, r, int64(len(pw.buf))); err != nil {
		return fmt.Errorf("writing pack %s: %w", packName, err)
	}

	// Update index
	for _, e := range pw.entries {
		pw.index.Entries[e.hash] = PackLocation{
			Pack:   packName,
			Offset: e.offset,
			Length: e.length,
		}
	}

	pw.buf = nil
	pw.entries = nil
	return nil
}

// ReadPackedChunk reads a single chunk from a pack file using a range request.
func ReadPackedChunk(ctx context.Context, backend adapter.Backend, loc PackLocation) ([]byte, error) {
	rc, err := backend.GetRange(ctx, loc.Pack, loc.Offset, loc.Length)
	if err != nil {
		return nil, fmt.Errorf("reading packed chunk from %s: %w", loc.Pack, err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
