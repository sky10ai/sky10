package skyfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/sky10/sky10/skyadapter"
)

// SchemaVersion is the current storage schema version.
// Increment this when making breaking changes to:
//   - encryption format (cipher, nonce size)
//   - content hash algorithm (SHA-256 → SHA3-256)
//   - key derivation (HKDF hash function)
//   - key wrapping format
//   - manifest/ops JSON structure
//   - blob naming convention
const SchemaVersion = 2

const schemaKey = "sky10.schema"

// Schema describes the algorithms and format versions used in a bucket.
// Written once on init, read on every open. If the schema version is
// newer than what the code supports, refuse to open (prevents silent
// data corruption).
type Schema struct {
	// Version is incremented on breaking changes.
	Version int `json:"version"`

	// Hash algorithm used for content addressing (chunk hashes).
	HashAlgorithm string `json:"hash_algorithm"`

	// KDF used for key derivation (HKDF, manifest key, file key).
	KDF string `json:"kdf"`

	// Cipher used for symmetric encryption.
	Cipher string `json:"cipher"`

	// KeyWrap describes the asymmetric key wrapping scheme.
	KeyWrap string `json:"key_wrap"`

	// BlobFormat describes the encrypted blob layout.
	// "v1" = [nonce | ciphertext+tag], no prefix byte
	// "v2" = [version_byte | nonce | ciphertext+tag]
	BlobFormat string `json:"blob_format"`
}

// CurrentSchema returns the schema for the current code version.
func CurrentSchema() Schema {
	return Schema{
		Version:       SchemaVersion,
		HashAlgorithm: "sha3-256",
		KDF:           "hkdf-sha3-256",
		Cipher:        "aes-256-gcm",
		KeyWrap:       "ephemeral-ecdh-x25519-hkdf-sha3-256-aes-256-gcm",
		BlobFormat:    "v2",
	}
}

// ErrIncompatibleSchema is returned when a bucket's schema version is
// newer than what this code supports.
var ErrIncompatibleSchema = errors.New("incompatible schema version")

// ErrNoSchema is returned when a bucket has no schema file (pre-versioning data).
var ErrNoSchema = errors.New("no schema found")

// WriteSchema writes the current schema to the bucket. Called during init.
func WriteSchema(ctx context.Context, backend skyadapter.Backend) error {
	schema := CurrentSchema()
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	r := bytes.NewReader(data)
	if err := backend.Put(ctx, schemaKey, r, int64(len(data))); err != nil {
		return fmt.Errorf("writing schema: %w", err)
	}

	return nil
}

// ReadSchema reads the schema from the bucket. Returns ErrNoSchema if
// no schema file exists (legacy/pre-versioning bucket).
func ReadSchema(ctx context.Context, backend skyadapter.Backend) (*Schema, error) {
	rc, err := backend.Get(ctx, schemaKey)
	if err != nil {
		if errors.Is(err, skyadapter.ErrNotFound) {
			return nil, ErrNoSchema
		}
		return nil, fmt.Errorf("reading schema: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading schema data: %w", err)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}

	return &schema, nil
}

// ValidateSchema checks that the bucket's schema is compatible with
// the current code. Returns nil if compatible, error if not.
func ValidateSchema(ctx context.Context, backend skyadapter.Backend) error {
	schema, err := ReadSchema(ctx, backend)
	if err != nil {
		if errors.Is(err, ErrNoSchema) {
			// Legacy bucket — no schema file. Could be v1 data.
			// Caller decides whether to migrate or reject.
			return ErrNoSchema
		}
		return err
	}

	if schema.Version > SchemaVersion {
		return fmt.Errorf("%w: bucket is schema v%d, code supports up to v%d — upgrade skyfs",
			ErrIncompatibleSchema, schema.Version, SchemaVersion)
	}

	if schema.Version < SchemaVersion {
		return fmt.Errorf("%w: bucket is schema v%d, code expects v%d — run skyfs migrate",
			ErrIncompatibleSchema, schema.Version, SchemaVersion)
	}

	return nil
}
