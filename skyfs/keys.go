package skyfs

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// NamespaceFromPath derives a namespace name from a file path.
// The top-level directory becomes the namespace. Files at root use "default".
//
//	"journal/2026-03-14.md" → "journal"
//	"financial/reports/q4.pdf" → "financial"
//	"notes.md" → "default"
func NamespaceFromPath(path string) string {
	path = strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return "default"
}

// GenerateNamespaceKey creates a random AES-256 key for a namespace.
func GenerateNamespaceKey() ([]byte, error) {
	return GenerateKey()
}

// WrapNamespaceKey encrypts a namespace key for a user's keypair.
func WrapNamespaceKey(nsKey []byte, pub ed25519.PublicKey, priv ed25519.PrivateKey) ([]byte, error) {
	return WrapKey(nsKey, pub, priv)
}

// UnwrapNamespaceKey decrypts a namespace key using the user's private key.
func UnwrapNamespaceKey(wrapped []byte, priv ed25519.PrivateKey) ([]byte, error) {
	return UnwrapKey(wrapped, priv)
}

// DeriveFileKey derives a deterministic file key from a namespace key and
// content hash using HKDF. Same content in the same namespace always produces
// the same file key.
func DeriveFileKey(nsKey []byte, contentHash []byte) ([]byte, error) {
	if len(nsKey) != KeySize {
		return nil, fmt.Errorf("invalid namespace key size: %d, want %d", len(nsKey), KeySize)
	}
	r := hkdf.New(sha256.New, nsKey, contentHash, []byte("sky10-file-key"))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving file key: %w", err)
	}
	return key, nil
}
