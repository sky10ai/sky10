// Package skyadapter defines the storage backend interface for skyfs.
//
// All backends (S3, IPFS, Arweave, local filesystem) implement the Backend
// interface. This keeps skyfs storage-agnostic — swap the backend without
// changing any encryption or chunking logic.
package skyadapter

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when a requested key does not exist.
var ErrNotFound = errors.New("not found")

// ObjectMeta contains metadata about a stored object.
type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Backend is the storage abstraction for skyfs. Implementations store and
// retrieve opaque encrypted blobs. The backend never sees plaintext.
type Backend interface {
	// Put stores data from r under the given key. Size must be the exact
	// number of bytes that r will produce.
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a reader for the data stored at key. The caller must
	// close the returned ReadCloser. Returns ErrNotFound if the key does
	// not exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object at key. Returns ErrNotFound if the key
	// does not exist.
	Delete(ctx context.Context, key string) error

	// List returns all keys with the given prefix, sorted lexicographically.
	List(ctx context.Context, prefix string) ([]string, error)

	// Head returns metadata for the object at key without downloading its
	// contents. Returns ErrNotFound if the key does not exist.
	Head(ctx context.Context, key string) (ObjectMeta, error)

	// GetRange returns a reader for a byte range within the object at key.
	// The range is [offset, offset+length). Returns ErrNotFound if the key
	// does not exist.
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
}
