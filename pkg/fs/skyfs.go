package fs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

// ErrFileNotFound is returned when a requested file path does not exist
// in the manifest.
var ErrFileNotFound = errors.New("file not found")

// Store provides encrypted file storage. It encrypts files locally, uploads
// encrypted chunks to a storage backend, and tracks file metadata via an
// append-only ops log with periodic manifest snapshots.
type Store struct {
	backend  adapter.Backend
	identity *Identity
	deviceID string
	clientID string // e.g. "cli/0.4.1", "cirrus/0.4.1"

	mu         sync.Mutex
	nsKeys     map[string][]byte // cached namespace keys
	opSeq      int               // per-session op sequence counter
	packWriter *PackWriter
	packIndex  *PackIndex
	packing    bool // when true, small chunks are bundled into pack files
}

// New creates a Store backed by the given storage backend and identity.
func New(backend adapter.Backend, identity *Identity) *Store {
	idx := NewPackIndex()
	return &Store{
		backend:    backend,
		identity:   identity,
		deviceID:   generateDeviceID(),
		nsKeys:     make(map[string][]byte),
		packIndex:  idx,
		packWriter: NewPackWriter(backend, identity, idx),
	}
}

// NewWithDevice creates a Store with an explicit device ID (for multi-device scenarios).
func NewWithDevice(backend adapter.Backend, identity *Identity, deviceID string) *Store {
	idx := NewPackIndex()
	return &Store{
		backend:    backend,
		identity:   identity,
		deviceID:   deviceID,
		nsKeys:     make(map[string][]byte),
		packIndex:  idx,
		packWriter: NewPackWriter(backend, identity, idx),
	}
}

// SetClient sets the client identifier embedded in ops (e.g. "cli/0.4.1").
func (s *Store) SetClient(client string) {
	s.clientID = client
}

// opsKey returns the shared encryption key for ops and manifests.
// This is the "default" namespace key — shared across all devices.
func (s *Store) opsKey(ctx context.Context) ([]byte, error) {
	return s.getOrCreateNamespaceKey(ctx, "default")
}

// generateDeviceID creates a random 8-character hex device identifier.
func generateDeviceID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// loadCurrentState loads the latest snapshot and replays any ops on top.
func (s *Store) loadCurrentState(ctx context.Context) (*Manifest, error) {
	encKey, err := s.opsKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops key: %w", err)
	}

	// Try to load latest snapshot
	snapshot, snapshotTimestamp, err := s.loadLatestSnapshot(ctx, encKey)
	if err != nil {
		return nil, fmt.Errorf("loading snapshot: %w", err)
	}

	// Read ops since the snapshot
	ops, err := ReadOps(ctx, s.backend, snapshotTimestamp, encKey)
	if err != nil {
		return nil, fmt.Errorf("reading ops: %w", err)
	}

	// Build current state
	return BuildState(snapshot, ops), nil
}

// loadLatestSnapshot finds and loads the most recent manifest snapshot.
// Returns (nil, 0, nil) if no snapshot exists.
func (s *Store) loadLatestSnapshot(ctx context.Context, encKey []byte) (*Manifest, int64, error) {
	// Check for v2 snapshots first
	keys, err := s.backend.List(ctx, "manifests/snapshot-")
	if err != nil {
		return nil, 0, fmt.Errorf("listing snapshots: %w", err)
	}

	if len(keys) > 0 {
		// Pick the latest snapshot (highest timestamp in key name)
		sort.Strings(keys)
		latestKey := keys[len(keys)-1]

		m, err := s.loadManifestFromKey(ctx, latestKey)
		if err != nil {
			return nil, 0, err
		}

		ts := parseSnapshotTimestamp(latestKey)
		return m, ts, nil
	}

	// Fall back to v1 manifest (manifests/current.enc)
	m, err := LoadManifest(ctx, s.backend, encKey)
	if err != nil {
		return nil, 0, err
	}
	if len(m.Tree) == 0 {
		return nil, 0, nil
	}
	return m, 0, nil
}

// loadManifestFromKey downloads and decrypts a manifest from a specific S3 key.
func (s *Store) loadManifestFromKey(ctx context.Context, key string) (*Manifest, error) {
	rc, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", key, err)
	}
	defer rc.Close()

	encrypted, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}

	opsEncKey, err := s.opsKey(ctx)
	if err != nil {
		return nil, err
	}

	data, err := Decrypt(encrypted, opsEncKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w", key, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", key, err)
	}
	if m.Tree == nil {
		m.Tree = make(map[string]FileEntry)
	}
	return &m, nil
}

// parseSnapshotTimestamp extracts the timestamp from a snapshot key.
func parseSnapshotTimestamp(key string) int64 {
	name := key
	name = strings.TrimPrefix(name, "manifests/snapshot-")
	name = strings.TrimSuffix(name, ".enc")
	var ts int64
	fmt.Sscanf(name, "%d", &ts)
	return ts
}

// writeOp writes an operation to the ops log.
func (s *Store) writeOp(ctx context.Context, op *Op) error {
	s.mu.Lock()
	s.opSeq++
	op.Seq = s.opSeq
	s.mu.Unlock()

	op.Device = s.deviceID
	op.Timestamp = time.Now().Unix()
	op.Client = s.clientID

	encKey, err := s.opsKey(ctx)
	if err != nil {
		return fmt.Errorf("ops key: %w", err)
	}

	return WriteOp(ctx, s.backend, op, encKey)
}

// Put encrypts and stores file data read from r at the given path.
// It streams through the data chunk by chunk, never holding more than
// one chunk (max 4MB) in memory.
func (s *Store) Put(ctx context.Context, path string, r io.Reader) error {
	namespace := NamespaceFromPath(path)

	nsKey, err := s.getOrCreateNamespaceKey(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace key for %q: %w", namespace, err)
	}

	// Get prev_checksum for conflict detection
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	prevChecksum := ""
	if existing, ok := state.Tree[path]; ok {
		prevChecksum = existing.Checksum
	}

	chunker := NewChunker(r)
	var chunkHashes []string
	var totalSize int64

	for {
		chunk, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("chunking: %w", err)
		}

		hashBytes := []byte(chunk.Hash)
		fileKey, err := DeriveFileKey(nsKey, hashBytes)
		if err != nil {
			return fmt.Errorf("deriving file key: %w", err)
		}

		// Dedup check
		_, headErr := s.backend.Head(ctx, chunk.BlobKey())
		if headErr == nil {
			chunkHashes = append(chunkHashes, chunk.Hash)
			totalSize += int64(chunk.Length)
			continue
		}
		if !errors.Is(headErr, adapter.ErrNotFound) {
			return fmt.Errorf("checking chunk %s: %w", chunk.Hash[:12], headErr)
		}

		compressed := CompressChunk(chunk.Data)
		encrypted, err := Encrypt(compressed, fileKey)
		if err != nil {
			return fmt.Errorf("encrypting chunk %s: %w", chunk.Hash[:12], err)
		}

		blob := PrependBlobHeader(encrypted)

		// Use pack writer if enabled, otherwise store individually
		if s.packing {
			packed, err := s.packWriter.Add(ctx, chunk.Hash, blob)
			if err != nil {
				return fmt.Errorf("packing chunk %s: %w", chunk.Hash[:12], err)
			}
			if !packed {
				cr := bytes.NewReader(blob)
				if err := s.backend.Put(ctx, chunk.BlobKey(), cr, int64(len(blob))); err != nil {
					return fmt.Errorf("uploading chunk %s: %w", chunk.Hash[:12], err)
				}
			}
		} else {
			cr := bytes.NewReader(blob)
			if err := s.backend.Put(ctx, chunk.BlobKey(), cr, int64(len(blob))); err != nil {
				return fmt.Errorf("uploading chunk %s: %w", chunk.Hash[:12], err)
			}
		}

		chunkHashes = append(chunkHashes, chunk.Hash)
		totalSize += int64(chunk.Length)
	}

	// File checksum from ordered chunk hashes
	allHashes := ""
	for _, h := range chunkHashes {
		allHashes += h
	}
	fileChecksum := ContentHash([]byte(allHashes))

	// Write op (all chunks uploaded first, op is atomic)
	op := &Op{
		Type:         OpPut,
		Path:         path,
		Chunks:       chunkHashes,
		Size:         totalSize,
		Checksum:     fileChecksum,
		PrevChecksum: prevChecksum,
		Namespace:    namespace,
	}

	if err := s.writeOp(ctx, op); err != nil {
		return fmt.Errorf("writing op: %w", err)
	}

	return nil
}

// Get retrieves and decrypts a file, streaming the plaintext to w.
func (s *Store) Get(ctx context.Context, path string, w io.Writer) error {
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	entry, ok := state.Tree[path]
	if !ok {
		return ErrFileNotFound
	}

	nsKey, err := s.getOrCreateNamespaceKey(ctx, entry.Namespace)
	if err != nil {
		return fmt.Errorf("namespace key for %q: %w", entry.Namespace, err)
	}

	for i, chunkHash := range entry.Chunks {
		var raw []byte

		// Check pack index first, fall back to individual blob
		if loc, ok := s.packIndex.Entries[chunkHash]; ok {
			packed, err := ReadPackedChunk(ctx, s.backend, loc)
			if err != nil {
				return fmt.Errorf("reading packed chunk %d (%s): %w", i, chunkHash[:12], err)
			}
			raw = packed
		} else {
			blobKey := (&Chunk{Hash: chunkHash}).BlobKey()
			rc, err := s.backend.Get(ctx, blobKey)
			if err != nil {
				return fmt.Errorf("downloading chunk %d (%s): %w", i, chunkHash[:12], err)
			}
			raw, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("reading chunk %d: %w", i, err)
			}
		}

		encrypted, _, err := StripBlobHeader(raw)
		if err != nil {
			return fmt.Errorf("parsing chunk %d header: %w", i, err)
		}

		fileKey, err := DeriveFileKey(nsKey, []byte(chunkHash))
		if err != nil {
			return fmt.Errorf("deriving file key for chunk %d: %w", i, err)
		}

		compressed, err := Decrypt(encrypted, fileKey)
		if err != nil {
			return fmt.Errorf("decrypting chunk %d: %w", i, err)
		}

		plaintext, err := DecompressChunk(compressed)
		if err != nil {
			return fmt.Errorf("decompressing chunk %d: %w", i, err)
		}

		if ContentHash(plaintext) != chunkHash {
			return fmt.Errorf("chunk %d: hash mismatch (data corrupted)", i)
		}

		if _, err := w.Write(plaintext); err != nil {
			return fmt.Errorf("writing chunk %d: %w", i, err)
		}
	}

	return nil
}

// List returns all file entries matching the prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]ManifestEntry, error) {
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	return state.ListPrefix(prefix), nil
}

// Remove deletes a file entry. Writes a delete op to the log.
func (s *Store) Remove(ctx context.Context, path string) error {
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	entry, ok := state.Tree[path]
	if !ok {
		return ErrFileNotFound
	}

	op := &Op{
		Type:         OpDelete,
		Path:         path,
		PrevChecksum: entry.Checksum,
		Namespace:    entry.Namespace,
	}

	if err := s.writeOp(ctx, op); err != nil {
		return fmt.Errorf("writing delete op: %w", err)
	}

	return nil
}

// Info returns summary information about the store.
func (s *Store) Info(ctx context.Context) (*StoreInfo, error) {
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}

	info := &StoreInfo{
		ID:        s.identity.Address(),
		FileCount: len(state.Tree),
	}

	namespaces := make(map[string]bool)
	for _, entry := range state.Tree {
		info.TotalSize += entry.Size
		namespaces[entry.Namespace] = true
	}
	for ns := range namespaces {
		info.Namespaces = append(info.Namespaces, ns)
	}

	return info, nil
}

// SaveSnapshot writes the current state as a manifest snapshot.
func (s *Store) SaveSnapshot(ctx context.Context) error {
	state, err := s.loadCurrentState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	encKey, err := s.opsKey(ctx)
	if err != nil {
		return fmt.Errorf("ops key: %w", err)
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling snapshot: %w", err)
	}

	encrypted, err := Encrypt(data, encKey)
	if err != nil {
		return fmt.Errorf("encrypting snapshot: %w", err)
	}

	key := fmt.Sprintf("manifests/snapshot-%d.enc", time.Now().Unix())
	r := bytes.NewReader(encrypted)
	if err := s.backend.Put(ctx, key, r, int64(len(encrypted))); err != nil {
		return fmt.Errorf("uploading snapshot: %w", err)
	}

	return nil
}

// EnablePacking turns on pack file bundling for small chunks.
// Call LoadPackState first to load existing pack index from the backend.
func (s *Store) EnablePacking(ctx context.Context) error {
	if err := s.LoadPackState(ctx); err != nil {
		return err
	}
	s.packing = true
	return nil
}

// Close flushes any buffered pack data and saves the pack index.
// Should be called when done writing to ensure all data is persisted.
func (s *Store) Close(ctx context.Context) error {
	if err := s.packWriter.Flush(ctx); err != nil {
		return fmt.Errorf("flushing pack writer: %w", err)
	}
	if len(s.packIndex.Entries) > 0 {
		encKey, err := s.opsKey(ctx)
		if err != nil {
			return fmt.Errorf("ops key: %w", err)
		}
		if err := SavePackIndex(ctx, s.backend, s.packIndex, encKey); err != nil {
			return fmt.Errorf("saving pack index: %w", err)
		}
	}
	return nil
}

// LoadPackState loads the pack index from the backend for reading packed chunks.
func (s *Store) LoadPackState(ctx context.Context) error {
	encKey, err := s.opsKey(ctx)
	if err != nil {
		return err
	}
	idx, err := LoadPackIndex(ctx, s.backend, encKey)
	if err != nil {
		return err
	}
	s.packIndex = idx
	s.packWriter = NewPackWriter(s.backend, s.identity, s.packIndex)
	return nil
}

// StoreInfo contains summary information about a Store.
type StoreInfo struct {
	ID         string   `json:"id"`
	FileCount  int      `json:"file_count"`
	TotalSize  int64    `json:"total_size"`
	Namespaces []string `json:"namespaces"`
}

// getOrCreateNamespaceKey returns the namespace key, loading from S3 or
// creating a new one if it doesn't exist yet.
func (s *Store) getOrCreateNamespaceKey(ctx context.Context, namespace string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key, ok := s.nsKeys[namespace]; ok {
		return key, nil
	}

	// Try device-specific key first (for joined devices), then the original path
	deviceID := shortPubkeyID(s.identity.Address())
	keyPaths := []string{
		"keys/namespaces/" + namespace + "." + deviceID + ".ns.enc",
		"keys/namespaces/" + namespace + ".ns.enc",
	}

	keyExists := false
	for _, nsKeyPath := range keyPaths {
		rc, err := s.backend.Get(ctx, nsKeyPath)
		if err != nil {
			if errors.Is(err, adapter.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("loading namespace key: %w", err)
		}
		keyExists = true
		wrapped, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading namespace key: %w", err)
		}
		nsKey, err := UnwrapNamespaceKey(wrapped, s.identity.PrivateKey)
		if err != nil {
			continue // wrong key for this device, try next path
		}
		s.nsKeys[namespace] = nsKey
		return nsKey, nil
	}

	// If a key exists but we couldn't unwrap it, this device doesn't have access.
	// Do NOT create a new key — that would overwrite the existing one.
	if keyExists {
		return nil, fmt.Errorf("namespace %q: access denied (key exists but cannot be unwrapped — join via invite first)", namespace)
	}

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		return nil, fmt.Errorf("generating namespace key: %w", err)
	}

	wrapped, err := WrapNamespaceKey(nsKey, s.identity.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("wrapping namespace key: %w", err)
	}

	newKeyPath := "keys/namespaces/" + namespace + ".ns.enc"
	r := bytes.NewReader(wrapped)
	if err := s.backend.Put(ctx, newKeyPath, r, int64(len(wrapped))); err != nil {
		return nil, fmt.Errorf("storing namespace key: %w", err)
	}

	s.nsKeys[namespace] = nsKey
	return nsKey, nil
}
