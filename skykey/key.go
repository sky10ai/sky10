package skykey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
)

const (
	// HRP is the human-readable prefix for sky10 addresses.
	HRP = "sky"

	// VersionEd25519 is the version byte for Ed25519 keys.
	VersionEd25519 byte = 0
)

// Key is an Ed25519 keypair. PrivateKey is nil for public-only keys
// (e.g., parsed from an address).
type Key struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// Generate creates a new random Ed25519 keypair.
func Generate() (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}
	return &Key{PublicKey: pub, PrivateKey: priv}, nil
}

// FromPublicKey creates a public-only Key (cannot sign, seal, or unwrap).
func FromPublicKey(pub ed25519.PublicKey) *Key {
	return &Key{PublicKey: pub}
}

// IsPrivate returns true if this key can perform private-key operations.
func (k *Key) IsPrivate() bool {
	return k.PrivateKey != nil
}

// Address returns the Bech32m-encoded address: sky10q...
func (k *Key) Address() string {
	addr, _ := Bech32mEncode(HRP, VersionEd25519, k.PublicKey)
	return addr
}

// ParseAddress decodes a sky10q... address into a public-only Key.
func ParseAddress(address string) (*Key, error) {
	hrp, version, data, err := Bech32mDecode(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}
	if hrp != HRP {
		return nil, fmt.Errorf("invalid hrp: %q, want %q", hrp, HRP)
	}
	if version != VersionEd25519 {
		return nil, fmt.Errorf("unsupported key version: %d", version)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length: %d", len(data))
	}
	return FromPublicKey(ed25519.PublicKey(data)), nil
}

// keyFile is the JSON representation stored on disk.
type keyFile struct {
	PublicKey  []byte `json:"public_key"`
	PrivateKey []byte `json:"private_key,omitempty"`
}

// Save writes the key to path with restricted permissions.
func Save(k *Key, path string) error {
	f := keyFile{PublicKey: k.PublicKey}
	if k.PrivateKey != nil {
		f.PrivateKey = k.PrivateKey
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// Load reads a key from path.
func Load(path string) (*Key, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key: %w", err)
	}
	var f keyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing key: %w", err)
	}
	if len(f.PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length: %d", len(f.PublicKey))
	}
	k := &Key{PublicKey: ed25519.PublicKey(f.PublicKey)}
	if len(f.PrivateKey) == ed25519.PrivateKeySize {
		k.PrivateKey = ed25519.PrivateKey(f.PrivateKey)
	}
	return k, nil
}
