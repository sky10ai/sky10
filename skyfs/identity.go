// Package skyfs provides encrypted file storage primitives.
//
// Every file is encrypted with AES-256-GCM before it leaves the device.
// Identity is an Ed25519 keypair. The key hierarchy (user → namespace → file)
// makes rotation cheap and scoped access possible.
package skyfs

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// Identity represents a user or agent identity backed by an Ed25519 keypair.
type Identity struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateIdentity creates a new random Ed25519 keypair.
func GenerateIdentity() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}
	return &Identity{PublicKey: pub, PrivateKey: priv}, nil
}

// ID returns the sky:// URI for this identity's public key.
func (id *Identity) ID() string {
	encoded := base64.RawURLEncoding.EncodeToString(id.PublicKey)
	return "sky://k1_" + encoded
}

// identityFile is the JSON representation stored on disk.
type identityFile struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// SaveIdentity writes the identity to path with restricted permissions.
func SaveIdentity(id *Identity, path string) error {
	f := identityFile{
		PublicKey:  base64.RawURLEncoding.EncodeToString(id.PublicKey),
		PrivateKey: base64.RawURLEncoding.EncodeToString(id.PrivateKey),
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling identity: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing identity: %w", err)
	}
	return nil
}

// LoadIdentity reads an identity from path.
func LoadIdentity(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading identity: %w", err)
	}

	var f identityFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing identity: %w", err)
	}

	pub, err := base64.RawURLEncoding.DecodeString(f.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	priv, err := base64.RawURLEncoding.DecodeString(f.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decoding private key: %w", err)
	}

	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length: %d", len(pub))
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key length: %d", len(priv))
	}

	return &Identity{
		PublicKey:  ed25519.PublicKey(pub),
		PrivateKey: ed25519.PrivateKey(priv),
	}, nil
}
