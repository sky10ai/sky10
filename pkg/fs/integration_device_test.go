//go:build integration

package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Full two-device sync over real S3 (MinIO).
func TestIntegrationTwoDeviceSync(t *testing.T) {
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "two-device-sync")

	storeA := NewWithDevice(backend, idA, "device-a")
	if err := storeA.Put(ctx, "hello.md", strings.NewReader("hello from A")); err != nil {
		t.Fatalf("A Put: %v", err)
	}

	simulateApprove(t, ctx, backend, idA, idB)

	storeB := NewWithDevice(backend, idB, "device-b")
	var buf bytes.Buffer
	if err := storeB.Get(ctx, "hello.md", &buf); err != nil {
		t.Fatalf("B Get: %v", err)
	}
	if buf.String() != "hello from A" {
		t.Errorf("B got %q, want %q", buf.String(), "hello from A")
	}

	if err := storeB.Put(ctx, "reply.md", strings.NewReader("hello from B")); err != nil {
		t.Fatalf("B Put: %v", err)
	}

	buf.Reset()
	if err := storeA.Get(ctx, "reply.md", &buf); err != nil {
		t.Fatalf("A Get reply: %v", err)
	}
	if buf.String() != "hello from B" {
		t.Errorf("A got %q", buf.String())
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

	storeB := NewWithDevice(backend, idB, "device-b")
	err := storeB.Put(ctx, "hack.md", strings.NewReader("pwned"))
	if err == nil {
		t.Fatal("unauthorized device should not be able to write")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected 'access denied', got: %v", err)
	}

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

	simulateApprove(t, ctx, backend, idA, idB1)
	storeB1 := NewWithDevice(backend, idB1, "device-b")
	var buf bytes.Buffer
	if err := storeB1.Get(ctx, "data.md", &buf); err != nil {
		t.Fatalf("B1 Get: %v", err)
	}

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
