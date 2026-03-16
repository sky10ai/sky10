package fs

import (
	"context"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestNewManifest(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if len(m.Tree) != 0 {
		t.Errorf("tree should be empty, got %d entries", len(m.Tree))
	}
}

func TestManifestSetAndRemove(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	entry := FileEntry{
		Chunks:    []string{"abc123"},
		Size:      100,
		Modified:  time.Now().UTC(),
		Checksum:  "sha256:abc123",
		Namespace: "journal",
	}

	m.Set("journal/test.md", entry)

	got, ok := m.Tree["journal/test.md"]
	if !ok {
		t.Fatal("entry not found after Set")
	}
	if got.Size != 100 {
		t.Errorf("size = %d, want 100", got.Size)
	}
	if got.Namespace != "journal" {
		t.Errorf("namespace = %q, want %q", got.Namespace, "journal")
	}

	if !m.Remove("journal/test.md") {
		t.Error("Remove returned false for existing entry")
	}
	if _, ok := m.Tree["journal/test.md"]; ok {
		t.Error("entry still present after Remove")
	}

	if m.Remove("nonexistent") {
		t.Error("Remove returned true for nonexistent entry")
	}
}

func TestManifestListPrefix(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	m.Set("journal/a.md", FileEntry{Size: 1})
	m.Set("journal/b.md", FileEntry{Size: 2})
	m.Set("financial/report.pdf", FileEntry{Size: 3})
	m.Set("notes.md", FileEntry{Size: 4})

	tests := []struct {
		prefix string
		want   int
	}{
		{"journal/", 2},
		{"financial/", 1},
		{"", 4},
		{"notes", 1},
		{"nonexistent/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			t.Parallel()
			entries := m.ListPrefix(tt.prefix)
			if len(entries) != tt.want {
				t.Errorf("ListPrefix(%q) returned %d entries, want %d", tt.prefix, len(entries), tt.want)
			}
		})
	}

	// Verify sorted order
	entries := m.ListPrefix("journal/")
	if len(entries) == 2 && entries[0].Path > entries[1].Path {
		t.Error("entries not sorted by path")
	}
}

func TestManifestSaveLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	_, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	encKey, _ := GenerateNamespaceKey()

	m := NewManifest()
	m.Set("journal/test.md", FileEntry{
		Chunks:    []string{"abc123", "def456"},
		Size:      1234,
		Modified:  time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC),
		Checksum:  "sha256:abc123",
		Namespace: "journal",
	})
	m.Set("notes.md", FileEntry{
		Chunks:    []string{"ghi789"},
		Size:      500,
		Modified:  time.Date(2026, 3, 14, 11, 0, 0, 0, time.UTC),
		Checksum:  "sha256:ghi789",
		Namespace: "default",
	})

	if err := SaveManifest(ctx, backend, m, encKey); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := LoadManifest(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if loaded.Version != m.Version {
		t.Errorf("version = %d, want %d", loaded.Version, m.Version)
	}
	if len(loaded.Tree) != len(m.Tree) {
		t.Fatalf("tree has %d entries, want %d", len(loaded.Tree), len(m.Tree))
	}

	for path, want := range m.Tree {
		got, ok := loaded.Tree[path]
		if !ok {
			t.Errorf("missing entry: %s", path)
			continue
		}
		if got.Size != want.Size {
			t.Errorf("%s: size = %d, want %d", path, got.Size, want.Size)
		}
		if got.Checksum != want.Checksum {
			t.Errorf("%s: checksum = %q, want %q", path, got.Checksum, want.Checksum)
		}
		if len(got.Chunks) != len(want.Chunks) {
			t.Errorf("%s: %d chunks, want %d", path, len(got.Chunks), len(want.Chunks))
		}
	}
}

func TestLoadManifestEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	m, err := LoadManifest(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Tree) != 0 {
		t.Errorf("expected empty manifest, got %d entries", len(m.Tree))
	}
}

func TestManifestEncrypted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	m := NewManifest()
	m.Set("secret.md", FileEntry{Size: 42, Checksum: "sha256:secret"})

	if err := SaveManifest(ctx, backend, m, encKey); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	// Read raw data from backend — should not contain plaintext
	rc, err := backend.Get(ctx, "manifests/current.enc")
	if err != nil {
		t.Fatalf("Get raw: %v", err)
	}
	defer rc.Close()

	// The raw data should not contain "secret.md" in plaintext
	raw := make([]byte, 4096)
	n, _ := rc.Read(raw)
	raw = raw[:n]

	for _, needle := range []string{"secret.md", "sha256:secret"} {
		for i := 0; i <= len(raw)-len(needle); i++ {
			if string(raw[i:i+len(needle)]) == needle {
				t.Errorf("raw manifest contains plaintext %q", needle)
			}
		}
	}
}

func TestManifestWrongKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()
	encKey2, _ := GenerateNamespaceKey()

	m := NewManifest()
	m.Set("test.md", FileEntry{Size: 1})

	if err := SaveManifest(ctx, backend, m, encKey); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	_, err := LoadManifest(ctx, backend, encKey2)
	if err == nil {
		t.Error("expected error loading manifest with wrong identity")
	}
}
