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
// Major: breaking changes (cipher, hash, key derivation, blob format)
// Minor: backward-compatible additions (new optional manifest fields)
// Patch: bug fixes that don't change data format
const SchemaVersion = "1.0.0"

const schemaKey = "sky10.schema"

// BlobVersion is prepended to every encrypted blob. Allows any tool to
// identify the encryption format without the schema file.
const BlobVersion byte = 0x01

// Schema describes the algorithms and format versions used in a bucket.
// Written once on init, read on every open.
type Schema struct {
	Version       string `json:"version"`
	HashAlgorithm string `json:"hash_algorithm"`
	KDF           string `json:"kdf"`
	Cipher        string `json:"cipher"`
	KeyWrap       string `json:"key_wrap"`
	BlobVersion   int    `json:"blob_version"`
}

// CurrentSchema returns the schema for the current code version.
func CurrentSchema() Schema {
	return Schema{
		Version:       SchemaVersion,
		HashAlgorithm: "sha3-256",
		KDF:           "hkdf-sha3-256",
		Cipher:        "aes-256-gcm",
		KeyWrap:       "ephemeral-ecdh-x25519-hkdf-sha3-256-aes-256-gcm",
		BlobVersion:   int(BlobVersion),
	}
}

var (
	// ErrIncompatibleSchema is returned when a bucket's schema major version
	// doesn't match the current code.
	ErrIncompatibleSchema = errors.New("incompatible schema version")

	// ErrNoSchema is returned when a bucket has no schema file.
	ErrNoSchema = errors.New("no schema found")
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

// ValidateSchema checks that the bucket's schema major version matches
// the current code. Minor/patch differences are compatible.
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

// PrependBlobVersion adds the blob version byte to encrypted data.
func PrependBlobVersion(encrypted []byte) []byte {
	out := make([]byte, 1+len(encrypted))
	out[0] = BlobVersion
	copy(out[1:], encrypted)
	return out
}

// StripBlobVersion removes and returns the blob version byte.
// Legacy data (no version prefix) is detected by checking if the first
// byte is a known version — AES-GCM nonces are random so collisions
// with low version numbers are handled by the caller.
func StripBlobVersion(data []byte) ([]byte, byte, error) {
	if len(data) == 0 {
		return nil, 0, errors.New("empty blob")
	}
	version := data[0]
	if version >= 0x01 && version <= BlobVersion {
		return data[1:], version, nil
	}
	// Not a known version byte — treat as legacy unversioned data
	return data, 0, nil
}
