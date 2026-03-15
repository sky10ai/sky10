package skykey

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

	// Alice seals for Bob
	sealed, err := Seal([]byte("for bob"), bob.PublicKey)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Bob opens
	msg, err := Open(sealed, bob.PrivateKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(msg) != "for bob" {
		t.Errorf("got %q", msg)
	}

	// Alice cannot open
	_, err = Open(sealed, alice.PrivateKey)
	if err == nil {
		t.Error("alice should not be able to open bob's sealed message")
	}
}

func TestSealForAddress(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	addr := k.Address()

	sealed, err := SealFor([]byte("via address"), addr)
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

func TestSealTooShort(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	_, err := Open([]byte("short"), k.PrivateKey)
	if err == nil {
		t.Error("expected error for short sealed data")
	}
}

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

func TestEncryptDecrypt(t *testing.T) {
	t.Parallel()
	key, _ := GenerateSymmetricKey()
	msg := []byte("symmetric encryption test")

	ct, err := Encrypt(msg, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	pt, err := Decrypt(ct, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(pt, msg) {
		t.Error("decrypted doesn't match")
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
