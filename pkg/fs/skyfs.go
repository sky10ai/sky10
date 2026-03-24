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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/sha3"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/fs/opslog"
	"github.com/sky10/sky10/pkg/transfer"
)

// ErrFileNotFound is returned when a requested file path does not exist
// in the manifest.
var ErrFileNotFound = errors.New("file not found")

// Store provides encrypted file storage. It encrypts files locally, uploads
// encrypted chunks to a storage backend, and tracks file metadata via an
// append-only ops log with periodic manifest snapshots.
type Store struct {
	backend   adapter.Backend
	identity  *DeviceKey
	deviceID  string
	clientID  string // e.g. "cli/0.4.1", "cirrus/0.4.1"
	namespace string // if set, all files use this namespace instead of path-derived

	mu           sync.Mutex
	nsKeys       map[string][]byte // cached namespace keys
	opsLog       *opslog.OpsLog    // lazily initialized
	packWriter   *PackWriter
	packIndex    *PackIndex
	packing      bool   // when true, small chunks are bundled into pack files
	prevChecksum string // optional: set before Put to avoid loadCurrentState
}

// SetPrevChecksum sets the previous checksum for the next Put call.
// This avoids loading the entire ops history from S3 just to populate
// the prev_checksum field in the op.
func (s *Store) SetPrevChecksum(checksum string) {
	s.prevChecksum = checksum
}

// New creates a Store backed by the given storage backend and identity.
func New(backend adapter.Backend, identity *DeviceKey) *Store {
	idx := NewPackIndex()
	return &Store{
		backend:    backend,
		identity:   identity,
		deviceID:   stableDeviceID(identity),
		nsKeys:     make(map[string][]byte),
		packIndex:  idx,
		packWriter: NewPackWriter(backend, identity, idx),
	}
}

// NewWithDevice creates a Store with an explicit device ID (for multi-device scenarios).
func NewWithDevice(backend adapter.Backend, identity *DeviceKey, deviceID string) *Store {
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

// SetNamespace forces all files to use this namespace instead of
// deriving it from the path. Use for drives where everything in the
// synced folder should share one key.
func (s *Store) SetNamespace(ns string) {
	s.namespace = ns
}

// namespaceFor returns the namespace for a given path.
func (s *Store) namespaceFor(path string) string {
	if s.namespace != "" {
		return s.namespace
	}
	return NamespaceFromPath(path)
}

// opsKey returns the shared encryption key for ops and manifests.
// This is the "default" namespace key — shared across all devices.
func (s *Store) opsKey(ctx context.Context) ([]byte, error) {
	return s.getOrCreateNamespaceKey(ctx, "default")
}

// getOpsLog returns the lazily-initialized OpsLog, creating it on first call.
func (s *Store) getOpsLog(ctx context.Context) (*opslog.OpsLog, error) {
	s.mu.Lock()
	if s.opsLog != nil {
		log := s.opsLog
		s.mu.Unlock()
		return log, nil
	}
	s.mu.Unlock()

	encKey, err := s.opsKey(ctx)
	if err != nil {
		return nil, err
	}

	log := opslog.New(s.backend, encKey, s.deviceID, s.clientID)

	s.mu.Lock()
	s.opsLog = log
	s.mu.Unlock()
	return log, nil
}

// opToEntry converts an Op to an opslog.Entry.
func opToEntry(op *Op) opslog.Entry {
	return opslog.Entry{
		Type:         opslog.EntryType(op.Type),
		Path:         op.Path,
		Chunks:       op.Chunks,
		Size:         op.Size,
		Checksum:     op.Checksum,
		PrevChecksum: op.PrevChecksum,
		LinkTarget:   op.LinkTarget,
		Namespace:    op.Namespace,
	}
}

// snapshotToManifest converts an opslog.Snapshot to a Manifest.
func snapshotToManifest(snap *opslog.Snapshot) *Manifest {
	m := &Manifest{
		Version: 1,
		Created: snap.Created(),
		Updated: snap.Updated(),
		Tree:    make(map[string]FileEntry, snap.Len()),
	}
	for path, fi := range snap.Files() {
		m.Tree[path] = FileEntry{
			Chunks:    fi.Chunks,
			Size:      fi.Size,
			Modified:  fi.Modified,
			Checksum:  fi.Checksum,
			Namespace: fi.Namespace,
		}
	}
	return m
}

// generateDeviceID creates a stable device identifier from the identity key.
// Using the identity ensures the same device always has the same ID across
// daemon restarts, so ops written by this device are always recognized as ours.
func generateDeviceID() string {
	// Fallback random ID — overwritten by New() which uses the identity
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// stableDeviceID derives a device ID from the identity's public key.
func stableDeviceID(identity *DeviceKey) string {
	return shortPubkeyID(identity.Address())
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

// loadCurrentState loads the latest snapshot and replays any ops on top.
// Delegates to the OpsLog for caching and state materialization.
func (s *Store) loadCurrentState(ctx context.Context) (*Manifest, error) {
	log, err := s.getOpsLog(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops log: %w", err)
	}

	snap, err := log.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	m := snapshotToManifest(snap)

	// V1 fallback: if opslog has no data, try legacy manifest
	if len(m.Tree) == 0 {
		encKey, err := s.opsKey(ctx)
		if err != nil {
			return m, nil
		}
		legacy, err := LoadManifest(ctx, s.backend, encKey)
		if err == nil && len(legacy.Tree) > 0 {
			return legacy, nil
		}
	}

	return m, nil
}

// writeOp writes an operation to the ops log via OpsLog.
func (s *Store) writeOp(ctx context.Context, op *Op) error {
	log, err := s.getOpsLog(ctx)
	if err != nil {
		return fmt.Errorf("ops log: %w", err)
	}

	entry := opToEntry(op)
	if err := log.Append(ctx, &entry); err != nil {
		return err
	}

	// Copy back auto-set fields
	op.Device = entry.Device
	op.Timestamp = entry.Timestamp
	op.Seq = entry.Seq
	op.Client = entry.Client
	return nil
}

// Put encrypts and stores file data read from r at the given path.
// It streams through the data chunk by chunk, never holding more than
// one chunk (max 4MB) in memory.
func (s *Store) Put(ctx context.Context, path string, r io.Reader) error {
	namespace := s.namespaceFor(path)

	nsKey, err := s.getOrCreateNamespaceKey(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace key for %q: %w", namespace, err)
	}

	// prev_checksum is informational — used for conflict detection but
	// not required for correctness. Skip the expensive loadCurrentState
	// call (which reads ALL ops from S3) and use whatever the caller
	// set via SetPrevChecksum, or leave empty.
	prevChecksum := s.prevChecksum
	s.prevChecksum = "" // consume it

	// Hash the full content while chunking so the op checksum matches
	// fileChecksum() (SHA3-256 of raw content). This prevents echo loops
	// between devices that compare content hashes against op checksums.
	contentHasher := sha3.New256()
	tee := io.TeeReader(r, contentHasher)

	chunker := NewChunker(tee)
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

	// File checksum = SHA3-256 of raw content (same as fileChecksum in scan.go)
	fileChecksum := hex.EncodeToString(contentHasher.Sum(nil))

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

	return s.downloadChunks(ctx, entry.Chunks, nsKey, w)
}

// GetChunks downloads and decrypts a file using known chunk hashes and namespace.
// This bypasses loadCurrentState entirely — no ops reading from S3.
func (s *Store) GetChunks(ctx context.Context, chunks []string, namespace string, w io.Writer) error {
	nsKey, err := s.getOrCreateNamespaceKey(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace key for %q: %w", namespace, err)
	}
	return s.downloadChunks(ctx, chunks, nsKey, w)
}

// downloadChunks fetches, decrypts, and writes chunks sequentially.
// Each chunk read has a 30-second idle timeout — if the S3 connection
// stalls, the reader is closed and the download fails (caller retries).
func (s *Store) downloadChunks(ctx context.Context, chunks []string, nsKey []byte, w io.Writer) error {
	for i, chunkHash := range chunks {
		var raw []byte

		if loc, ok := s.packIndex.Entries[chunkHash]; ok {
			rc, err := s.backend.GetRange(ctx, loc.Pack, loc.Offset, loc.Length)
			if err != nil {
				return fmt.Errorf("reading packed chunk %d (%s): %w", i, chunkHash[:12], err)
			}
			tr := transfer.NewReader(rc, int64(loc.Length))
			tr.SetIdleTimeout(30 * time.Second)
			raw, err = io.ReadAll(tr)
			rc.Close()
			if err != nil {
				return fmt.Errorf("reading packed chunk %d: %w", i, err)
			}
		} else {
			blobKey := (&Chunk{Hash: chunkHash}).BlobKey()
			rc, err := s.backend.Get(ctx, blobKey)
			if err != nil {
				return fmt.Errorf("downloading chunk %d (%s): %w", i, chunkHash[:12], err)
			}
			tr := transfer.NewReader(rc, -1)
			tr.SetIdleTimeout(30 * time.Second)
			raw, err = io.ReadAll(tr)
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
// Prefer Compact() which also cleans up old ops and snapshots.
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
// creating a new one if it doesn't exist yet. Successfully loaded keys
// are cached locally in ~/.sky10/keys/ as a recovery backup.
func (s *Store) getOrCreateNamespaceKey(ctx context.Context, namespace string) ([]byte, error) {
	// Fast path: check in-memory cache under lock
	s.mu.Lock()
	if key, ok := s.nsKeys[namespace]; ok {
		s.mu.Unlock()
		return key, nil
	}
	s.mu.Unlock()

	// Slow path: S3 + disk I/O without holding the lock.
	// Multiple goroutines may race here for the same namespace — that's fine,
	// they'll all compute the same key and the last write to the cache wins.

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
		s.mu.Lock()
		s.nsKeys[namespace] = nsKey
		s.mu.Unlock()
		s.cacheNamespaceKey(namespace, nsKey)
		return nsKey, nil
	}

	// If a key exists but we couldn't unwrap it, this device doesn't have access.
	// Do NOT create a new key — that would overwrite the existing one.
	// Try the local cache as a last resort (S3 key may have been corrupted).
	if keyExists {
		if cached, err := s.loadCachedNamespaceKey(namespace); err == nil {
			s.mu.Lock()
			s.nsKeys[namespace] = cached
			s.mu.Unlock()
			return cached, nil
		}
		return nil, fmt.Errorf("namespace %q: access denied (key exists but cannot be unwrapped — join via invite first)", namespace)
	}

	// Also check local cache before creating — the S3 key may have been deleted
	if cached, err := s.loadCachedNamespaceKey(namespace); err == nil {
		s.mu.Lock()
		s.nsKeys[namespace] = cached
		s.mu.Unlock()
		return cached, nil
	}

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		return nil, fmt.Errorf("generating namespace key: %w", err)
	}

	// Wrap for this device
	wrapped, err := WrapNamespaceKey(nsKey, s.identity.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("wrapping namespace key: %w", err)
	}
	newKeyPath := "keys/namespaces/" + namespace + ".ns.enc"
	r := bytes.NewReader(wrapped)
	if err := s.backend.Put(ctx, newKeyPath, r, int64(len(wrapped))); err != nil {
		return nil, fmt.Errorf("storing namespace key: %w", err)
	}

	// Also wrap for all other registered devices so they can access new namespaces
	s.wrapKeyForAllDevices(ctx, namespace, nsKey)

	s.mu.Lock()
	s.nsKeys[namespace] = nsKey
	s.mu.Unlock()
	s.cacheNamespaceKey(namespace, nsKey)
	return nsKey, nil
}

// wrapKeyForAllDevices wraps a namespace key for every registered device
// (except this one, which already has it). This ensures new namespaces
// created by one device are accessible to all other devices.
func (s *Store) wrapKeyForAllDevices(ctx context.Context, namespace string, nsKey []byte) {
	devices, err := ListDevices(ctx, s.backend)
	if err != nil {
		return
	}
	myAddr := s.identity.Address()
	for _, dev := range devices {
		if dev.PubKey == myAddr {
			continue
		}
		pubKey, err := parseAddressToPublicKey(dev.PubKey)
		if err != nil {
			continue
		}
		wrapped, err := WrapNamespaceKey(nsKey, pubKey)
		if err != nil {
			continue
		}
		devID := shortPubkeyID(dev.PubKey)
		keyPath := "keys/namespaces/" + namespace + "." + devID + ".ns.enc"
		r := bytes.NewReader(wrapped)
		s.backend.Put(ctx, keyPath, r, int64(len(wrapped)))
	}
}

// cacheNamespaceKey writes the raw namespace key to ~/.sky10/keys/<id>/<namespace>.key.
// Scoped by identity so different devices on the same machine don't collide.
// This is a local-only backup — if S3 gets corrupted, we can recover from here.
func (s *Store) cacheNamespaceKey(namespace string, key []byte) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	id := shortPubkeyID(s.identity.Address())
	dir := filepath.Join(home, ".sky10", "fs", "keys", id)
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, namespace+".key")
	os.WriteFile(path, key, 0600)
}

// loadCachedNamespaceKey reads a locally cached namespace key.
func (s *Store) loadCachedNamespaceKey(namespace string) ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	id := shortPubkeyID(s.identity.Address())
	path := filepath.Join(home, ".sky10", "fs", "keys", id, namespace+".key")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) != 32 {
		return nil, fmt.Errorf("cached key wrong size: %d", len(data))
	}
	return data, nil
}
