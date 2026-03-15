package skykey

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	t.Parallel()
	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(k.PublicKey) != ed25519.PublicKeySize {
		t.Errorf("pub key size = %d", len(k.PublicKey))
	}
	if len(k.PrivateKey) != ed25519.PrivateKeySize {
		t.Errorf("priv key size = %d", len(k.PrivateKey))
	}
	if !k.IsPrivate() {
		t.Error("generated key should be private")
	}
}

func TestFromPublicKey(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	pub := FromPublicKey(k.PublicKey)
	if pub.IsPrivate() {
		t.Error("public-only key should not be private")
	}
}

func TestAddressRoundTrip(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	addr := k.Address()

	if !strings.HasPrefix(addr, "sky10") {
		t.Errorf("address = %q, want sky10 prefix", addr)
	}

	parsed, err := ParseAddress(addr)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	if !parsed.PublicKey.Equal(k.PublicKey) {
		t.Error("parsed public key doesn't match")
	}
	if parsed.IsPrivate() {
		t.Error("parsed key should be public-only")
	}
}

func TestAddressUniqueness(t *testing.T) {
	t.Parallel()
	k1, _ := Generate()
	k2, _ := Generate()
	if k1.Address() == k2.Address() {
		t.Error("different keys should have different addresses")
	}
}

func TestParseAddressInvalid(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"sky10",
		"sky10qinvalid",
		"wrong10qpzry",
		"not-an-address",
	}
	for _, s := range tests {
		_, err := ParseAddress(s)
		if err == nil {
			t.Errorf("ParseAddress(%q) should fail", s)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	path := filepath.Join(t.TempDir(), "key.json")

	if err := Save(k, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !loaded.PublicKey.Equal(k.PublicKey) {
		t.Error("loaded public key doesn't match")
	}
	if !loaded.PrivateKey.Equal(k.PrivateKey) {
		t.Error("loaded private key doesn't match")
	}
	if loaded.Address() != k.Address() {
		t.Error("loaded address doesn't match")
	}
}

func TestAddressDeterministic(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	a1 := k.Address()
	a2 := k.Address()
	if a1 != a2 {
		t.Error("same key should produce same address")
	}
}
