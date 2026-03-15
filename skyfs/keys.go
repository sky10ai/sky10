package skyfs

import (
	"crypto/ed25519"
	"fmt"
	"strings"

	"github.com/sky10/sky10/skykey"
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
	return skykey.GenerateSymmetricKey()
}

// WrapNamespaceKey encrypts a namespace key for a user's public key.
func WrapNamespaceKey(nsKey []byte, pub ed25519.PublicKey) ([]byte, error) {
	return skykey.WrapKey(nsKey, pub)
}

// UnwrapNamespaceKey decrypts a namespace key using the user's private key.
func UnwrapNamespaceKey(wrapped []byte, priv ed25519.PrivateKey) ([]byte, error) {
	return skykey.UnwrapKey(wrapped, priv)
}

// DeriveFileKey derives a deterministic file key from a namespace key and
// content hash using HKDF-SHA3-256.
func DeriveFileKey(nsKey []byte, contentHash []byte) ([]byte, error) {
	if len(nsKey) != skykey.KeySize {
		return nil, fmt.Errorf("invalid namespace key size: %d, want %d", len(nsKey), skykey.KeySize)
	}
	return skykey.DeriveKey(nsKey, contentHash, "sky10-file-key")
}
