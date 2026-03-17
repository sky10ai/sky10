package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/sky10/sky10/pkg/adapter"
	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// helper: simulate the invite approve flow — unwrap A's namespace keys, wrap for B
func simulateApprove(t *testing.T, ctx context.Context, backend adapter.Backend, idA, idB *DeviceKey) {
	t.Helper()
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	bID := shortPubkeyID(idB.Address())

	for _, nsKeyPath := range nsKeys {
		rc, err := backend.Get(ctx, nsKeyPath)
		if err != nil {
			continue
		}
		wrapped, _ := io.ReadAll(rc)
		rc.Close()

		nsKey, err := UnwrapNamespaceKey(wrapped, idA.PrivateKey)
		if err != nil {
			continue
		}

		wrappedForB, err := WrapNamespaceKey(nsKey, idB.PublicKey)
		if err != nil {
			t.Fatalf("wrapping for B: %v", err)
		}

		nsName := extractNamespaceName(nsKeyPath)
		bKeyPath := "keys/namespaces/" + nsName + "." + bID + ".ns.enc"
		r := bytes.NewReader(wrappedForB)
		backend.Put(ctx, bKeyPath, r, int64(len(wrappedForB)))
	}
}

// Two devices with different keys can read/write ops using a shared namespace key.
func TestMultiDeviceCrossReadWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "file1.md", strings.NewReader("from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	if err := storeB.Put(ctx, "file2.md", strings.NewReader("from B")); err != nil {
		t.Fatalf("B Put: %v", err)
	}

	// Both see both files
	for name, store := range map[string]*Store{"A": storeA, "B": storeB} {
		entries, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("%s List: %v", name, err)
		}
		if len(entries) != 2 {
			t.Errorf("%s: expected 2 files, got %d", name, len(entries))
		}
	}

	// Cross-reads
	var buf bytes.Buffer
	if err := storeA.Get(ctx, "file2.md", &buf); err != nil {
		t.Fatalf("A reading B's file: %v", err)
	}
	if buf.String() != "from B" {
		t.Errorf("got %q, want %q", buf.String(), "from B")
	}
	buf.Reset()
	if err := storeB.Get(ctx, "file1.md", &buf); err != nil {
		t.Fatalf("B reading A's file: %v", err)
	}
	if buf.String() != "from A" {
		t.Errorf("got %q, want %q", buf.String(), "from A")
	}
}

// Unauthorized device must get "access denied" and must NOT overwrite
// the existing namespace key. Before fix: device would silently create
// a new key at the same path, destroying all existing encrypted data.
func TestUnauthorizedDeviceCannotOverwriteKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "secret.md", strings.NewReader("important data")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// B tries without invite — must fail
	storeB := NewWithDevice(backend, idB, "device-b")
	err := storeB.Put(ctx, "evil.md", strings.NewReader("overwrite attempt"))
	if err == nil {
		t.Fatal("expected error from unauthorized device, got nil")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected 'access denied' error, got: %v", err)
	}

	// A's data must still be readable
	storeA2 := NewWithDevice(backend, idA, "device-a")
	var buf bytes.Buffer
	if err := storeA2.Get(ctx, "secret.md", &buf); err != nil {
		t.Fatalf("A Get after B's attempt: %v", err)
	}
	if buf.String() != "important data" {
		t.Fatalf("A's data corrupted: got %q", buf.String())
	}
}

// Full invite lifecycle: create → fail without access → approve → succeed → cross-read.
func TestFullInviteFlowEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "from-a.md", strings.NewReader("hello from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// B fails without access
	storeB := NewWithDevice(backend, idB, "device-b")
	if err := storeB.Put(ctx, "from-b.md", strings.NewReader("hello from B")); err == nil {
		t.Fatal("B should not be able to write without invite")
	}

	// Approve
	simulateApprove(t, ctx, backend, idA, idB)

	// B succeeds after approve
	storeB2 := NewWithDevice(backend, idB, "device-b")
	if err := storeB2.Put(ctx, "from-b.md", strings.NewReader("hello from B")); err != nil {
		t.Fatalf("B Put after invite: %v", err)
	}

	// Both see both files
	for name, store := range map[string]*Store{"A": storeA, "B": storeB2} {
		entries, err := store.List(ctx, "")
		if err != nil {
			t.Fatalf("%s List: %v", name, err)
		}
		if len(entries) != 2 {
			t.Errorf("%s sees %d files, want 2", name, len(entries))
		}
	}

	// Cross-reads
	var buf bytes.Buffer
	if err := storeB2.Get(ctx, "from-a.md", &buf); err != nil {
		t.Fatalf("B reading A's file: %v", err)
	}
	if buf.String() != "hello from A" {
		t.Errorf("B got %q", buf.String())
	}
	buf.Reset()
	if err := storeA.Get(ctx, "from-b.md", &buf); err != nil {
		t.Fatalf("A reading B's file: %v", err)
	}
	if buf.String() != "hello from B" {
		t.Errorf("A got %q", buf.String())
	}
}

// Subfolders within a drive must use the drive's namespace, not create
// separate namespace keys per directory. Before fix: every top-level
// directory created its own namespace key, which other devices couldn't
// decrypt because the key was only wrapped for the creating device.
func TestSubfoldersShareDriveNamespace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	// Device A with a fixed namespace (simulating a drive)
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("mydrive")

	// Write files in various subfolders
	files := map[string]string{
		"readme.md":             "root file",
		"notes/daily/today.md":  "daily note",
		"photos/vacation/1.jpg": "photo data",
		"deep/a/b/c/d.txt":      "deep file",
	}
	for path, content := range files {
		if err := storeA.Put(ctx, path, strings.NewReader(content)); err != nil {
			t.Fatalf("A Put %s: %v", path, err)
		}
	}

	// Should have exactly ONE namespace key (mydrive) plus default (for ops)
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	nsNames := make(map[string]bool)
	for _, k := range nsKeys {
		nsNames[extractNamespaceName(k)] = true
	}
	if nsNames["notes"] || nsNames["photos"] || nsNames["deep"] {
		t.Errorf("subfolders created separate namespace keys: %v", nsKeys)
	}
	if !nsNames["mydrive"] {
		t.Error("drive namespace key missing")
	}

	// Approve for Device B
	simulateApprove(t, ctx, backend, idA, idB)

	// Device B reads all files — should work because they're all under one namespace
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("mydrive")

	for path, wantContent := range files {
		var buf bytes.Buffer
		if err := storeB.Get(ctx, path, &buf); err != nil {
			t.Fatalf("B Get %s: %v", path, err)
		}
		if buf.String() != wantContent {
			t.Errorf("B Get %s = %q, want %q", path, buf.String(), wantContent)
		}
	}
}

// Device B joins when multiple namespaces exist. ApproveJoin must wrap
// ALL namespace keys for the joiner, not just "default". Before fix:
// only default was wrapped, leaving the joiner locked out of other
// namespaces like drive-specific ones.
func TestApproveWrapsAllNamespaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	// Device A creates files in multiple namespaces using path-derived namespaces
	// (no SetNamespace — simulates CLI usage where top-level dir = namespace)
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "root.md", strings.NewReader("default ns"))
	storeA.Put(ctx, "photos/cat.jpg", strings.NewReader("meow"))
	storeA.Put(ctx, "docs/notes.md", strings.NewReader("my notes"))

	// Verify multiple namespace keys exist
	nsKeys, _ := backend.List(ctx, "keys/namespaces/")
	nsNames := make(map[string]bool)
	for _, k := range nsKeys {
		nsNames[extractNamespaceName(k)] = true
	}
	if !nsNames["default"] || !nsNames["photos"] || !nsNames["docs"] {
		t.Fatalf("expected default, photos, docs namespaces, got: %v", nsKeys)
	}

	// Approve B — must wrap ALL namespaces
	simulateApprove(t, ctx, backend, idA, idB)

	// B must be able to read from ALL namespaces
	storeB := NewWithDevice(backend, idB, "device-b")

	// B must see all 3 files across all namespaces
	entries, err := storeB.List(ctx, "")
	if err != nil {
		t.Fatalf("B List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("B sees %d files, want 3", len(entries))
	}

	var buf bytes.Buffer
	if err := storeB.Get(ctx, "root.md", &buf); err != nil {
		t.Fatalf("B Get root.md (default ns): %v", err)
	}

	buf.Reset()
	if err := storeB.Get(ctx, "photos/cat.jpg", &buf); err != nil {
		t.Fatalf("B Get photos/cat.jpg: %v", err)
	}
	if buf.String() != "meow" {
		t.Errorf("got %q", buf.String())
	}

	buf.Reset()
	if err := storeB.Get(ctx, "docs/notes.md", &buf); err != nil {
		t.Fatalf("B Get docs/notes.md: %v", err)
	}
	if buf.String() != "my notes" {
		t.Errorf("got %q", buf.String())
	}
}

// When device A creates a new namespace, the key must be wrapped for
// all registered devices, not just device A.
func TestNewNamespaceWrappedForAllDevices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	// Device A writes initial file (creates "default" namespace)
	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "file1.md", strings.NewReader("first")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	// Approve B for existing namespaces
	simulateApprove(t, ctx, backend, idA, idB)

	// Register Device B so Device A knows about it
	RegisterDevice(ctx, backend, idB.Address(), "Device B", "test")

	// Device A creates a file in a NEW namespace (no drive namespace set, uses path-derived)
	if err := storeA.Put(ctx, "photos/cat.jpg", strings.NewReader("meow")); err != nil {
		t.Fatalf("A Put photos: %v", err)
	}

	// Device B should have a wrapped key for the "photos" namespace
	bID := shortPubkeyID(idB.Address())
	bKeyPath := "keys/namespaces/photos." + bID + ".ns.enc"
	if _, err := backend.Head(ctx, bKeyPath); err != nil {
		t.Errorf("Device B's key for 'photos' namespace not found at %s", bKeyPath)
	}

	// Device B should be able to read the file
	storeB := NewWithDevice(backend, idB, "device-b")
	var buf bytes.Buffer
	if err := storeB.Get(ctx, "photos/cat.jpg", &buf); err != nil {
		t.Fatalf("B Get photos/cat.jpg: %v", err)
	}
	if buf.String() != "meow" {
		t.Errorf("B got %q, want %q", buf.String(), "meow")
	}
}

// Three devices: A creates, B joins, C joins later. All must be able
// to read all files.
func TestThreeDevices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	idC, _ := GenerateDeviceKey()

	// A writes
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "a.md", strings.NewReader("from A"))

	// Approve B
	simulateApprove(t, ctx, backend, idA, idB)

	// B writes
	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.Put(ctx, "b.md", strings.NewReader("from B"))

	// Approve C (A wraps keys)
	simulateApprove(t, ctx, backend, idA, idC)

	// C writes
	storeC := NewWithDevice(backend, idC, "device-c")
	if err := storeC.Put(ctx, "c.md", strings.NewReader("from C")); err != nil {
		t.Fatalf("C Put: %v", err)
	}

	// All three see all three files
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

// Ops encrypted with per-device key (the old bug) must not be decryptable
// by other devices. This verifies the fix: ops use the shared namespace key.
func TestOpsUseSharedKeyNotPerDevice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()

	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "test.md", strings.NewReader("hello"))

	// Get the ops key from A — it should be the "default" namespace key
	opsKeyA, err := storeA.opsKey(ctx)
	if err != nil {
		t.Fatalf("opsKey: %v", err)
	}

	// Wrap for B and verify B gets the same key
	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	opsKeyB, err := storeB.opsKey(ctx)
	if err != nil {
		t.Fatalf("B opsKey: %v", err)
	}

	if !bytes.Equal(opsKeyA, opsKeyB) {
		t.Error("ops keys differ between devices — ops will be unreadable cross-device")
	}
}

// Op envelope must be readable by any device, and legacy ops without
// envelope must also work.
func TestOpEnvelopeBackwardCompat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := GenerateNamespaceKey()

	// Write a legacy op (no envelope — just encrypted JSON)
	legacyOp := Op{Type: OpPut, Path: "old.md", Chunks: []string{"aaa"},
		Size: 100, Checksum: "c1", Device: "dev-a", Timestamp: 1000, Seq: 1}
	data, _ := json.Marshal(legacyOp)
	encrypted, _ := Encrypt(data, encKey)
	r := bytes.NewReader(encrypted)
	backend.Put(ctx, legacyOp.OpKey(), r, int64(len(encrypted)))

	// Write a new op (with envelope)
	newOp := Op{Type: OpPut, Path: "new.md", Chunks: []string{"bbb"},
		Size: 200, Checksum: "c2", Device: "dev-a", Timestamp: 2000, Seq: 2}
	WriteOp(ctx, backend, &newOp, encKey)

	// Both should be readable
	ops, err := ReadAllOps(ctx, backend, encKey)
	if err != nil {
		t.Fatalf("ReadAllOps: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}
}

// Device B joining with an old key, then rejoining with a new key
// (after rm -rf ~/.sky10). The old device-specific namespace key
// should not interfere with the new one.
func TestDeviceRejoinWithNewKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	idA, _ := GenerateDeviceKey()
	idB1, _ := GenerateDeviceKey() // B's first key
	idB2, _ := GenerateDeviceKey() // B's second key (after reset)

	// A creates store
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.Put(ctx, "test.md", strings.NewReader("data"))

	// Approve B with first key
	simulateApprove(t, ctx, backend, idA, idB1)

	// B1 can read
	storeB1 := NewWithDevice(backend, idB1, "device-b")
	var buf bytes.Buffer
	if err := storeB1.Get(ctx, "test.md", &buf); err != nil {
		t.Fatalf("B1 Get: %v", err)
	}

	// B resets and gets a new key — approve again
	simulateApprove(t, ctx, backend, idA, idB2)

	// B2 can also read (has its own wrapped key)
	storeB2 := NewWithDevice(backend, idB2, "device-b")
	buf.Reset()
	if err := storeB2.Get(ctx, "test.md", &buf); err != nil {
		t.Fatalf("B2 Get: %v", err)
	}
	if buf.String() != "data" {
		t.Errorf("B2 got %q, want %q", buf.String(), "data")
	}

	// Old B1 key still works too (both wrapped keys coexist in S3)
	storeB1b := NewWithDevice(backend, idB1, "device-b")
	buf.Reset()
	if err := storeB1b.Get(ctx, "test.md", &buf); err != nil {
		t.Fatalf("B1 still works: %v", err)
	}
}
