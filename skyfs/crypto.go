package skyfs

import (
	"crypto/ed25519"

	"github.com/sky10/sky10/skykey"
)

// Re-export skykey constants and functions used throughout skyfs.
// skyfs no longer has its own crypto — everything delegates to skykey.

const (
	KeySize   = skykey.KeySize
	NonceSize = skykey.NonceSize
)

func GenerateKey() ([]byte, error)                   { return skykey.GenerateSymmetricKey() }
func Encrypt(plaintext, key []byte) ([]byte, error)  { return skykey.Encrypt(plaintext, key) }
func Decrypt(ciphertext, key []byte) ([]byte, error) { return skykey.Decrypt(ciphertext, key) }

func WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error) {
	return skykey.WrapKey(dataKey, recipientPub)
}

func UnwrapKey(wrapped []byte, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	return skykey.UnwrapKey(wrapped, recipientPriv)
}

func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	return skykey.DeriveKey(secret, salt, info)
}
