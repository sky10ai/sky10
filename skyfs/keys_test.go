package skyfs

import (
	"bytes"
	"testing"
)

func TestNamespaceFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{"journal/2026-03-14.md", "journal"},
		{"financial/reports/q4.pdf", "financial"},
		{"notes.md", "default"},
		{"contacts/alice.vcf", "contacts"},
		{"/journal/entry.md", "journal"},
		{"a/b/c/d/e.txt", "a"},
		{"single", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			got := NamespaceFromPath(tt.path)
			if got != tt.want {
				t.Errorf("NamespaceFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDeriveFileKey(t *testing.T) {
	t.Parallel()

	nsKey, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	hash1 := []byte("hash-of-content-1")
	hash2 := []byte("hash-of-content-2")

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		k1, err := DeriveFileKey(nsKey, hash1)
		if err != nil {
			t.Fatalf("DeriveFileKey: %v", err)
		}
		k2, err := DeriveFileKey(nsKey, hash1)
		if err != nil {
			t.Fatalf("DeriveFileKey: %v", err)
		}
		if !bytes.Equal(k1, k2) {
			t.Error("same inputs produced different keys")
		}
	})

	t.Run("different content different key", func(t *testing.T) {
		t.Parallel()
		k1, _ := DeriveFileKey(nsKey, hash1)
		k2, _ := DeriveFileKey(nsKey, hash2)
		if bytes.Equal(k1, k2) {
			t.Error("different content hashes produced same key")
		}
	})

	t.Run("different namespace different key", func(t *testing.T) {
		t.Parallel()
		nsKey2, _ := GenerateKey()
		k1, _ := DeriveFileKey(nsKey, hash1)
		k2, _ := DeriveFileKey(nsKey2, hash1)
		if bytes.Equal(k1, k2) {
			t.Error("different namespace keys produced same key")
		}
	})

	t.Run("correct length", func(t *testing.T) {
		t.Parallel()
		k, _ := DeriveFileKey(nsKey, hash1)
		if len(k) != KeySize {
			t.Errorf("key length = %d, want %d", len(k), KeySize)
		}
	})

	t.Run("invalid namespace key", func(t *testing.T) {
		t.Parallel()
		_, err := DeriveFileKey([]byte("short"), hash1)
		if err == nil {
			t.Error("expected error for short namespace key")
		}
	})
}

func TestWrapUnwrapNamespaceKey(t *testing.T) {
	t.Parallel()

	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}

	nsKey, err := GenerateNamespaceKey()
	if err != nil {
		t.Fatalf("GenerateNamespaceKey: %v", err)
	}

	wrapped, err := WrapNamespaceKey(nsKey, id.PublicKey)
	if err != nil {
		t.Fatalf("WrapNamespaceKey: %v", err)
	}

	unwrapped, err := UnwrapNamespaceKey(wrapped, id.PrivateKey)
	if err != nil {
		t.Fatalf("UnwrapNamespaceKey: %v", err)
	}

	if !bytes.Equal(unwrapped, nsKey) {
		t.Error("unwrapped namespace key does not match original")
	}
}
