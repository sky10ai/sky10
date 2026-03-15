package fs

import (
	"crypto/ed25519"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Re-export skykey constants and functions used throughout fs.
// fs has no direct crypto — everything delegates to skykey.

const (
	KeySize   = skykey.KeySize
	NonceSize = skykey.NonceSize
)

func GenerateKey() ([]byte, error)                      { return skykey.GenerateSymmetricKey() }
func Encrypt(plaintext, encKey []byte) ([]byte, error)  { return skykey.Encrypt(plaintext, encKey) }
func Decrypt(ciphertext, encKey []byte) ([]byte, error) { return skykey.Decrypt(ciphertext, encKey) }

func WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error) {
	return skykey.WrapKey(dataKey, recipientPub)
}

func UnwrapKey(wrapped []byte, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	return skykey.UnwrapKey(wrapped, recipientPriv)
}

func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	return skykey.DeriveKey(secret, salt, info)
}
