package key

import (
	"bytes"
	"testing"
)

func TestBech32mRoundTrip(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20}

	encoded, err := Bech32mEncode("sky", 0, data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Should start with sky10
	if !hasPrefix(encoded, "sky10") {
		t.Errorf("encoded = %q, want sky10 prefix", encoded)
	}

	hrp, version, decoded, err := Bech32mDecode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if hrp != "sky" {
		t.Errorf("hrp = %q, want sky", hrp)
	}
	if version != 0 {
		t.Errorf("version = %d, want 0", version)
	}
	if !bytes.Equal(decoded, data) {
		t.Errorf("decoded doesn't match original")
	}
}

func TestBech32mVersionByte(t *testing.T) {
	t.Parallel()

	data := []byte{0xAA, 0xBB, 0xCC}

	for _, version := range []byte{0, 1, 5, 15, 31} {
		encoded, err := Bech32mEncode("sky", version, data)
		if err != nil {
			t.Fatalf("Encode v%d: %v", version, err)
		}

		_, gotVersion, decoded, err := Bech32mDecode(encoded)
		if err != nil {
			t.Fatalf("Decode v%d: %v", version, err)
		}
		if gotVersion != version {
			t.Errorf("version = %d, want %d", gotVersion, version)
		}
		if !bytes.Equal(decoded, data) {
			t.Errorf("v%d: data mismatch", version)
		}
	}
}

func TestBech32mInvalidVersion(t *testing.T) {
	t.Parallel()

	_, err := Bech32mEncode("sky", 32, []byte{0x01})
	if err == nil {
		t.Error("expected error for version > 31")
	}
}

func TestBech32mEmptyHRP(t *testing.T) {
	t.Parallel()

	_, err := Bech32mEncode("", 0, []byte{0x01})
	if err == nil {
		t.Error("expected error for empty HRP")
	}
}

func TestBech32mInvalidChecksum(t *testing.T) {
	t.Parallel()

	encoded, _ := Bech32mEncode("sky", 0, []byte{0x01, 0x02, 0x03})

	// Flip the last character
	tampered := encoded[:len(encoded)-1] + "q"
	if tampered == encoded {
		tampered = encoded[:len(encoded)-1] + "p"
	}

	_, _, _, err := Bech32mDecode(tampered)
	if err == nil {
		t.Error("expected error for invalid checksum")
	}
}

func TestBech32mInvalidCharacter(t *testing.T) {
	t.Parallel()

	_, _, _, err := Bech32mDecode("sky10qpzBBBBBBBBBB")
	if err == nil {
		t.Error("expected error for invalid character (uppercase)")
	}
}

func TestBech32mNoSeparator(t *testing.T) {
	t.Parallel()

	_, _, _, err := Bech32mDecode("skyqpzry")
	if err == nil {
		t.Error("expected error for missing separator")
	}
}

func TestBech32mDataTooShort(t *testing.T) {
	t.Parallel()

	_, _, _, err := Bech32mDecode("sky10qp")
	if err == nil {
		t.Error("expected error for data too short")
	}
}

func TestBech32m32ByteKey(t *testing.T) {
	t.Parallel()

	// Simulate a 32-byte Ed25519 public key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 7)
	}

	encoded, err := Bech32mEncode("sky", 0, key)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	hrp, version, decoded, err := Bech32mDecode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if hrp != "sky" || version != 0 || !bytes.Equal(decoded, key) {
		t.Error("32-byte key round-trip failed")
	}

	t.Logf("32-byte key address: %s (%d chars)", encoded, len(encoded))
}

func TestBech32mEmptyData(t *testing.T) {
	t.Parallel()

	encoded, err := Bech32mEncode("sky", 0, []byte{})
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}

	_, version, decoded, err := Bech32mDecode(encoded)
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if version != 0 || len(decoded) != 0 {
		t.Error("empty data round-trip failed")
	}
}

func TestBech32mDeterministic(t *testing.T) {
	t.Parallel()

	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	e1, _ := Bech32mEncode("sky", 0, data)
	e2, _ := Bech32mEncode("sky", 0, data)

	if e1 != e2 {
		t.Error("same input produced different encodings")
	}
}

func TestBech32mDifferentHRP(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02}
	e1, _ := Bech32mEncode("sky", 0, data)
	e2, _ := Bech32mEncode("test", 0, data)

	if e1 == e2 {
		t.Error("different HRP produced same encoding")
	}

	// Different HRP means different checksum — cross-decode should fail
	_, _, _, err := Bech32mDecode("sky" + e2[4:]) // replace hrp
	if err == nil {
		t.Error("expected checksum failure when swapping HRP")
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
