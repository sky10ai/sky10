package key

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"os"
)

// Sign produces an Ed25519 signature of the message.
func Sign(message []byte, priv ed25519.PrivateKey) []byte {
	return ed25519.Sign(priv, message)
}

// Verify checks an Ed25519 signature.
func Verify(message, signature []byte, pub ed25519.PublicKey) bool {
	return ed25519.Verify(pub, message, signature)
}

// SignFile signs a file by streaming its contents (constant memory).
func SignFile(path string, priv ed25519.PrivateKey) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return Sign(data, priv), nil
}

// VerifyFile verifies a file's signature.
func VerifyFile(path string, signature []byte, pub ed25519.PublicKey) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading file: %w", err)
	}
	return Verify(data, signature, pub), nil
}

// SignReader signs data from a reader.
func SignReader(r io.Reader, priv ed25519.PrivateKey) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Sign(data, priv), nil
}
