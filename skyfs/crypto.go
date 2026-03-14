package skyfs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// KeySize is the size of an AES-256 key in bytes.
	KeySize = 32
	// NonceSize is the size of an AES-GCM nonce in bytes.
	NonceSize = 12
)

// GenerateKey creates a random 256-bit key for AES-256-GCM.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using a random nonce.
// The nonce is prepended to the ciphertext: [nonce | ciphertext+tag].
// This operates at the chunk level (max 4MB). Streaming happens above.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key size: %d, want %d", len(key), KeySize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	// nonce + ciphertext + tag
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts data produced by Encrypt. Expects [nonce | ciphertext+tag].
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key size: %d, want %d", len(key), KeySize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce := ciphertext[:nonceSize]
	ct := ciphertext[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}

	return plaintext, nil
}

// edToX25519Private converts an Ed25519 private key to an X25519 private key.
// Ed25519 private keys contain a 32-byte seed; the X25519 private key is
// derived by hashing that seed with SHA-512 (same as Ed25519 does internally).
func edToX25519Private(edPriv ed25519.PrivateKey) (*ecdh.PrivateKey, error) {
	seed := edPriv.Seed()
	h := sha256.Sum256(seed)
	return ecdh.X25519().NewPrivateKey(h[:])
}

// edToX25519Public converts an Ed25519 public key to an X25519 public key.
// This uses the birational map from the Edwards curve to the Montgomery curve.
//
// We do this by generating the X25519 public key from the private key's seed.
// This avoids implementing the Edwards-to-Montgomery point conversion directly.
func edToX25519Public(edPub ed25519.PublicKey, edPriv ed25519.PrivateKey) (*ecdh.PublicKey, error) {
	priv, err := edToX25519Private(edPriv)
	if err != nil {
		return nil, err
	}
	return priv.PublicKey(), nil
}

// WrapKey encrypts a data key so only the holder of recipientPriv can decrypt it.
//
// The wrapping uses ephemeral ECDH: generate a throwaway X25519 keypair,
// compute a shared secret with the recipient's public key, derive a wrapping
// key via HKDF, and encrypt the data key with AES-256-GCM.
//
// Output format: [32-byte ephemeral public key | AES-GCM wrapped data key]
func WrapKey(dataKey []byte, recipientPub ed25519.PublicKey, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	recipientX, err := edToX25519Public(recipientPub, recipientPriv)
	if err != nil {
		return nil, fmt.Errorf("converting recipient key: %w", err)
	}

	// Generate ephemeral X25519 keypair
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral key: %w", err)
	}

	// ECDH shared secret
	shared, err := ephemeral.ECDH(recipientX)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	// HKDF derive wrapping key
	wrappingKey, err := deriveKey(shared, ephemeral.PublicKey().Bytes(), "sky10-key-wrap")
	if err != nil {
		return nil, fmt.Errorf("deriving wrapping key: %w", err)
	}

	// Encrypt the data key
	wrapped, err := Encrypt(dataKey, wrappingKey)
	if err != nil {
		return nil, fmt.Errorf("wrapping key: %w", err)
	}

	// [ephemeral pub | wrapped key]
	out := make([]byte, 0, 32+len(wrapped))
	out = append(out, ephemeral.PublicKey().Bytes()...)
	out = append(out, wrapped...)
	return out, nil
}

// UnwrapKey decrypts a data key that was encrypted with WrapKey.
func UnwrapKey(wrapped []byte, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	if len(wrapped) < 32 {
		return nil, errors.New("wrapped key too short")
	}

	ephemeralPubBytes := wrapped[:32]
	encryptedKey := wrapped[32:]

	ephemeralPub, err := ecdh.X25519().NewPublicKey(ephemeralPubBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing ephemeral public key: %w", err)
	}

	recipientX, err := edToX25519Private(recipientPriv)
	if err != nil {
		return nil, fmt.Errorf("converting recipient key: %w", err)
	}

	// ECDH shared secret
	shared, err := recipientX.ECDH(ephemeralPub)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	// HKDF derive wrapping key (same params as WrapKey)
	wrappingKey, err := deriveKey(shared, ephemeralPubBytes, "sky10-key-wrap")
	if err != nil {
		return nil, fmt.Errorf("deriving wrapping key: %w", err)
	}

	dataKey, err := Decrypt(encryptedKey, wrappingKey)
	if err != nil {
		return nil, fmt.Errorf("unwrapping key: %w", err)
	}

	return dataKey, nil
}

// deriveKey uses HKDF-SHA256 to derive a 32-byte key.
func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	r := hkdf.New(sha256.New, secret, salt, []byte(info))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}
	return key, nil
}
