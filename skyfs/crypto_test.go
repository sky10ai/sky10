package skyfs

import (
	"bytes"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	t.Parallel()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(key) != KeySize {
		t.Errorf("key length = %d, want %d", len(key), KeySize)
	}

	// Two keys should be different
	key2, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if bytes.Equal(key, key2) {
		t.Error("two generated keys are identical")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", []byte{}},
		{"short", []byte("hello sky10")},
		{"binary", []byte{0x00, 0xFF, 0x80, 0x01, 0xFE}},
		{"1KB", bytes.Repeat([]byte("x"), 1024)},
		{"1MB", bytes.Repeat([]byte("y"), 1024*1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, err := GenerateKey()
			if err != nil {
				t.Fatalf("GenerateKey: %v", err)
			}

			ciphertext, err := Encrypt(tt.plaintext, key)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}

			// Ciphertext should differ from plaintext
			if len(tt.plaintext) > 0 && bytes.Equal(ciphertext, tt.plaintext) {
				t.Error("ciphertext equals plaintext")
			}

			// Ciphertext should be longer (nonce + tag)
			if len(ciphertext) <= len(tt.plaintext) {
				t.Error("ciphertext is not longer than plaintext")
			}

			plaintext, err := Decrypt(ciphertext, key)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}

			if !bytes.Equal(plaintext, tt.plaintext) {
				t.Errorf("plaintext mismatch: got %d bytes, want %d bytes", len(plaintext), len(tt.plaintext))
			}
		})
	}
}

func TestDecryptWrongKey(t *testing.T) {
	t.Parallel()

	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	ciphertext, err := Encrypt([]byte("secret"), key1)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = Decrypt(ciphertext, key2)
	if err == nil {
		t.Error("expected error decrypting with wrong key")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	t.Parallel()

	key, _ := GenerateKey()
	ciphertext, err := Encrypt([]byte("secret"), key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the ciphertext (after nonce)
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[NonceSize+1] ^= 0xFF

	_, err = Decrypt(tampered, key)
	if err == nil {
		t.Error("expected error decrypting tampered ciphertext")
	}
}

func TestEncryptInvalidKeySize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  []byte
	}{
		{"too short", make([]byte, 16)},
		{"too long", make([]byte, 64)},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Encrypt([]byte("data"), tt.key)
			if err == nil {
				t.Error("expected error for invalid key size")
			}
		})
	}
}

func TestDecryptTooShort(t *testing.T) {
	t.Parallel()

	key, _ := GenerateKey()
	_, err := Decrypt([]byte("short"), key)
	if err == nil {
		t.Error("expected error for ciphertext shorter than nonce")
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	t.Parallel()

	key, _ := GenerateKey()
	plaintext := []byte("same input")

	ct1, _ := Encrypt(plaintext, key)
	ct2, _ := Encrypt(plaintext, key)

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext produced identical ciphertext (nonce reuse)")
	}
}

func TestWrapUnwrapKey(t *testing.T) {
	t.Parallel()

	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}

	dataKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	wrapped, err := WrapKey(dataKey, id.PublicKey, id.PrivateKey)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	// Wrapped key should not contain the plaintext data key
	if bytes.Contains(wrapped, dataKey) {
		t.Error("wrapped output contains plaintext data key")
	}

	unwrapped, err := UnwrapKey(wrapped, id.PrivateKey)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}

	if !bytes.Equal(unwrapped, dataKey) {
		t.Error("unwrapped key does not match original")
	}
}

func TestWrapUnwrapDifferentIdentities(t *testing.T) {
	t.Parallel()

	id1, _ := GenerateIdentity()
	id2, _ := GenerateIdentity()

	dataKey, _ := GenerateKey()

	wrapped, err := WrapKey(dataKey, id1.PublicKey, id1.PrivateKey)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	// Wrong private key should fail to unwrap
	_, err = UnwrapKey(wrapped, id2.PrivateKey)
	if err == nil {
		t.Error("expected error unwrapping with wrong private key")
	}
}

func TestUnwrapKeyTooShort(t *testing.T) {
	t.Parallel()

	id, _ := GenerateIdentity()
	_, err := UnwrapKey([]byte("short"), id.PrivateKey)
	if err == nil {
		t.Error("expected error for short wrapped key")
	}
}

func TestDeriveKey(t *testing.T) {
	t.Parallel()

	secret := []byte("shared secret")
	salt := []byte("salt value")

	key1, err := deriveKey(secret, salt, "info1")
	if err != nil {
		t.Fatalf("deriveKey: %v", err)
	}
	if len(key1) != KeySize {
		t.Errorf("derived key length = %d, want %d", len(key1), KeySize)
	}

	// Same inputs produce same output
	key2, _ := deriveKey(secret, salt, "info1")
	if !bytes.Equal(key1, key2) {
		t.Error("same inputs produced different keys")
	}

	// Different info produces different output
	key3, _ := deriveKey(secret, salt, "info2")
	if bytes.Equal(key1, key3) {
		t.Error("different info strings produced same key")
	}
}
