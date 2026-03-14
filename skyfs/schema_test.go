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
		t.Errorf("version = %d, want %d", schema.Version, SchemaVersion)
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

func TestValidateSchemaTooNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	// Write a schema from the "future"
	WriteSchema(ctx, backend)
	schema, _ := ReadSchema(ctx, backend)
	schema.Version = SchemaVersion + 5

	// Overwrite with future version
	data, _ := json.Marshal(schema)
	r := bytes.NewReader(data)
	backend.Put(ctx, schemaKey, r, int64(len(data)))

	err := ValidateSchema(ctx, backend)
	if !errors.Is(err, ErrIncompatibleSchema) {
		t.Errorf("got %v, want ErrIncompatibleSchema", err)
	}
}

func TestValidateSchemaTooOld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()

	// Write a schema from the "past"
	WriteSchema(ctx, backend)
	schema, _ := ReadSchema(ctx, backend)
	schema.Version = 1

	data, _ := json.Marshal(schema)
	r := bytes.NewReader(data)
	backend.Put(ctx, schemaKey, r, int64(len(data)))

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

func TestCurrentSchema(t *testing.T) {
	t.Parallel()

	s := CurrentSchema()
	if s.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", s.Version, SchemaVersion)
	}
	if s.BlobFormat != "v2" {
		t.Errorf("blob format = %q, want v2", s.BlobFormat)
	}
}
