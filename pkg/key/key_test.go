package key

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

func TestLoadNotFound(t *testing.T) {
	t.Parallel()
	_, err := Load("/nonexistent/path")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not json"), 0600)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadInvalidKeyLength(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "short.json")
	os.WriteFile(path, []byte(`{"public_key":"AQID"}`), 0600)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for short public key")
	}
}

func TestSaveOverwrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "key.json")

	k1, _ := Generate()
	Save(k1, path)

	k2, _ := Generate()
	Save(k2, path)

	loaded, _ := Load(path)
	if !loaded.PublicKey.Equal(k2.PublicKey) {
		t.Error("overwritten key should be k2")
	}
}

func TestAddressLength(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	addr := k.Address()

	// Bech32m of 32 bytes: "sky" + "10" + 1 version + 52 data + 6 checksum ≈ 64 chars
	if len(addr) < 50 || len(addr) > 80 {
		t.Errorf("address length = %d, expected 50-80", len(addr))
	}
}

func TestAddressLowercaseOnly(t *testing.T) {
	t.Parallel()
	k, _ := Generate()
	addr := k.Address()

	for _, c := range addr {
		if c >= 'A' && c <= 'Z' {
			t.Errorf("address contains uppercase: %c in %s", c, addr)
			break
		}
	}
}

func TestGenerateUniqueness(t *testing.T) {
	t.Parallel()
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		k, _ := Generate()
		addr := k.Address()
		if keys[addr] {
			t.Fatalf("duplicate key generated at iteration %d", i)
		}
		keys[addr] = true
	}
}
