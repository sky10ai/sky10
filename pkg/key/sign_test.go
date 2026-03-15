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

	// Modify the file
	os.WriteFile(path, []byte("modified"), 0644)

	valid, _ := VerifyFile(path, sig, k.PublicKey)
	if valid {
		t.Error("modified file should fail verification")
	}
}
