package skyfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sky10/sky10/skyadapter"
)

// ErrFileNotFound is returned when a requested file path does not exist
// in the manifest.
var ErrFileNotFound = errors.New("file not found")

// Store provides encrypted file storage. It encrypts files locally, uploads
// encrypted chunks to a storage backend, and tracks file metadata in an
// encrypted manifest.
type Store struct {
	backend  skyadapter.Backend
	identity *Identity

	mu     sync.Mutex
	nsKeys map[string][]byte // cached namespace keys
}

// New creates a Store backed by the given storage backend and identity.
func New(backend skyadapter.Backend, identity *Identity) *Store {
	return &Store{
		backend:  backend,
		identity: identity,
		nsKeys:   make(map[string][]byte),
	}
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

		// Derive file key from namespace key + chunk content hash
		hashBytes := []byte(chunk.Hash)
		fileKey, err := DeriveFileKey(nsKey, hashBytes)
		if err != nil {
			return fmt.Errorf("deriving file key: %w", err)
		}

		// Check if chunk already exists (dedup)
		_, headErr := s.backend.Head(ctx, chunk.BlobKey())
		if headErr == nil {
			// Already exists, skip upload
			chunkHashes = append(chunkHashes, chunk.Hash)
			totalSize += int64(chunk.Length)
			continue
		}
		if !errors.Is(headErr, skyadapter.ErrNotFound) {
			return fmt.Errorf("checking chunk %s: %w", chunk.Hash[:12], headErr)
		}

		// Encrypt and upload chunk
		encrypted, err := Encrypt(chunk.Data, fileKey)
		if err != nil {
			return fmt.Errorf("encrypting chunk %s: %w", chunk.Hash[:12], err)
		}

		cr := bytes.NewReader(encrypted)
		if err := s.backend.Put(ctx, chunk.BlobKey(), cr, int64(len(encrypted))); err != nil {
			return fmt.Errorf("uploading chunk %s: %w", chunk.Hash[:12], err)
		}

		chunkHashes = append(chunkHashes, chunk.Hash)
		totalSize += int64(chunk.Length)
	}

	// Compute overall file checksum from ordered chunk hashes
	allHashes := ""
	for _, h := range chunkHashes {
		allHashes += h
	}
	fileChecksum := ContentHash([]byte(allHashes))

	// Update manifest
	manifest, err := LoadManifest(ctx, s.backend, s.identity)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	manifest.Set(path, FileEntry{
		Chunks:    chunkHashes,
		Size:      totalSize,
		Modified:  time.Now().UTC(),
		Checksum:  fileChecksum,
		Namespace: namespace,
	})

	if err := SaveManifest(ctx, s.backend, manifest, s.identity); err != nil {
		return fmt.Errorf("saving manifest: %w", err)
	}

	return nil
}

// Get retrieves and decrypts a file, streaming the plaintext to w.
// It processes one chunk at a time, never holding more than 4MB in memory.
func (s *Store) Get(ctx context.Context, path string, w io.Writer) error {
	manifest, err := LoadManifest(ctx, s.backend, s.identity)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	entry, ok := manifest.Tree[path]
	if !ok {
		return ErrFileNotFound
	}

	nsKey, err := s.getOrCreateNamespaceKey(ctx, entry.Namespace)
	if err != nil {
		return fmt.Errorf("namespace key for %q: %w", entry.Namespace, err)
	}

	for i, chunkHash := range entry.Chunks {
		blobKey := (&Chunk{Hash: chunkHash}).BlobKey()

		rc, err := s.backend.Get(ctx, blobKey)
		if err != nil {
			return fmt.Errorf("downloading chunk %d (%s): %w", i, chunkHash[:12], err)
		}

		encrypted, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading chunk %d: %w", i, err)
		}

		fileKey, err := DeriveFileKey(nsKey, []byte(chunkHash))
		if err != nil {
			return fmt.Errorf("deriving file key for chunk %d: %w", i, err)
		}

		plaintext, err := Decrypt(encrypted, fileKey)
		if err != nil {
			return fmt.Errorf("decrypting chunk %d: %w", i, err)
		}

		// Verify chunk hash
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
	manifest, err := LoadManifest(ctx, s.backend, s.identity)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	return manifest.ListPrefix(prefix), nil
}

// Remove deletes a file entry from the manifest. It does not delete the
// underlying blobs (they may be shared via dedup). Blob garbage collection
// is a separate concern.
func (s *Store) Remove(ctx context.Context, path string) error {
	manifest, err := LoadManifest(ctx, s.backend, s.identity)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	if !manifest.Remove(path) {
		return ErrFileNotFound
	}

	if err := SaveManifest(ctx, s.backend, manifest, s.identity); err != nil {
		return fmt.Errorf("saving manifest: %w", err)
	}

	return nil
}

// Info returns summary information about the store.
func (s *Store) Info(ctx context.Context) (*StoreInfo, error) {
	manifest, err := LoadManifest(ctx, s.backend, s.identity)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}

	info := &StoreInfo{
		ID:        s.identity.ID(),
		FileCount: len(manifest.Tree),
	}

	namespaces := make(map[string]bool)
	for _, entry := range manifest.Tree {
		info.TotalSize += entry.Size
		namespaces[entry.Namespace] = true
	}
	for ns := range namespaces {
		info.Namespaces = append(info.Namespaces, ns)
	}

	return info, nil
}

// StoreInfo contains summary information about a Store.
type StoreInfo struct {
	ID         string
	FileCount  int
	TotalSize  int64
	Namespaces []string
}

// getOrCreateNamespaceKey returns the namespace key, loading from S3 or
// creating a new one if it doesn't exist yet.
func (s *Store) getOrCreateNamespaceKey(ctx context.Context, namespace string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check cache
	if key, ok := s.nsKeys[namespace]; ok {
		return key, nil
	}

	nsKeyPath := "keys/namespaces/" + namespace + ".ns.enc"

	// Try to load from backend
	rc, err := s.backend.Get(ctx, nsKeyPath)
	if err == nil {
		defer rc.Close()
		wrapped, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("reading namespace key: %w", err)
		}
		nsKey, err := UnwrapNamespaceKey(wrapped, s.identity.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("unwrapping namespace key: %w", err)
		}
		s.nsKeys[namespace] = nsKey
		return nsKey, nil
	}
	if !errors.Is(err, skyadapter.ErrNotFound) {
		return nil, fmt.Errorf("loading namespace key: %w", err)
	}

	// Create new namespace key
	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		return nil, fmt.Errorf("generating namespace key: %w", err)
	}

	wrapped, err := WrapNamespaceKey(nsKey, s.identity.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("wrapping namespace key: %w", err)
	}

	r := bytes.NewReader(wrapped)
	if err := s.backend.Put(ctx, nsKeyPath, r, int64(len(wrapped))); err != nil {
		return nil, fmt.Errorf("storing namespace key: %w", err)
	}

	s.nsKeys[namespace] = nsKey
	return nsKey, nil
}
