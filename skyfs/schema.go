package skyfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sky10/sky10/skyadapter"
)

// SchemaVersion is the current storage schema version (semver).
// This version appears in sky10.schema AND as a prefix on every blob.
// One version. Everywhere.
//
// Major: breaking changes (cipher, hash, key derivation, blob format)
// Minor: backward-compatible additions (new optional manifest fields)
// Patch: bug fixes that don't change data format
const SchemaVersion = "1.0.0"

const schemaKey = "sky10.schema"

// Schema describes the algorithms and format versions used in a bucket.
// Written once on init, read on every open. The same version string is
// embedded in every encrypted blob so any blob can self-describe its
// encryption format.
type Schema struct {
	Version       string `json:"version"`
	HashAlgorithm string `json:"hash_algorithm"`
	KDF           string `json:"kdf"`
	Cipher        string `json:"cipher"`
	KeyWrap       string `json:"key_wrap"`
}

// CurrentSchema returns the schema for the current code version.
func CurrentSchema() Schema {
	return Schema{
		Version:       SchemaVersion,
		HashAlgorithm: "sha3-256",
		KDF:           "hkdf-sha3-256",
		Cipher:        "aes-256-gcm",
		KeyWrap:       "ephemeral-ecdh-x25519-hkdf-sha3-256-aes-256-gcm",
	}
}

var (
	ErrIncompatibleSchema = errors.New("incompatible schema version")
	ErrNoSchema           = errors.New("no schema found")
)

// WriteSchema writes the current schema to the bucket.
func WriteSchema(ctx context.Context, backend skyadapter.Backend) error {
	data, err := json.MarshalIndent(CurrentSchema(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}
	r := bytes.NewReader(data)
	return backend.Put(ctx, schemaKey, r, int64(len(data)))
}

// ReadSchema reads the schema from the bucket.
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
		return nil, fmt.Errorf("reading schema: %w", err)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}
	return &schema, nil
}

// ValidateSchema checks that the bucket's schema major version matches.
func ValidateSchema(ctx context.Context, backend skyadapter.Backend) error {
	schema, err := ReadSchema(ctx, backend)
	if err != nil {
		return err
	}

	bucketMajor := semverMajor(schema.Version)
	codeMajor := semverMajor(SchemaVersion)

	if bucketMajor > codeMajor {
		return fmt.Errorf("%w: bucket is v%s, code supports v%s — upgrade skyfs",
			ErrIncompatibleSchema, schema.Version, SchemaVersion)
	}
	if bucketMajor < codeMajor {
		return fmt.Errorf("%w: bucket is v%s, code expects v%s — run skyfs migrate",
			ErrIncompatibleSchema, schema.Version, SchemaVersion)
	}
	return nil
}

func semverMajor(v string) int {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[0])
	return n
}

// blobPrefix is the binary header prepended to every encrypted blob.
// Format: "SKY" magic (3 bytes) + schema major version (1 byte).
// Total: 4 bytes. Every blob is self-describing.
var blobPrefix = []byte{'S', 'K', 'Y', byte(semverMajor(SchemaVersion))}

// PrependBlobHeader adds the schema header to encrypted data.
// Format: [S K Y <major_version> | encrypted_data]
func PrependBlobHeader(encrypted []byte) []byte {
	out := make([]byte, len(blobPrefix)+len(encrypted))
	copy(out, blobPrefix)
	copy(out[len(blobPrefix):], encrypted)
	return out
}

// StripBlobHeader removes and validates the blob header.
// Returns the encrypted data and the schema major version.
// Legacy blobs (no header) are detected by the absence of the "SKY" magic.
func StripBlobHeader(data []byte) ([]byte, int, error) {
	if len(data) == 0 {
		return nil, 0, errors.New("empty blob")
	}

	// Check for "SKY" magic bytes
	if len(data) >= 4 && data[0] == 'S' && data[1] == 'K' && data[2] == 'Y' {
		version := int(data[3])
		return data[4:], version, nil
	}

	// No magic — legacy unversioned blob
	return data, 0, nil
}
