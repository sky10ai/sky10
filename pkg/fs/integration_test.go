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

// Full two-device sync over real S3 (MinIO).
// Device A uploads, Device B joins and downloads.
func TestIntegrationTwoDeviceSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "two-device-sync")

	// Device A: init and upload
	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "hello.md", strings.NewReader("hello from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Simulate invite approve
	simulateApprove(t, ctx, backend, idA, idB)

	// Device B: read A's file
	storeB := NewWithDevice(backend, idB, "device-b")
	var buf bytes.Buffer
	if err := storeB.Get(ctx, "hello.md", &buf); err != nil {
		t.Fatalf("B Get: %v", err)
	}
	if buf.String() != "hello from A" {
		t.Errorf("B got %q, want %q", buf.String(), "hello from A")
	}

	// Device B: upload a file
	if err := storeB.Put(ctx, "reply.md", strings.NewReader("hello from B")); err != nil {
		t.Fatalf("B Put: %v", err)
	}

	// Device A: read B's file
	buf.Reset()
	if err := storeA.Get(ctx, "reply.md", &buf); err != nil {
		t.Fatalf("A Get reply: %v", err)
	}
	if buf.String() != "hello from B" {
		t.Errorf("A got %q", buf.String())
	}
}

// Bidirectional folder sync over real S3.
func TestIntegrationBidirectionalFolderSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "bidir-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")

	// A uploads some files
	storeA.Put(ctx, "notes.md", strings.NewReader("my notes"))
	storeA.Put(ctx, "todo.md", strings.NewReader("buy milk"))

	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")

	// Sync to B's local directory
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

	// Verify files on disk
	data, err := os.ReadFile(filepath.Join(dirB, "notes.md"))
	if err != nil {
		t.Fatalf("read notes.md: %v", err)
	}
	if string(data) != "my notes" {
		t.Errorf("notes.md = %q", string(data))
	}

	// B creates a new file locally
	os.WriteFile(filepath.Join(dirB, "from-b.md"), []byte("B's file"), 0644)

	// Sync again — should upload B's new file
	result, err = engineB.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("B SyncOnce 2: %v", err)
	}
	if result.Uploaded != 1 {
		t.Errorf("B uploaded %d, want 1", result.Uploaded)
	}

	// Sync to A's local directory — should download B's file
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

	// A uploads files in nested directories
	storeA.Put(ctx, "root.md", strings.NewReader("root"))
	storeA.Put(ctx, "docs/readme.md", strings.NewReader("readme"))
	storeA.Put(ctx, "docs/guides/setup.md", strings.NewReader("setup guide"))
	storeA.Put(ctx, "photos/2026/march/pic.jpg", strings.NewReader("photo data"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B downloads everything
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

	// All subdirectories should be created
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

// Unauthorized device gets "access denied" over real S3.
func TestIntegrationUnauthorizedDevice(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "unauth-device")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "secret.md", strings.NewReader("classified"))

	// B tries without invite
	storeB := NewWithDevice(backend, idB, "device-b")
	err := storeB.Put(ctx, "hack.md", strings.NewReader("pwned"))
	if err == nil {
		t.Fatal("unauthorized device should not be able to write")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied', got: %v", err)
	}

	// A's data intact
	var buf bytes.Buffer
	storeA2 := NewWithDevice(backend, idA, "device-a")
	if err := storeA2.Get(ctx, "secret.md", &buf); err != nil {
		t.Fatalf("A Get after attack: %v", err)
	}
	if buf.String() != "classified" {
		t.Errorf("data corrupted: %q", buf.String())
	}
}

// Device rejoin with new key over real S3.
func TestIntegrationDeviceRejoin(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB1, _ := GenerateDeviceKey()
	idB2, _ := GenerateDeviceKey()
	backend := h.Backend(t, "device-rejoin")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "data.md", strings.NewReader("important"))

	// First join
	simulateApprove(t, ctx, backend, idA, idB1)
	storeB1 := NewWithDevice(backend, idB1, "device-b")
	var buf bytes.Buffer
	if err := storeB1.Get(ctx, "data.md", &buf); err != nil {
		t.Fatalf("B1 Get: %v", err)
	}

	// B resets, gets new key, rejoins
	simulateApprove(t, ctx, backend, idA, idB2)
	storeB2 := NewWithDevice(backend, idB2, "device-b")
	buf.Reset()
	if err := storeB2.Get(ctx, "data.md", &buf); err != nil {
		t.Fatalf("B2 Get: %v", err)
	}
	if buf.String() != "important" {
		t.Errorf("B2 got %q", buf.String())
	}
}

// Large file upload and download over real S3.
func TestIntegrationLargeFile(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	id, _ := GenerateDeviceKey()
	backend := h.Backend(t, "large-file")
	store := New(backend, id)

	// 5MB file — will be chunked
	size := 5 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := store.Put(ctx, "big.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var buf bytes.Buffer
	if err := store.Get(ctx, "big.bin", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.Len() != size {
		t.Errorf("got %d bytes, want %d", buf.Len(), size)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("data mismatch after round-trip")
	}
}

// Three devices all syncing the same drive.
func TestIntegrationThreeDeviceSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	idC, _ := GenerateDeviceKey()
	backend := h.Backend(t, "three-device")

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "from-a.md", strings.NewReader("A"))

	simulateApprove(t, ctx, backend, idA, idB)
	simulateApprove(t, ctx, backend, idA, idC)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.Put(ctx, "from-b.md", strings.NewReader("B"))

	storeC := NewWithDevice(backend, idC, "device-c")
	storeC.Put(ctx, "from-c.md", strings.NewReader("C"))

	// All three should see all three files
	for name, store := range map[string]*Store{"A": storeA, "B": storeB, "C": storeC} {
		entries, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("%s List: %v", name, err)
		}
		if len(entries) != 3 {
			t.Errorf("%s sees %d files, want 3", name, len(entries))
		}
	}
}

// Op envelope backward compat over real S3.
func TestIntegrationOpEnvelope(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	id, _ := GenerateDeviceKey()
	backend := h.Backend(t, "op-envelope")
	store := New(backend, id)

	store.Put(ctx, "test.md", strings.NewReader("with envelope"))

	// Verify ops have the envelope
	keys, _ := backend.List(ctx, "ops/")
	if len(keys) == 0 {
		t.Fatal("no ops written")
	}

	rc, _ := backend.Get(ctx, keys[0])
	raw := readAll(rc)
	rc.Close()

	if len(raw) < OpEnvelopeSize {
		t.Fatalf("op too short: %d bytes", len(raw))
	}
	if raw[0] != 'O' || raw[1] != 'P' || raw[2] != 'S' {
		t.Error("op missing OPS magic header")
	}
}

func readAll(rc interface{ Read([]byte) (int, error) }) []byte {
	var buf bytes.Buffer
	buf.ReadFrom(rc)
	return buf.Bytes()
}
