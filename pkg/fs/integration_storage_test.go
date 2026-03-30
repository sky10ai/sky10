//go:build integration

package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Large file upload and download over real S3.
func TestIntegrationLargeFile(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
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

// Op envelope format over real S3.
func TestIntegrationOpEnvelope(t *testing.T) {
	t.Skip("snapshot-exchange: S3 ops no longer written")
	h := StartMinIO(t)
	ctx := context.Background()

	id, _ := GenerateDeviceKey()
	backend := h.Backend(t, "op-envelope")
	store := New(backend, id)

	store.Put(ctx, "test.md", strings.NewReader("with envelope"))

	keys, _ := backend.List(ctx, "ops/")
	if len(keys) == 0 {
		t.Fatal("no ops written")
	}

	rc, _ := backend.Get(ctx, keys[0])
	var raw bytes.Buffer
	raw.ReadFrom(rc)
	rc.Close()

	if raw.Len() < OpEnvelopeSize {
		t.Fatalf("op too short: %d bytes", raw.Len())
	}
	b := raw.Bytes()
	if b[0] != 'O' || b[1] != 'P' || b[2] != 'S' {
		t.Error("op missing OPS magic header")
	}
}
