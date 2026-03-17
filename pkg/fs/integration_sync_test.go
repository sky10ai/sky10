//go:build integration

package fs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bidirectional folder sync over real S3.
func TestIntegrationBidirectionalFolderSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "bidir-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")

	storeA.Put(ctx, "notes.md", strings.NewReader("my notes"))
	storeA.Put(ctx, "todo.md", strings.NewReader("buy milk"))

	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")

	dirB := t.TempDir()
	engineB := NewSyncEngine(storeB, SyncConfig{
		LocalRoot:  dirB,
		Namespaces: []string{"shared"},
	})
	result, err := engineB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}
	if result.Downloaded != 2 {
		t.Errorf("B downloaded %d, want 2", result.Downloaded)
	}

	data, err := os.ReadFile(filepath.Join(dirB, "notes.md"))
	if err != nil {
		t.Fatalf("read notes.md: %v", err)
	}
	if string(data) != "my notes" {
		t.Errorf("notes.md = %q", string(data))
	}

	os.WriteFile(filepath.Join(dirB, "from-b.md"), []byte("B's file"), 0644)

	result, err = engineB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce 2: %v", err)
	}
	if result.Uploaded != 1 {
		t.Errorf("B uploaded %d, want 1", result.Uploaded)
	}

	dirA := t.TempDir()
	engineA := NewSyncEngine(storeA, SyncConfig{
		LocalRoot:  dirA,
		Namespaces: []string{"shared"},
	})
	result, err = engineA.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("A SyncOnce: %v", err)
	}
	if result.Downloaded < 1 {
		t.Errorf("A downloaded %d, want >= 1", result.Downloaded)
	}

	data, err = os.ReadFile(filepath.Join(dirA, "from-b.md"))
	if err != nil {
		t.Fatalf("read from-b.md on A: %v", err)
	}
	if string(data) != "B's file" {
		t.Errorf("from-b.md = %q", string(data))
	}
}

// Subfolder sync with drive namespace over real S3.
func TestIntegrationSubfolderSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "subfolder-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("drive1")

	storeA.Put(ctx, "root.md", strings.NewReader("root"))
	storeA.Put(ctx, "docs/readme.md", strings.NewReader("readme"))
	storeA.Put(ctx, "docs/guides/setup.md", strings.NewReader("setup guide"))
	storeA.Put(ctx, "photos/2026/march/pic.jpg", strings.NewReader("photo data"))

	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("drive1")

	dirB := t.TempDir()
	engine := NewSyncEngine(storeB, SyncConfig{
		LocalRoot:  dirB,
		Namespaces: []string{"drive1"},
	})
	result, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if result.Downloaded != 4 {
		t.Errorf("downloaded %d, want 4", result.Downloaded)
	}

	paths := []string{
		"root.md",
		"docs/readme.md",
		"docs/guides/setup.md",
		"photos/2026/march/pic.jpg",
	}
	for _, p := range paths {
		full := filepath.Join(dirB, filepath.FromSlash(p))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing: %s", p)
		}
	}
}

// Regression: empty local files must not overwrite remote content.
func TestIntegrationEmptyLocalDoesNotWipeRemote(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "empty-wipe-test")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	if err := storeA.Put(ctx, "important.txt", strings.NewReader("critical data here")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	simulateApprove(t, ctx, backend, idA, idB)

	dirB := t.TempDir()
	os.WriteFile(filepath.Join(dirB, "important.txt"), []byte{}, 0644)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	engineB := NewSyncEngine(storeB, SyncConfig{
		LocalRoot:  dirB,
		Namespaces: []string{"shared"},
	})
	result, err := engineB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce: %v", err)
	}

	if result.Downloaded != 1 {
		t.Errorf("expected 1 download, got %d (uploaded=%d)", result.Downloaded, result.Uploaded)
	}
	if result.Uploaded != 0 {
		t.Errorf("expected 0 uploads, got %d — empty file would have wiped remote", result.Uploaded)
	}

	data, err := os.ReadFile(filepath.Join(dirB, "important.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "critical data here" {
		t.Errorf("local file = %q, want %q", string(data), "critical data here")
	}

	var buf bytes.Buffer
	if err := storeA.Get(ctx, "important.txt", &buf); err != nil {
		t.Fatalf("A Get: %v", err)
	}
	if buf.String() != "critical data here" {
		t.Errorf("remote data corrupted: %q", buf.String())
	}
}

// Verify that a legitimate local edit DOES upload.
func TestIntegrationRealEditUploads(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	id, _ := GenerateDeviceKey()
	backend := h.Backend(t, "real-edit-test")

	store := New(backend, id)
	store.SetNamespace("docs")
	store.Put(ctx, "notes.txt", strings.NewReader("version 1"))

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("version 2 - edited locally"), 0644)

	engine := NewSyncEngine(store, SyncConfig{
		LocalRoot:  dir,
		Namespaces: []string{"docs"},
	})
	result, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if result.Uploaded != 1 {
		t.Errorf("expected 1 upload for real edit, got %d", result.Uploaded)
	}

	var buf bytes.Buffer
	store2 := New(backend, id)
	store2.SetNamespace("docs")
	if err := store2.Get(ctx, "notes.txt", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.String() != "version 2 - edited locally" {
		t.Errorf("remote = %q, want edited version", buf.String())
	}
}
