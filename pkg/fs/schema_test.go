package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestWriteReadSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	if err := WriteSchema(ctx, backend); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	schema, err := ReadSchema(ctx, backend)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}

	if schema.Version != SchemaVersion {
		t.Errorf("version = %q, want %q", schema.Version, SchemaVersion)
	}
	if schema.HashAlgorithm != "sha3-256" {
		t.Errorf("hash = %q, want sha3-256", schema.HashAlgorithm)
	}
	if schema.Cipher != "aes-256-gcm" {
		t.Errorf("cipher = %q, want aes-256-gcm", schema.Cipher)
	}
}

func TestReadSchemaNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	_, err := ReadSchema(ctx, backend)
	if !errors.Is(err, ErrNoSchema) {
		t.Errorf("got %v, want ErrNoSchema", err)
	}
}

func TestValidateSchemaCompatible(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	WriteSchema(ctx, backend)
	if err := ValidateSchema(ctx, backend); err != nil {
		t.Errorf("ValidateSchema: %v", err)
	}
}

func TestValidateSchemaMinorDifference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	WriteSchema(ctx, backend)
	schema, _ := ReadSchema(ctx, backend)
	schema.Version = "1.5.0"
	data, _ := json.Marshal(schema)
	backend.Put(ctx, schemaKey, bytes.NewReader(data), int64(len(data)))

	if err := ValidateSchema(ctx, backend); err != nil {
		t.Errorf("minor difference should be compatible: %v", err)
	}
}

func TestValidateSchemaTooNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	schema := Schema{Version: "5.0.0"}
	data, _ := json.Marshal(schema)
	backend.Put(ctx, schemaKey, bytes.NewReader(data), int64(len(data)))

	err := ValidateSchema(ctx, backend)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Errorf("got %v, want ErrIncompatibleSchema", err)
	}
}

func TestValidateSchemaTooOld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	schema := Schema{Version: "0.9.0"}
	data, _ := json.Marshal(schema)
	backend.Put(ctx, schemaKey, bytes.NewReader(data), int64(len(data)))

	err := ValidateSchema(ctx, backend)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Errorf("got %v, want ErrIncompatibleSchema", err)
	}
}

func TestBlobHeader(t *testing.T) {
	t.Parallel()

	original := []byte("encrypted payload")
	withHeader := PrependBlobHeader(original)

	// Check magic bytes
	if string(withHeader[:3]) != "SKY" {
		t.Errorf("magic = %q, want SKY", withHeader[:3])
	}

	// Check header size
	if len(withHeader) != BlobHeaderSize+len(original) {
		t.Errorf("header+data = %d, want %d", len(withHeader), BlobHeaderSize+len(original))
	}

	// Check version bytes
	h := CurrentBlobHeader()
	if withHeader[3] != h.Major || withHeader[4] != h.Minor || withHeader[5] != h.Patch {
		t.Errorf("version = %d.%d.%d, want %d.%d.%d",
			withHeader[3], withHeader[4], withHeader[5], h.Major, h.Minor, h.Patch)
	}

	// Strip and verify
	stripped, parsed, err := StripBlobHeader(withHeader)
	if err != nil {
		t.Fatalf("StripBlobHeader: %v", err)
	}
	if parsed.Major != h.Major || parsed.Minor != h.Minor || parsed.Patch != h.Patch {
		t.Errorf("parsed version mismatch")
	}
	if !bytes.Equal(stripped, original) {
		t.Error("stripped doesn't match original")
	}
}

func TestBlobHeaderLegacy(t *testing.T) {
	t.Parallel()

	legacy := []byte{0xFF, 0xAB, 0xCD, 0xEF, 0x01, 0x02}
	stripped, h, err := StripBlobHeader(legacy)
	if err != nil {
		t.Fatalf("StripBlobHeader: %v", err)
	}
	if h.Major != 0 || h.Minor != 0 || h.Patch != 0 {
		t.Errorf("legacy should have zero header, got %d.%d.%d", h.Major, h.Minor, h.Patch)
	}
	if !bytes.Equal(stripped, legacy) {
		t.Error("legacy data should pass through unchanged")
	}
}

func TestBlobHeaderEmpty(t *testing.T) {
	t.Parallel()

	_, _, err := StripBlobHeader(nil)
	if err == nil {
		t.Error("expected error for empty blob")
	}
}

func TestSemverMajor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		v    string
		want int
	}{
		{"1.0.0", 1},
		{"2.3.4", 2},
		{"0.1.0", 0},
		{"10.0.0", 10},
	}
	for _, tt := range tests {
		if got := semverMajor(tt.v); got != tt.want {
			t.Errorf("semverMajor(%q) = %d, want %d", tt.v, got, tt.want)
		}
	}
}
