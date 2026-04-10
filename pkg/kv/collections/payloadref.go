package collections

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	// PayloadKindChunkedKV references a payload stored in chunked KV state.
	PayloadKindChunkedKV = "chunked_kv"
	// PayloadKindSkyFS references a payload stored in skyfs.
	PayloadKindSkyFS = "skyfs"
	// PayloadKindSealedObject references a sealed opaque object in indirect
	// storage for a specific recipient.
	PayloadKindSealedObject = "sealed_object"
)

// PayloadRef points to payload bytes stored outside the inline mailbox
// envelope.
type PayloadRef struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Size   int    `json:"size"`
	Digest string `json:"digest,omitempty"` // sha256:<hex>
}

// Validate checks whether the reference is structurally valid.
func (r PayloadRef) Validate() error {
	if r.Kind == "" {
		return fmt.Errorf("payload ref kind is required")
	}
	if r.Key == "" {
		return fmt.Errorf("payload ref key is required")
	}
	if r.Size < 0 {
		return fmt.Errorf("payload ref size must be non-negative")
	}
	if r.Digest != "" && len(r.Digest) < len("sha256:")+64 {
		return fmt.Errorf("payload ref digest %q is too short", r.Digest)
	}
	return nil
}

// ShouldInline reports whether a payload of size bytes should stay inline.
func ShouldInline(size, maxInline int) bool {
	if maxInline <= 0 {
		return false
	}
	return size <= maxInline
}

// NewPayloadRef constructs a validated payload reference.
func NewPayloadRef(kind, key string, size int, digest string) (PayloadRef, error) {
	ref := PayloadRef{
		Kind:   kind,
		Key:    key,
		Size:   size,
		Digest: digest,
	}
	if err := ref.Validate(); err != nil {
		return PayloadRef{}, err
	}
	return ref, nil
}

// SHA256Digest returns a canonical sha256 digest string for data.
func SHA256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
