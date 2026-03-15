package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

const manifestKey = "manifests/current.enc"

// Manifest maps file paths to their encrypted chunk references.
type Manifest struct {
	Version int                  `json:"version"`
	Created time.Time            `json:"created"`
	Updated time.Time            `json:"updated"`
	Tree    map[string]FileEntry `json:"tree"`
}

// FileEntry describes a stored file and its chunks.
type FileEntry struct {
	Chunks    []string  `json:"chunks"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
	Checksum  string    `json:"checksum"`
	Namespace string    `json:"namespace"`
}

// NewManifest creates an empty manifest.
func NewManifest() *Manifest {
	now := time.Now().UTC()
	return &Manifest{
		Version: 1,
		Created: now,
		Updated: now,
		Tree:    make(map[string]FileEntry),
	}
}

// Set adds or updates a file entry in the manifest.
func (m *Manifest) Set(path string, entry FileEntry) {
	m.Tree[path] = entry
	m.Updated = time.Now().UTC()
}

// Remove deletes a file entry from the manifest. Returns false if not found.
func (m *Manifest) Remove(path string) bool {
	if _, ok := m.Tree[path]; !ok {
		return false
	}
	delete(m.Tree, path)
	m.Updated = time.Now().UTC()
	return true
}

// ListPrefix returns all entries whose paths start with prefix, sorted by path.
func (m *Manifest) ListPrefix(prefix string) []ManifestEntry {
	var result []ManifestEntry
	for path, entry := range m.Tree {
		if strings.HasPrefix(path, prefix) {
			result = append(result, ManifestEntry{Path: path, FileEntry: entry})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

// ManifestEntry pairs a path with its FileEntry for listing.
type ManifestEntry struct {
	Path string
	FileEntry
}

// manifestKeyName returns the S3 key for the encrypted manifest key.
const manifestKeyEnc = "keys/manifest.key.enc"

// SaveManifest encrypts and uploads the manifest to the backend.
// The manifest is encrypted with a key derived from the user's identity.
func SaveManifest(ctx context.Context, backend adapter.Backend, m *Manifest, id *Identity) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}

	// Derive a deterministic manifest key from the identity
	manifestEncKey, err := deriveManifestKey(id)
	if err != nil {
		return fmt.Errorf("deriving manifest key: %w", err)
	}

	encrypted, err := Encrypt(data, manifestEncKey)
	if err != nil {
		return fmt.Errorf("encrypting manifest: %w", err)
	}

	r := bytes.NewReader(encrypted)
	if err := backend.Put(ctx, manifestKey, r, int64(len(encrypted))); err != nil {
		return fmt.Errorf("uploading manifest: %w", err)
	}

	return nil
}

// LoadManifest downloads and decrypts the manifest from the backend.
// Returns a new empty manifest if none exists yet.
func LoadManifest(ctx context.Context, backend adapter.Backend, id *Identity) (*Manifest, error) {
	rc, err := backend.Get(ctx, manifestKey)
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
			return NewManifest(), nil
		}
		return nil, fmt.Errorf("downloading manifest: %w", err)
	}
	defer rc.Close()

	encrypted, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	manifestEncKey, err := deriveManifestKey(id)
	if err != nil {
		return nil, fmt.Errorf("deriving manifest key: %w", err)
	}

	data, err := Decrypt(encrypted, manifestEncKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	if m.Tree == nil {
		m.Tree = make(map[string]FileEntry)
	}

	return &m, nil
}

// deriveManifestKey derives a deterministic encryption key for the manifest
// from the user's Ed25519 private key seed.
func deriveManifestKey(id *Identity) ([]byte, error) {
	seed := id.PrivateKey.Seed()
	return deriveKey(seed, []byte("sky10-manifest"), "sky10-manifest-key")
}
