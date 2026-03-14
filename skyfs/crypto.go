package skyfs

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

// edPubToX25519 converts an Ed25519 public key to an X25519 public key
// using the birational map from the Edwards curve to the Montgomery curve.
//
// This is the standard conversion: given an Edwards point (x, y), the
// Montgomery u-coordinate is u = (1 + y) / (1 - y).
//
// Uses filippo.io/edwards25519 for the field arithmetic.
func edPubToX25519(edPub ed25519.PublicKey) (*ecdh.PublicKey, error) {
	p, err := new(edwards25519.Point).SetBytes(edPub)
	if err != nil {
		return nil, fmt.Errorf("invalid Ed25519 public key: %w", err)
	}
	return ecdh.X25519().NewPublicKey(p.BytesMontgomery())
}

// edPrivToX25519 converts an Ed25519 private key to an X25519 private key.
// Uses SHA-512 of the seed and applies X25519 clamping, matching the standard
// Ed25519-to-X25519 conversion (RFC 7748 / draft-ietf-core-oscore).
//
// Note: SHA-512 is required here by the Ed25519 spec (RFC 8032). This is the
// one place where the hash algorithm is not our choice.
func edPrivToX25519(edPriv ed25519.PrivateKey) (*ecdh.PrivateKey, error) {
	seed := edPriv.Seed()
	h := sha512sum(seed)
	// Apply X25519 clamping to the first 32 bytes
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	return ecdh.X25519().NewPrivateKey(h[:32])
}

// WrapKey encrypts a data key so only the holder of the corresponding
// private key can decrypt it. Only the recipient's public key is needed.
//
// The wrapping uses ephemeral ECDH: generate a throwaway X25519 keypair,
// compute a shared secret with the recipient's public key (converted from
// Ed25519 to X25519 via the birational map), derive a wrapping key via
// HKDF-SHA3-256, and encrypt the data key with AES-256-GCM.
//
// Output format: [32-byte ephemeral public key | AES-GCM wrapped data key]
func WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error) {
	recipientX, err := edPubToX25519(recipientPub)
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

	// HKDF-SHA3-256 derive wrapping key
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

	recipientX, err := edPrivToX25519(recipientPriv)
	if err != nil {
		return nil, fmt.Errorf("converting recipient key: %w", err)
	}

	// ECDH shared secret
	shared, err := recipientX.ECDH(ephemeralPub)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}

	// HKDF-SHA3-256 derive wrapping key (same params as WrapKey)
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

// sha512sum computes SHA-512 and returns the full 64-byte hash.
// Used only for Ed25519→X25519 conversion (required by RFC 8032).
func sha512sum(data []byte) []byte {
	h := cryptosha512.Sum512(data)
	return h[:]
}

// newSHA3256 returns a new SHA3-256 hash as a hash.Hash interface.
func newSHA3256() hash.Hash { return sha3.New256() }

// deriveKey uses HKDF-SHA3-256 to derive a 32-byte key.
// SHA-3 (Keccak sponge construction) provides stronger collision resistance
// than SHA-2 and is immune to length-extension attacks by design.
func deriveKey(secret, salt []byte, info string) ([]byte, error) {
	r := hkdf.New(newSHA3256, secret, salt, []byte(info))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}
	return key, nil
}
