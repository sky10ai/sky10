package skykey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha3"
	cryptosha512 "crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"io"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/hkdf"
)

const (
	// KeySize is the size of an AES-256 key in bytes.
	KeySize = 32
	// NonceSize is the size of an AES-GCM nonce.
	NonceSize = 12
	// EphemeralPubSize is the size of an X25519 public key.
	EphemeralPubSize = 32
)

// Seal encrypts a message so only the holder of the recipient's private
// key can decrypt it. Uses ephemeral ECDH (X25519) + HKDF-SHA3-256 +
// AES-256-GCM.
//
// Output: [ephemeral_pub (32) | nonce (12) | ciphertext + auth_tag]
func Seal(message []byte, recipientPub ed25519.PublicKey) ([]byte, error) {
	recipientX, err := edPubToX25519(recipientPub)
	if err != nil {
		return nil, fmt.Errorf("converting recipient key: %w", err)
	}

	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral key: %w", err)
	}

	shared, err := ephemeral.ECDH(recipientX)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	encKey, err := deriveKey(shared, ephemeral.PublicKey().Bytes(), "sky10-seal")
	if err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, message, nil)

	// [ephemeral_pub | nonce | ciphertext+tag]
	out := make([]byte, 0, EphemeralPubSize+len(nonce)+len(ciphertext))
	out = append(out, ephemeral.PublicKey().Bytes()...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// Open decrypts a message that was sealed for the recipient.
func Open(sealed []byte, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	minLen := EphemeralPubSize + NonceSize + 16 // 32 + 12 + tag
	if len(sealed) < minLen {
		return nil, errors.New("sealed data too short")
	}

	ephPubBytes := sealed[:EphemeralPubSize]
	nonce := sealed[EphemeralPubSize : EphemeralPubSize+NonceSize]
	ciphertext := sealed[EphemeralPubSize+NonceSize:]

	ephPub, err := ecdh.X25519().NewPublicKey(ephPubBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing ephemeral key: %w", err)
	}

	recipientX, err := edPrivToX25519(recipientPriv)
	if err != nil {
		return nil, fmt.Errorf("converting recipient key: %w", err)
	}

	shared, err := recipientX.ECDH(ephPub)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	encKey, err := deriveKey(shared, ephPubBytes, "sky10-seal")
	if err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	return plaintext, nil
}

// SealFor encrypts a message for a sky10q... address.
func SealFor(message []byte, address string) ([]byte, error) {
	key, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}
	return Seal(message, key.PublicKey)
}

// Encrypt encrypts plaintext with AES-256-GCM using a symmetric key.
// The nonce is prepended: [nonce | ciphertext+tag].
func Encrypt(plaintext, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key size: %d, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts data produced by Encrypt.
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key size: %d, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}

// GenerateSymmetricKey creates a random 256-bit key.
func GenerateSymmetricKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapKey encrypts a symmetric key for a recipient's public key.
// Convenience wrapper around Seal for 32-byte keys.
func WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error) {
	return Seal(dataKey, recipientPub)
}

// UnwrapKey decrypts a symmetric key sealed for the recipient.
func UnwrapKey(wrapped []byte, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	return Open(wrapped, recipientPriv)
}

// DeriveKey uses HKDF-SHA3-256 to derive a key from a secret.
func DeriveKey(secret, salt []byte, info string) ([]byte, error) {
	return deriveKey(secret, salt, info)
}

// --- internal helpers ---

func edPubToX25519(edPub ed25519.PublicKey) (*ecdh.PublicKey, error) {
	p, err := new(edwards25519.Point).SetBytes(edPub)
	if err != nil {
		return nil, fmt.Errorf("invalid Ed25519 public key: %w", err)
	}
	return ecdh.X25519().NewPublicKey(p.BytesMontgomery())
}

func edPrivToX25519(edPriv ed25519.PrivateKey) (*ecdh.PrivateKey, error) {
	seed := edPriv.Seed()
	h := cryptosha512.Sum512(seed)
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	return ecdh.X25519().NewPrivateKey(h[:32])
}

func newSHA3256() hash.Hash { return sha3.New256() }

func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	r := hkdf.New(newSHA3256, secret, salt, []byte(info))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}
