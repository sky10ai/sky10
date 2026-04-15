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
	"github.com/sky10/sky10/pkg/config"
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
	backend      adapter.Backend
	identity     *DeviceKey
	deviceID     string
	devicePubKey string // full device sky10q... address (for device registry matching)
	clientID     string // e.g. "cli/0.4.1"
	namespace    string // if set, all files use this namespace instead of path-derived

	mu             sync.Mutex
	nsKeys         map[string][]byte // cached namespace keys
	nsID           string            // namespace ID for S3 path scoping (set by daemon)
	packWriter     *PackWriter
	packIndex      *PackIndex
	packing        bool   // when true, small chunks are bundled into pack files
	prevChecksum   string // optional: informational prev_checksum for dedup
	lastPut        *PutResult
	peerChunks     peerChunkFetcher
	onChunkRead    func(chunkSourceKind)
	planner        *chunkSourcePlanner
	chunkPrefetch  int
	remoteFetchSem chan struct{}
}

type peerChunkFetcher interface {
	GetChunk(ctx context.Context, nsID, chunkHash string) ([]byte, error)
}

type chunkSourceKind string

const (
	chunkSourceLocal  chunkSourceKind = "local"
	chunkSourcePeer   chunkSourceKind = "peer"
	chunkSourceS3Pack chunkSourceKind = "s3-pack"
	chunkSourceS3Blob chunkSourceKind = "s3-blob"
)

const (
	defaultChunkPrefetchLimit    = 3
	defaultRemoteChunkFetchLimit = 8
)

type chunkSourcePlan struct {
	kind           chunkSourceKind
	cacheOnSuccess bool
}

// PutResult holds metadata from the last successful Put call.
// Used by the outbox worker to confirm the upload in the local log.
type PutResult struct {
	Chunks   []string
	Checksum string
	Size     int64
}

// LastPutResult returns metadata from the last successful Put call,
// or nil if no Put has completed yet.
func (s *Store) LastPutResult() *PutResult {
	return s.lastPut
}

// SetNamespaceID sets the S3 namespace prefix for blob storage.
// When set, blobs are stored at fs/{nsID}/blobs/... instead of blobs/...
func (s *Store) SetNamespaceID(nsID string) {
	s.nsID = nsID
}

// SetPeerChunkFetcher configures optional peer chunk fetching for non-S3 mode.
func (s *Store) SetPeerChunkFetcher(fetcher peerChunkFetcher) {
	s.peerChunks = fetcher
}

// SetChunkReadRecorder configures an optional callback for successful chunk reads.
func (s *Store) SetChunkReadRecorder(fn func(kind chunkSourceKind)) {
	s.onChunkRead = fn
}

// blobKeyFor returns the S3 key for a chunk, respecting the namespace prefix.
func (s *Store) blobKeyFor(hash string) string {
	if s.nsID != "" {
		return namespacedBlobKey(s.nsID, hash)
	}
	return (&Chunk{Hash: hash}).BlobKey()
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
		backend:        backend,
		identity:       identity,
		deviceID:       stableDeviceID(identity),
		nsKeys:         make(map[string][]byte),
		packIndex:      idx,
		packWriter:     NewPackWriter(backend, identity, idx),
		planner:        newChunkSourcePlanner(),
		chunkPrefetch:  defaultChunkPrefetchLimit,
		remoteFetchSem: make(chan struct{}, defaultRemoteChunkFetchLimit),
	}
}

// NewWithDevice creates a Store with an explicit device ID (for multi-device scenarios).
func NewWithDevice(backend adapter.Backend, identity *DeviceKey, deviceID string) *Store {
	idx := NewPackIndex()
	return &Store{
		backend:        backend,
		identity:       identity,
		deviceID:       deviceID,
		nsKeys:         make(map[string][]byte),
		packIndex:      idx,
		packWriter:     NewPackWriter(backend, identity, idx),
		planner:        newChunkSourcePlanner(),
		chunkPrefetch:  defaultChunkPrefetchLimit,
		remoteFetchSem: make(chan struct{}, defaultRemoteChunkFetchLimit),
	}
}

// SetClient sets the client identifier embedded in ops (e.g. "cli/0.4.1").
func (s *Store) SetClient(client string) {
	s.clientID = client
}

// SetDevicePubKey sets the device's public key address for registry matching.
func (s *Store) SetDevicePubKey(pubkey string) {
	s.devicePubKey = pubkey
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

// Put encrypts and stores file data read from r at the given path.
// It streams through the data chunk by chunk, never holding more than
// one chunk (max 4MB) in memory.
func (s *Store) Put(ctx context.Context, path string, r io.Reader) error {
	namespace := s.namespaceFor(path)

	nsID, nsKey, err := s.resolveNamespaceState(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace state for %q: %w", namespace, err)
	}

	s.prevChecksum = "" // consume prev_checksum (no longer written to S3 ops)

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

		if s.backend == nil {
			if localBlobExists(nsID, chunk.Hash) {
				chunkHashes = append(chunkHashes, chunk.Hash)
				totalSize += int64(chunk.Length)
				continue
			}
		} else {
			_, headErr := s.backend.Head(ctx, s.blobKeyFor(chunk.Hash))
			if headErr == nil {
				chunkHashes = append(chunkHashes, chunk.Hash)
				totalSize += int64(chunk.Length)
				continue
			}
			if !errors.Is(headErr, adapter.ErrNotFound) {
				return fmt.Errorf("checking chunk %s: %w", chunk.Hash[:12], headErr)
			}
		}

		compressed := CompressChunk(chunk.Data)
		encrypted, err := Encrypt(compressed, fileKey)
		if err != nil {
			return fmt.Errorf("encrypting chunk %s: %w", chunk.Hash[:12], err)
		}

		blob := PrependBlobHeader(encrypted)

		if s.backend == nil {
			if err := writeLocalBlob(nsID, chunk.Hash, blob); err != nil {
				return fmt.Errorf("caching chunk %s locally: %w", chunk.Hash[:12], err)
			}
		} else if s.packing {
			packed, err := s.packWriter.Add(ctx, chunk.Hash, blob)
			if err != nil {
				return fmt.Errorf("packing chunk %s: %w", chunk.Hash[:12], err)
			}
			if !packed {
				cr := bytes.NewReader(blob)
				if err := s.backend.Put(ctx, s.blobKeyFor(chunk.Hash), cr, int64(len(blob))); err != nil {
					return fmt.Errorf("uploading chunk %s: %w", chunk.Hash[:12], err)
				}
			}
		} else {
			cr := bytes.NewReader(blob)
			if err := s.backend.Put(ctx, s.blobKeyFor(chunk.Hash), cr, int64(len(blob))); err != nil {
				return fmt.Errorf("uploading chunk %s: %w", chunk.Hash[:12], err)
			}
		}

		chunkHashes = append(chunkHashes, chunk.Hash)
		totalSize += int64(chunk.Length)
	}

	// File checksum = SHA3-256 of raw content (same as fileChecksum in scan.go)
	fileChecksum := hex.EncodeToString(contentHasher.Sum(nil))

	// No S3 op written — upload-then-record means the caller (outbox
	// worker) writes the local log entry after this returns.
	s.lastPut = &PutResult{
		Chunks:   chunkHashes,
		Checksum: fileChecksum,
		Size:     totalSize,
	}

	return nil
}

// GetChunks downloads and decrypts a file using known chunk hashes and namespace.
func (s *Store) GetChunks(ctx context.Context, chunks []string, namespace string, w io.Writer) error {
	return s.getChunksWithReuse(ctx, chunks, namespace, w, nil)
}

func (s *Store) getChunksWithReuse(ctx context.Context, chunks []string, namespace string, w io.Writer, reuse chunkReuseProvider) error {
	nsID, nsKey, err := s.resolveNamespaceState(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace state for %q: %w", namespace, err)
	}
	return s.downloadChunks(ctx, nsID, chunks, nsKey, w, reuse)
}

// --- Deprecated stubs: kept so skipped tests compile. Remove when tests are rewritten. ---

func (s *Store) Get(ctx context.Context, path string, w io.Writer) error {
	return fmt.Errorf("Store.Get removed: use GetChunks")
}
func (s *Store) List(ctx context.Context, prefix string) ([]ManifestEntry, error) {
	return nil, fmt.Errorf("Store.List removed")
}
func (s *Store) Remove(ctx context.Context, path string) error {
	return fmt.Errorf("Store.Remove removed")
}
func (s *Store) Info(ctx context.Context) (*StoreInfo, error) {
	return nil, fmt.Errorf("Store.Info removed")
}
func (s *Store) SaveSnapshot(ctx context.Context) error {
	return fmt.Errorf("Store.SaveSnapshot removed")
}
func (s *Store) getOpsLog(ctx context.Context) (*opslog.OpsLog, error) {
	return nil, fmt.Errorf("getOpsLog removed")
}
func (s *Store) writeOp(ctx context.Context, op *Op) error {
	return fmt.Errorf("writeOp removed")
}
func (s *Store) loadCurrentState(ctx context.Context) (*Manifest, error) {
	return nil, fmt.Errorf("loadCurrentState removed")
}

// downloadChunks fetches, decrypts, and writes chunks to w in order.
// Up to 3 chunks are fetched concurrently to overlap network I/O.
// Each chunk read has a 30-second idle timeout — if the S3 connection
// stalls, the reader is closed and the download fails (caller retries).
func (s *Store) downloadChunks(ctx context.Context, nsID string, chunks []string, nsKey []byte, w io.Writer, reuse chunkReuseProvider) error {
	if len(chunks) <= 1 {
		// Single-chunk fast path — no goroutine overhead.
		for i, hash := range chunks {
			plain, err := s.fetchChunk(ctx, nsID, i, hash, nsKey, reuse)
			if err != nil {
				return err
			}
			if _, err := w.Write(plain); err != nil {
				return fmt.Errorf("writing chunk %d: %w", i, err)
			}
		}
		return nil
	}

	ahead := s.chunkPrefetch
	if ahead <= 0 {
		ahead = 1
	}
	if ahead > len(chunks) {
		ahead = len(chunks)
	}

	type result struct {
		data []byte
		err  error
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Prefetch semaphore — limits in-flight fetches to bound memory.
	sem := make(chan struct{}, ahead)
	// One buffered result channel per chunk preserves ordering.
	slots := make([]chan result, len(chunks))
	for i := range slots {
		slots[i] = make(chan result, 1)
	}

	// Producer: acquire semaphore slots sequentially so goroutines
	// are launched in chunk order. This prevents out-of-order slot
	// acquisition which can deadlock the consumer.
	go func() {
		for i, hash := range chunks {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				// Fill remaining slots so consumer doesn't block.
				for j := i; j < len(chunks); j++ {
					slots[j] <- result{err: ctx.Err()}
				}
				return
			}
			i, hash := i, hash
			go func() {
				plain, err := s.fetchChunk(ctx, nsID, i, hash, nsKey, reuse)
				slots[i] <- result{data: plain, err: err}
				// Semaphore released by consumer, not here — keeps
				// backpressure tight so at most `ahead` chunks are buffered.
			}()
		}
	}()

	// Consume results in order and write to output.
	for i := range chunks {
		select {
		case r := <-slots[i]:
			<-sem // release prefetch slot after consuming
			if r.err != nil {
				return r.err
			}
			if _, err := w.Write(r.data); err != nil {
				return fmt.Errorf("writing chunk %d: %w", i, err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// fetchChunk resolves a single chunk through the local cache, peers, and/or
// backend, then decrypts, decompresses, and verifies its content hash.
func (s *Store) fetchChunk(ctx context.Context, nsID string, index int, chunkHash string, nsKey []byte, reuse chunkReuseProvider) ([]byte, error) {
	if reuse != nil {
		plaintext, ok, err := reuse.LookupChunk(chunkHash)
		if err != nil {
			return nil, fmt.Errorf("local reuse chunk %d (%s): %w", index, chunkHash[:12], err)
		}
		if ok {
			if s.onChunkRead != nil {
				s.onChunkRead(chunkSourceLocal)
			}
			return plaintext, nil
		}
	}

	sources := s.planChunkSources(chunkHash)
	if len(sources) == 0 {
		return nil, fmt.Errorf("chunk %d (%s): no source available", index, chunkHash[:12])
	}

	var attempts []string
	for _, source := range sources {
		raw, err := s.readRawChunkSource(ctx, nsID, index, chunkHash, source)
		if err != nil {
			if s.planner != nil {
				s.planner.recordFailure(source.kind, err)
			}
			attempts = append(attempts, fmt.Sprintf("%s: %v", source.kind, err))
			continue
		}

		plaintext, err := decodeChunkBlob(index, chunkHash, nsKey, raw)
		if err != nil {
			if s.planner != nil {
				s.planner.recordFailure(source.kind, err)
			}
			attempts = append(attempts, fmt.Sprintf("%s: %v", source.kind, err))
			continue
		}
		if s.planner != nil {
			s.planner.recordSuccess(source.kind)
		}

		if source.cacheOnSuccess {
			if err := writeLocalBlob(nsID, chunkHash, raw); err != nil {
				return nil, fmt.Errorf("caching %s chunk %d (%s): %w", source.kind, index, chunkHash[:12], err)
			}
		}
		if s.onChunkRead != nil {
			s.onChunkRead(source.kind)
		}
		return plaintext, nil
	}

	return nil, fmt.Errorf("chunk %d (%s): %s", index, chunkHash[:12], strings.Join(attempts, "; "))
}

func decodeChunkBlob(index int, chunkHash string, nsKey, raw []byte) ([]byte, error) {
	encrypted, _, err := StripBlobHeader(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing chunk %d header: %w", index, err)
	}

	fileKey, err := DeriveFileKey(nsKey, []byte(chunkHash))
	if err != nil {
		return nil, fmt.Errorf("deriving file key for chunk %d: %w", index, err)
	}

	compressed, err := Decrypt(encrypted, fileKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting chunk %d: %w", index, err)
	}

	plaintext, err := DecompressChunk(compressed)
	if err != nil {
		return nil, fmt.Errorf("decompressing chunk %d: %w", index, err)
	}

	if ContentHash(plaintext) != chunkHash {
		return nil, fmt.Errorf("chunk %d: hash mismatch (data corrupted)", index)
	}

	return plaintext, nil
}

func (s *Store) planChunkSources(chunkHash string) []chunkSourcePlan {
	sources := []chunkSourcePlan{{kind: chunkSourceLocal}}
	if s.peerChunks != nil {
		sources = append(sources, chunkSourcePlan{kind: chunkSourcePeer, cacheOnSuccess: true})
	}
	if s.backend != nil {
		if loc, ok := s.packIndex.Entries[chunkHash]; ok {
			_ = loc
			sources = append(sources, chunkSourcePlan{kind: chunkSourceS3Pack, cacheOnSuccess: true})
		} else {
			sources = append(sources, chunkSourcePlan{kind: chunkSourceS3Blob, cacheOnSuccess: true})
		}
	}
	if s.planner != nil {
		return s.planner.prioritize(sources)
	}
	return sources
}

func (s *Store) readRawChunkSource(ctx context.Context, nsID string, index int, chunkHash string, source chunkSourcePlan) ([]byte, error) {
	switch source.kind {
	case chunkSourceLocal:
		raw, err := readLocalBlob(nsID, chunkHash)
		if err != nil {
			return nil, err
		}
		return raw, nil
	case chunkSourcePeer:
		if s.peerChunks == nil {
			return nil, fmt.Errorf("peer chunk fetcher not configured")
		}
		raw, err := s.withRemoteFetchPermit(ctx, func() ([]byte, error) {
			return s.peerChunks.GetChunk(ctx, nsID, chunkHash)
		})
		if err != nil {
			return nil, fmt.Errorf("fetching peer chunk %d (%s): %w", index, chunkHash[:12], err)
		}
		return raw, nil
	case chunkSourceS3Pack:
		if s.backend == nil {
			return nil, fmt.Errorf("storage backend not configured")
		}
		loc, ok := s.packIndex.Entries[chunkHash]
		if !ok {
			return nil, fmt.Errorf("packed chunk metadata not found")
		}
		raw, err := s.withRemoteFetchPermit(ctx, func() ([]byte, error) {
			rc, err := s.backend.GetRange(ctx, loc.Pack, loc.Offset, loc.Length)
			if err != nil {
				return nil, err
			}
			tr := transfer.NewReader(rc, int64(loc.Length))
			tr.SetIdleTimeout(30 * time.Second)
			raw, err := io.ReadAll(tr)
			rc.Close()
			return raw, err
		})
		if err != nil {
			return nil, fmt.Errorf("reading packed chunk %d (%s): %w", index, chunkHash[:12], err)
		}
		return raw, nil
	case chunkSourceS3Blob:
		if s.backend == nil {
			return nil, fmt.Errorf("storage backend not configured")
		}
		blobKey := s.blobKeyFor(chunkHash)
		raw, err := s.withRemoteFetchPermit(ctx, func() ([]byte, error) {
			rc, err := s.backend.Get(ctx, blobKey)
			if err != nil {
				return nil, err
			}
			tr := transfer.NewReader(rc, -1)
			tr.SetIdleTimeout(30 * time.Second)
			raw, err := io.ReadAll(tr)
			rc.Close()
			return raw, err
		})
		if err != nil {
			return nil, fmt.Errorf("downloading chunk %d (%s): %w", index, chunkHash[:12], err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unknown chunk source %q", source.kind)
	}
}

func (s *Store) withRemoteFetchPermit(ctx context.Context, fn func() ([]byte, error)) ([]byte, error) {
	if fn == nil {
		return nil, fmt.Errorf("remote fetch function not configured")
	}
	if s == nil || s.remoteFetchSem == nil {
		return fn()
	}
	select {
	case s.remoteFetchSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.remoteFetchSem }()
	return fn()
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
// nsKeyName returns the S3 key name for an fs namespace.
// Prefixed with "fs:" to avoid collision with other modules (kv:, link:, etc).
func nsKeyName(namespace string) string {
	return "fs:" + namespace
}

func (s *Store) getOrCreateNamespaceKey(ctx context.Context, namespace string) ([]byte, error) {
	// Fast path: check in-memory cache under lock
	s.mu.Lock()
	if key, ok := s.nsKeys[namespace]; ok {
		s.mu.Unlock()
		return key, nil
	}
	s.mu.Unlock()

	if s.backend == nil {
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
		s.mu.Lock()
		s.nsKeys[namespace] = nsKey
		s.mu.Unlock()
		s.cacheNamespaceKey(namespace, nsKey)
		return nsKey, nil
	}

	// Slow path: S3 + disk I/O without holding the lock.
	// Multiple goroutines may race here for the same namespace — that's fine,
	// they'll all compute the same key and the last write to the cache wins.

	keyName := nsKeyName(namespace)
	deviceID := shortPubkeyID(s.identity.Address())
	keyPaths := []string{
		"keys/namespaces/" + keyName + "." + deviceID + ".ns.enc",
		"keys/namespaces/" + keyName + ".ns.enc",
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
		// Re-upload to S3 so other devices can access it
		wrapped, wErr := WrapNamespaceKey(cached, s.identity.PublicKey)
		if wErr == nil {
			keyPath := "keys/namespaces/" + keyName + ".ns.enc"
			r := bytes.NewReader(wrapped)
			s.backend.Put(ctx, keyPath, r, int64(len(wrapped)))
			s.wrapKeyForAllDevices(ctx, keyName, cached)
		}
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
	newKeyPath := "keys/namespaces/" + keyName + ".ns.enc"
	r := bytes.NewReader(wrapped)
	if err := s.backend.Put(ctx, newKeyPath, r, int64(len(wrapped))); err != nil {
		return nil, fmt.Errorf("storing namespace key: %w", err)
	}

	s.wrapKeyForAllDevices(ctx, keyName, nsKey)

	s.mu.Lock()
	s.nsKeys[namespace] = nsKey
	s.mu.Unlock()
	s.cacheNamespaceKey(namespace, nsKey)
	return nsKey, nil
}

func (s *Store) resolveNamespaceState(ctx context.Context, namespace string) (string, []byte, error) {
	nsKey, err := s.getOrCreateNamespaceKey(ctx, namespace)
	if err != nil {
		return "", nil, err
	}

	s.mu.Lock()
	storeNSID := s.nsID
	storeNamespace := s.namespace
	s.mu.Unlock()

	if storeNSID != "" && (storeNamespace == "" || storeNamespace == namespace) {
		return storeNSID, nsKey, nil
	}

	nsID, err := loadCachedNSID(namespace)
	if err != nil || nsID == "" {
		nsID, err = resolveNSID(ctx, s.backend, namespace, nsKey)
		if err != nil {
			return "", nil, err
		}
		cacheNSID(namespace, nsID)
	}
	return nsID, nsKey, nil
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

// cacheNamespaceKey writes the raw namespace key to local disk.
func (s *Store) cacheNamespaceKey(namespace string, key []byte) {
	dir, err := config.FSKeysDir(shortPubkeyID(s.identity.Address()))
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, nsKeyName(namespace)+".key")
	os.WriteFile(path, key, 0600)
}

// loadCachedNamespaceKey reads a locally cached namespace key.
func (s *Store) loadCachedNamespaceKey(namespace string) ([]byte, error) {
	dir, err := config.FSKeysDir(shortPubkeyID(s.identity.Address()))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, nsKeyName(namespace)+".key")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) != 32 {
		return nil, fmt.Errorf("cached key wrong size: %d", len(data))
	}
	return data, nil
}
