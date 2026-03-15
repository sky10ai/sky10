package key

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	tests := []struct {
		name string
		msg  []byte
	}{
		{"empty", []byte{}},
		{"short", []byte("hello sky10")},
		{"binary", []byte{0x00, 0xFF, 0x80}},
		{"1KB", bytes.Repeat([]byte("x"), 1024)},
		{"1MB", bytes.Repeat([]byte("y"), 1024*1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sealed, err := Seal(tt.msg, k.PublicKey)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			opened, err := Open(sealed, k.PrivateKey)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(opened, tt.msg) {
				t.Error("decrypted doesn't match original")
			}
		})
	}
}

func TestSealWrongKey(t *testing.T) {
	t.Parallel()
	alice, _ := Generate()
	bob, _ := Generate()

	sealed, _ := Seal([]byte("secret"), alice.PublicKey)
	_, err := Open(sealed, bob.PrivateKey)
	if err == nil {
		t.Error("wrong key should fail")
	}
}

func TestSealNonDeterministic(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	s1, _ := Seal([]byte("same"), k.PublicKey)
	s2, _ := Seal([]byte("same"), k.PublicKey)

	if bytes.Equal(s1, s2) {
		t.Error("same plaintext should produce different sealed output")
	}
}

func TestSealCrossIdentity(t *testing.T) {
	t.Parallel()
	alice, _ := Generate()
	bob, _ := Generate()

	sealed, err := Seal([]byte("for bob"), bob.PublicKey)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	msg, err := Open(sealed, bob.PrivateKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(msg) != "for bob" {
		t.Errorf("got %q", msg)
	}

	_, err = Open(sealed, alice.PrivateKey)
	if err == nil {
		t.Error("alice should not be able to open bob's sealed message")
	}
}

func TestSealForAddress(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	sealed, err := SealFor([]byte("via address"), k.Address())
	if err != nil {
		t.Fatalf("SealFor: %v", err)
	}

	msg, err := Open(sealed, k.PrivateKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(msg) != "via address" {
		t.Errorf("got %q", msg)
	}
}

func TestSealForInvalidAddress(t *testing.T) {
	t.Parallel()
	_, err := SealFor([]byte("msg"), "not-an-address")
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestSealTooShort(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	_, err := Open([]byte("short"), k.PrivateKey)
	if err == nil {
		t.Error("expected error for short sealed data")
	}
}

func TestSealTamperedCiphertext(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	sealed, _ := Seal([]byte("original"), k.PublicKey)

	tampered := make([]byte, len(sealed))
	copy(tampered, sealed)
	// Flip a byte in the ciphertext (after ephemeral pub + nonce)
	tampered[EphemeralPubSize+NonceSize+1] ^= 0xFF

	_, err := Open(tampered, k.PrivateKey)
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}

func TestSealOutputDoesNotContainPlaintext(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	secret := []byte("this is highly sensitive plaintext data")

	sealed, _ := Seal(secret, k.PublicKey)

	if bytes.Contains(sealed, secret) {
		t.Error("sealed output contains plaintext")
	}
}

// --- WrapKey ---

func TestWrapUnwrap(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	dataKey, _ := GenerateSymmetricKey()

	wrapped, err := WrapKey(dataKey, k.PublicKey)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	unwrapped, err := UnwrapKey(wrapped, k.PrivateKey)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}

	if !bytes.Equal(unwrapped, dataKey) {
		t.Error("unwrapped key doesn't match")
	}
}

func TestWrapOutputDoesNotContainPlaintext(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	dataKey, _ := GenerateSymmetricKey()

	wrapped, _ := WrapKey(dataKey, k.PublicKey)

	if bytes.Contains(wrapped, dataKey) {
		t.Error("wrapped output contains plaintext key")
	}
}

func TestWrapCrossIdentity(t *testing.T) {
	t.Parallel()
	alice, _ := Generate()
	bob, _ := Generate()
	dataKey, _ := GenerateSymmetricKey()

	// Wrap for Bob
	wrapped, _ := WrapKey(dataKey, bob.PublicKey)

	// Bob unwraps
	unwrapped, err := UnwrapKey(wrapped, bob.PrivateKey)
	if err != nil {
		t.Fatalf("Bob UnwrapKey: %v", err)
	}
	if !bytes.Equal(unwrapped, dataKey) {
		t.Error("Bob got wrong key")
	}

	// Alice cannot unwrap
	_, err = UnwrapKey(wrapped, alice.PrivateKey)
	if err == nil {
		t.Error("Alice should not unwrap Bob's key")
	}
}

func TestWrapSameKeyForMultiple(t *testing.T) {
	t.Parallel()
	alice, _ := Generate()
	bob, _ := Generate()
	dataKey, _ := GenerateSymmetricKey()

	wrappedA, _ := WrapKey(dataKey, alice.PublicKey)
	wrappedB, _ := WrapKey(dataKey, bob.PublicKey)

	unwrappedA, _ := UnwrapKey(wrappedA, alice.PrivateKey)
	unwrappedB, _ := UnwrapKey(wrappedB, bob.PrivateKey)

	if !bytes.Equal(unwrappedA, dataKey) || !bytes.Equal(unwrappedB, dataKey) {
		t.Error("both should unwrap to the same key")
	}
}

// --- Symmetric Encrypt/Decrypt ---

func TestEncryptDecrypt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  []byte
	}{
		{"empty", []byte{}},
		{"short", []byte("hello")},
		{"binary", []byte{0x00, 0xFF, 0x80, 0x01, 0xFE}},
		{"1KB", bytes.Repeat([]byte("x"), 1024)},
		{"1MB", bytes.Repeat([]byte("y"), 1024*1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k, _ := GenerateSymmetricKey()
			ct, err := Encrypt(tt.msg, k)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			pt, err := Decrypt(ct, k)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(pt, tt.msg) {
				t.Error("decrypted doesn't match")
			}
		})
	}
}

func TestEncryptWrongKey(t *testing.T) {
	t.Parallel()
	k1, _ := GenerateSymmetricKey()
	k2, _ := GenerateSymmetricKey()

	ct, _ := Encrypt([]byte("secret"), k1)
	_, err := Decrypt(ct, k2)
	if err == nil {
		t.Error("wrong key should fail")
	}
}

func TestEncryptTamperedCiphertext(t *testing.T) {
	t.Parallel()
	k, _ := GenerateSymmetricKey()
	ct, _ := Encrypt([]byte("data"), k)

	tampered := make([]byte, len(ct))
	copy(tampered, ct)
	tampered[NonceSize+1] ^= 0xFF

	_, err := Decrypt(tampered, k)
	if err == nil {
		t.Error("expected error for tampered ciphertext")
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
	k, _ := GenerateSymmetricKey()
	_, err := Decrypt([]byte("x"), k)
	if err == nil {
		t.Error("expected error for ciphertext shorter than nonce")
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	t.Parallel()
	k, _ := GenerateSymmetricKey()
	msg := []byte("same input")

	ct1, _ := Encrypt(msg, k)
	ct2, _ := Encrypt(msg, k)

	if bytes.Equal(ct1, ct2) {
		t.Error("same plaintext should produce different ciphertext (nonce reuse)")
	}
}

func TestEncryptOutputLongerThanInput(t *testing.T) {
	t.Parallel()
	k, _ := GenerateSymmetricKey()
	msg := []byte("data")

	ct, _ := Encrypt(msg, k)
	if len(ct) <= len(msg) {
		t.Error("ciphertext should be longer than plaintext (nonce + tag)")
	}
}

// --- GenerateSymmetricKey ---

func TestGenerateSymmetricKey(t *testing.T) {
	t.Parallel()

	k1, err := GenerateSymmetricKey()
	if err != nil {
		t.Fatalf("GenerateSymmetricKey: %v", err)
	}
	if len(k1) != KeySize {
		t.Errorf("key length = %d, want %d", len(k1), KeySize)
	}

	k2, _ := GenerateSymmetricKey()
	if bytes.Equal(k1, k2) {
		t.Error("two generated keys are identical")
	}
}

// --- DeriveKey ---

func TestDeriveKey(t *testing.T) {
	t.Parallel()

	k1, _ := DeriveKey([]byte("secret"), []byte("salt"), "info1")
	k2, _ := DeriveKey([]byte("secret"), []byte("salt"), "info1")
	k3, _ := DeriveKey([]byte("secret"), []byte("salt"), "info2")

	if !bytes.Equal(k1, k2) {
		t.Error("same inputs should produce same key")
	}
	if bytes.Equal(k1, k3) {
		t.Error("different info should produce different key")
	}
	if len(k1) != KeySize {
		t.Errorf("key length = %d, want %d", len(k1), KeySize)
	}
}

func TestDeriveKeyDifferentSalt(t *testing.T) {
	t.Parallel()

	k1, _ := DeriveKey([]byte("secret"), []byte("salt-a"), "info")
	k2, _ := DeriveKey([]byte("secret"), []byte("salt-b"), "info")

	if bytes.Equal(k1, k2) {
		t.Error("different salt should produce different key")
	}
}

func TestDeriveKeyDifferentSecret(t *testing.T) {
	t.Parallel()

	k1, _ := DeriveKey([]byte("secret-a"), []byte("salt"), "info")
	k2, _ := DeriveKey([]byte("secret-b"), []byte("salt"), "info")

	if bytes.Equal(k1, k2) {
		t.Error("different secret should produce different key")
	}
}

func TestDeriveKeyEmptyInputs(t *testing.T) {
	t.Parallel()

	// Should not panic or error on empty inputs
	k, err := DeriveKey([]byte{}, []byte{}, "")
	if err != nil {
		t.Fatalf("DeriveKey with empty inputs: %v", err)
	}
	if len(k) != KeySize {
		t.Errorf("key length = %d, want %d", len(k), KeySize)
	}
}
