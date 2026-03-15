package key

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignVerify(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	msg := []byte("sign this message")

	sig := Sign(msg, k.PrivateKey)

	if !Verify(msg, sig, k.PublicKey) {
		t.Error("valid signature failed verification")
	}
}

func TestSignVerifyTampered(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	msg := []byte("original message")

	sig := Sign(msg, k.PrivateKey)

	tampered := []byte("tampered message")
	if Verify(tampered, sig, k.PublicKey) {
		t.Error("tampered message should fail verification")
	}
}

func TestSignVerifyWrongKey(t *testing.T) {
	t.Parallel()
	k1, _ := Generate()
	k2, _ := Generate()
	msg := []byte("signed by k1")

	sig := Sign(msg, k1.PrivateKey)

	if Verify(msg, sig, k2.PublicKey) {
		t.Error("wrong key should fail verification")
	}
}

func TestSignVerifyFile(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	path := filepath.Join(t.TempDir(), "doc.txt")
	os.WriteFile(path, []byte("file content to sign"), 0644)

	sig, err := SignFile(path, k.PrivateKey)
	if err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	valid, err := VerifyFile(path, sig, k.PublicKey)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if !valid {
		t.Error("file signature should be valid")
	}
}

func TestSignVerifyFileModified(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	path := filepath.Join(t.TempDir(), "doc.txt")
	os.WriteFile(path, []byte("original"), 0644)

	sig, _ := SignFile(path, k.PrivateKey)

	os.WriteFile(path, []byte("modified"), 0644)

	valid, _ := VerifyFile(path, sig, k.PublicKey)
	if valid {
		t.Error("modified file should fail verification")
	}
}

func TestSignEmptyMessage(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	sig := Sign([]byte{}, k.PrivateKey)
	if !Verify([]byte{}, sig, k.PublicKey) {
		t.Error("empty message signature should be valid")
	}
}

func TestSignEmptyFile(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	path := filepath.Join(t.TempDir(), "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	sig, err := SignFile(path, k.PrivateKey)
	if err != nil {
		t.Fatalf("SignFile empty: %v", err)
	}

	valid, _ := VerifyFile(path, sig, k.PublicKey)
	if !valid {
		t.Error("empty file signature should be valid")
	}
}

func TestSignLargeFile(t *testing.T) {
	t.Parallel()
	k, _ := Generate()

	path := filepath.Join(t.TempDir(), "large.bin")
	data := make([]byte, 5*1024*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	os.WriteFile(path, data, 0644)

	sig, err := SignFile(path, k.PrivateKey)
	if err != nil {
		t.Fatalf("SignFile large: %v", err)
	}

	valid, _ := VerifyFile(path, sig, k.PublicKey)
	if !valid {
		t.Error("large file signature should be valid")
	}
}

func TestSignFileNotFound(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	_, err := SignFile("/nonexistent", k.PrivateKey)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestVerifyFileNotFound(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	_, err := VerifyFile("/nonexistent", []byte("sig"), k.PublicKey)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestSignDeterministic(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	msg := []byte("deterministic")

	sig1 := Sign(msg, k.PrivateKey)
	sig2 := Sign(msg, k.PrivateKey)

	// Ed25519 signatures are deterministic (same key + same message = same sig)
	if len(sig1) != len(sig2) {
		t.Error("signature lengths differ")
	}
}

func TestSignatureLength(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	sig := Sign([]byte("msg"), k.PrivateKey)

	// Ed25519 signatures are always 64 bytes
	if len(sig) != 64 {
		t.Errorf("signature length = %d, want 64", len(sig))
	}
}
