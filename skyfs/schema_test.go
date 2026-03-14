package skyfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	s3adapter "github.com/sky10/sky10/skyadapter/s3"
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
	if schema.KDF != "hkdf-sha3-256" {
		t.Errorf("kdf = %q, want hkdf-sha3-256", schema.KDF)
	}
	if schema.Cipher != "aes-256-gcm" {
		t.Errorf("cipher = %q, want aes-256-gcm", schema.Cipher)
	}
	if schema.BlobVersion != int(BlobVersion) {
		t.Errorf("blob version = %d, want %d", schema.BlobVersion, BlobVersion)
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

	// Same major, different minor — should be compatible
	WriteSchema(ctx, backend)
	schema, _ := ReadSchema(ctx, backend)
	schema.Version = "1.5.0"
	data, _ := json.Marshal(schema)
	backend.Put(ctx, schemaKey, bytes.NewReader(data), int64(len(data)))

	if err := ValidateSchema(ctx, backend); err != nil {
		t.Errorf("minor version difference should be compatible: %v", err)
	}
}

func TestValidateSchemaTooNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	WriteSchema(ctx, backend)
	schema, _ := ReadSchema(ctx, backend)
	schema.Version = "5.0.0"
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

	// Write a v0 schema (older major than current v1)
	schema := Schema{Version: "0.9.0"}
	data, _ := json.Marshal(schema)
	backend.Put(ctx, schemaKey, bytes.NewReader(data), int64(len(data)))

	err := ValidateSchema(ctx, backend)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Errorf("got %v, want ErrIncompatibleSchema", err)
	}
}

func TestValidateSchemaNoSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	err := ValidateSchema(ctx, backend)
	if !errors.Is(err, ErrNoSchema) {
		t.Errorf("got %v, want ErrNoSchema", err)
	}
}

func TestPrependStripBlobVersion(t *testing.T) {
	t.Parallel()

	original := []byte("encrypted data here")
	versioned := PrependBlobVersion(original)

	if versioned[0] != BlobVersion {
		t.Errorf("first byte = %x, want %x", versioned[0], BlobVersion)
	}
	if len(versioned) != len(original)+1 {
		t.Errorf("length = %d, want %d", len(versioned), len(original)+1)
	}

	stripped, version, err := StripBlobVersion(versioned)
	if err != nil {
		t.Fatalf("StripBlobVersion: %v", err)
	}
	if version != BlobVersion {
		t.Errorf("version = %x, want %x", version, BlobVersion)
	}
	if !bytes.Equal(stripped, original) {
		t.Error("stripped data doesn't match original")
	}
}

func TestStripBlobVersionLegacy(t *testing.T) {
	t.Parallel()

	// Legacy data starts with a random nonce byte (likely > BlobVersion)
	legacy := []byte{0xFF, 0xAB, 0xCD, 0xEF}
	stripped, version, err := StripBlobVersion(legacy)
	if err != nil {
		t.Fatalf("StripBlobVersion: %v", err)
	}
	if version != 0 {
		t.Errorf("legacy version = %d, want 0", version)
	}
	if !bytes.Equal(stripped, legacy) {
		t.Error("legacy data should pass through unchanged")
	}
}

func TestStripBlobVersionEmpty(t *testing.T) {
	t.Parallel()

	_, _, err := StripBlobVersion(nil)
	if err == nil {
		t.Error("expected error for empty blob")
	}
}

func TestSemverMajor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    int
	}{
		{"1.0.0", 1},
		{"2.3.4", 2},
		{"0.1.0", 0},
		{"10.0.0", 10},
	}

	for _, tt := range tests {
		got := semverMajor(tt.version)
		if got != tt.want {
			t.Errorf("semverMajor(%q) = %d, want %d", tt.version, got, tt.want)
		}
	}
}
