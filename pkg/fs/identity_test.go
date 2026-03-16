package fs

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDeviceKey(t *testing.T) {
	t.Parallel()
	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if len(id.PublicKey) != ed25519.PublicKeySize {
		t.Errorf("public key length = %d, want %d", len(id.PublicKey), ed25519.PublicKeySize)
	}
	if len(id.PrivateKey) != ed25519.PrivateKeySize {
		t.Errorf("private key length = %d, want %d", len(id.PrivateKey), ed25519.PrivateKeySize)
	}
}

func TestIdentityID(t *testing.T) {
	t.Parallel()
	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	skyID := id.Address()
	if !strings.HasPrefix(skyID, "sky10q") {
		t.Errorf("ID = %q, want prefix sky10q", skyID)
	}
}

func TestIdentityUniqueness(t *testing.T) {
	t.Parallel()
	id1, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey 1: %v", err)
	}
	id2, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey 2: %v", err)
	}

	if id1.Address() == id2.Address() {
		t.Error("two generated identities have the same ID")
	}
}

func TestSaveLoadKey(t *testing.T) {
	t.Parallel()
	id, err := GenerateDeviceKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	path := filepath.Join(t.TempDir(), "identity.key")
	if err := SaveKey(id, path); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	loaded, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}

	if !id.PublicKey.Equal(loaded.PublicKey) {
		t.Error("public keys don't match after save/load")
	}
	if !id.PrivateKey.Equal(loaded.PrivateKey) {
		t.Error("private keys don't match after save/load")
	}
	if id.Address() != loaded.Address() {
		t.Errorf("IDs don't match: %q != %q", id.Address(), loaded.Address())
	}
}

func TestLoadKeyErrors(t *testing.T) {
	t.Parallel()

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()
		_, err := LoadKey("/nonexistent/path")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "bad.key")
		if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadKey(path)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("invalid key length", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "short.key")
		data := `{"public_key": "dG9vc2hvcnQ", "private_key": "dG9vc2hvcnQ"}`
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadKey(path)
		if err == nil {
			t.Error("expected error for short keys")
		}
	})
}
