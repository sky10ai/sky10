package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sky10/sky10/pkg/adapter"
)

// SchemaVersion is the current storage schema version (semver).
// This version appears in sky10.schema AND as a prefix on every blob.
// One version. Everywhere.
//
// Major: breaking changes (cipher, hash, key derivation, blob format)
// Minor: backward-compatible additions (new optional manifest fields)
// Patch: bug fixes that don't change data format
const SchemaVersion = "1.1.0"

const schemaKey = "fs/schema"

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
func WriteSchema(ctx context.Context, backend adapter.Backend) error {
	data, err := json.MarshalIndent(CurrentSchema(), "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}
	r := bytes.NewReader(data)
	return backend.Put(ctx, schemaKey, r, int64(len(data)))
}

// ReadSchema reads the schema from the bucket.
func ReadSchema(ctx context.Context, backend adapter.Backend) (*Schema, error) {
	rc, err := backend.Get(ctx, schemaKey)
	if err != nil {
		if errors.Is(err, adapter.ErrNotFound) {
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
func ValidateSchema(ctx context.Context, backend adapter.Backend) error {
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

// BlobHeaderSize is the fixed size of the blob header: "SKY" + major + minor + patch.
const BlobHeaderSize = 6

// BlobHeader is prepended to every encrypted blob.
// Format: [S K Y major minor patch | encrypted_data]
// 6 bytes. The version tells you everything — algorithms, format, how to parse.
type BlobHeader struct {
	Major byte
	Minor byte
	Patch byte
}

// CurrentBlobHeader returns the header for the current schema version.
func CurrentBlobHeader() BlobHeader {
	parts := strings.SplitN(SchemaVersion, ".", 3)
	var major, minor, patch int
	if len(parts) >= 1 {
		major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		patch, _ = strconv.Atoi(parts[2])
	}
	return BlobHeader{Major: byte(major), Minor: byte(minor), Patch: byte(patch)}
}

// PrependBlobHeader adds the 6-byte schema header to encrypted data.
func PrependBlobHeader(encrypted []byte) []byte {
	h := CurrentBlobHeader()
	out := make([]byte, BlobHeaderSize+len(encrypted))
	out[0] = 'S'
	out[1] = 'K'
	out[2] = 'Y'
	out[3] = h.Major
	out[4] = h.Minor
	out[5] = h.Patch
	copy(out[BlobHeaderSize:], encrypted)
	return out
}

// StripBlobHeader removes and parses the 6-byte header.
// Returns the encrypted data and the parsed header.
// Legacy blobs (no "SKY" magic) return a zero header.
func StripBlobHeader(data []byte) ([]byte, BlobHeader, error) {
	if len(data) == 0 {
		return nil, BlobHeader{}, errors.New("empty blob")
	}

	if len(data) >= BlobHeaderSize && data[0] == 'S' && data[1] == 'K' && data[2] == 'Y' {
		h := BlobHeader{
			Major: data[3],
			Minor: data[4],
			Patch: data[5],
		}
		return data[BlobHeaderSize:], h, nil
	}

	// No magic — legacy unversioned blob
	return data, BlobHeader{}, nil
}
