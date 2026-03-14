# skyfs v1 — Implementation Plan

Status: **complete**
Created: 2026-03-14
Completed: 2026-03-14

## Goal

A working encrypted file storage library and CLI. Single-user, AES-256-GCM,
backed by S3-compatible storage. Put a file in, get it back, list what's there.
Everything encrypted before it leaves the device.

```
skyfs init                          # generate keypair, configure bucket
skyfs put ./file.txt                # encrypt + store
skyfs get journal/2026-03-14.md     # retrieve + decrypt
skyfs ls                            # list files
skyfs rm old-note.md                # delete
```

## Out of Scope (v1)

These are real features but belong in later versions:

- Multi-party key sharing (skydb concern)
- Ops log + concurrent writes (multi-device, v2)
- Pack files (optimization, v2)
- Local SQLite index (sync state, v2)
- File watching / sync daemon (skyshare, v3)
- Presigned URLs (skylink concern)
- FUSE mount
- Mobile

## Package Layout

```
sky10/
├── go.mod                          module github.com/sky10/sky10
├── cmd/
│   └── skyfs/
│       └── main.go                 CLI entry point
├── skyfs/
│   ├── skyfs.go                    top-level API (Put, Get, List, Remove, Info)
│   ├── crypto.go                   AES-256-GCM, key generation, key wrapping
│   ├── identity.go                 Ed25519 keypair, load/save
│   ├── keys.go                     key hierarchy (user → namespace → file)
│   ├── chunk.go                    streaming chunker (4MB max)
│   ├── manifest.go                 encrypted file tree ↔ blob mapping
│   ├── *_test.go                   tests alongside each file
│   └── edge_test.go                edge case tests
├── skyadapter/
│   ├── adapter.go                  Backend interface (io.Reader/Writer streaming)
│   └── s3/
│       └── s3.go                   S3 implementation + MemoryBackend for tests
└── internal/
    └── config/
        └── config.go               ~/.skyfs/ config loading
```

## Dependencies

```
stdlib:
  crypto/aes, crypto/cipher        AES-256-GCM
  crypto/ed25519                    identity, signing
  crypto/ecdh                       X25519 key exchange
  crypto/sha256                     content addressing
  crypto/rand                       key generation
  encoding/json                     manifest, config
  flag                              CLI

golang.org/x/crypto:
  hkdf                              key derivation

external:
  github.com/aws/aws-sdk-go-v2     S3 client (modular, service/s3 + config only)

not yet used:
  modernc.org/sqlite                deferred to v2 (not needed until sync state)
  github.com/jotfs/fastcdc-go       deferred — using fixed-size chunking for now (see notes)
```

---

## Milestone 1: Project Scaffolding + S3 Backend ✓

The skeleton. Prove we can talk to S3.

### Checklist

- [x] `go mod init github.com/sky10/sky10`
- [x] Create directory structure: `cmd/skyfs/`, `skyfs/`, `skyadapter/`, `skyadapter/s3/`
- [x] Define `skyadapter.Backend` interface:
  ```go
  type Backend interface {
      Put(ctx context.Context, key string, r io.Reader, size int64) error
      Get(ctx context.Context, key string) (io.ReadCloser, error)
      Delete(ctx context.Context, key string) error
      List(ctx context.Context, prefix string) ([]string, error)
      Head(ctx context.Context, key string) (ObjectMeta, error)
  }
  ```
  Streaming from day one. Put takes a Reader + size, Get returns a ReadCloser.
- [x] Implement S3 backend (`skyadapter/s3/`)
  - Constructor: `New(ctx, Config) (*Backend, error)`
  - Support custom endpoint for B2/R2/MinIO
  - `ForcePathStyle` option for MinIO compatibility
- [x] In-memory `MemoryBackend` for unit tests (no S3 needed)
- [x] Tests: put, get, delete, list, head, overwrite, not-found errors

---

## Milestone 2: Crypto Primitives ✓

### Checklist

- [x] `skyfs/identity.go` — Ed25519 keypair
  - `GenerateIdentity() (*Identity, error)`
  - `LoadIdentity(path string) (*Identity, error)`
  - `SaveIdentity(id *Identity, path string) error`
  - `ID()` returns `sky://k1_...` format
- [x] `skyfs/crypto.go` — symmetric encryption
  - `Encrypt(plaintext, key []byte) ([]byte, error)` — AES-256-GCM, random nonce prepended
  - `Decrypt(ciphertext, key []byte) ([]byte, error)` — extract nonce, decrypt
  - `GenerateKey() ([]byte, error)` — 32 random bytes
  - Operates at chunk level (max 4MB). Streaming happens above.
- [x] `skyfs/crypto.go` — asymmetric key wrapping
  - `WrapKey()` — ephemeral ECDH (X25519) + HKDF + AES-256-GCM
  - `UnwrapKey()` — reverse the process
  - Ed25519 → X25519 conversion via SHA-256 of seed
- [x] Tests: encrypt/decrypt round-trip, wrong key fails, tampered ciphertext
  fails, wrap/unwrap round-trip, non-deterministic encryption (nonce uniqueness),
  invalid key sizes, key derivation determinism

---

## Milestone 3: Encrypted File Storage (single chunk) ✓

### Checklist

- [x] `skyfs/skyfs.go` — top-level `Store` type
- [x] `Store.Put(ctx, path string, r io.Reader) error` — streaming
- [x] `Store.Get(ctx, path string, w io.Writer) error` — streaming
- [x] Integration test: put a file, get it back, compare bytes
- [x] Test: blob in S3 is not plaintext

### Note

Milestones 3-6 were implemented together since they're tightly coupled.
The Store always uses the full key hierarchy and manifest — there was no
intermediate "raw" stage in the final implementation.

---

## Milestone 4: Key Hierarchy + Namespaces ✓

### Checklist

- [x] `skyfs/keys.go` — namespace key management
  - `NamespaceFromPath()` — top-level directory, or "default" for root files
  - `GenerateNamespaceKey()` / `WrapNamespaceKey()` / `UnwrapNamespaceKey()`
  - `DeriveFileKey(nsKey, contentHash)` — HKDF-SHA256
- [x] S3 key layout: `keys/namespaces/{namespace}.ns.enc`
- [x] Namespace keys created lazily on first put, cached in memory
- [x] Tests: namespace derivation from paths, key isolation between namespaces,
  deterministic file key derivation, caching (one key per namespace in S3)

---

## Milestone 5: Chunking ✓

### Checklist

- [x] `skyfs/chunk.go` — streaming chunker
  - `NewChunker(r io.Reader) *Chunker`
  - `(*Chunker) Next() (Chunk, error)` — returns io.EOF when done
  - Fixed-size chunking at 4MB max (see note below)
  - SHA-256 hash per chunk
- [x] Content-addressed storage: `blobs/{hash[0:2]}/{hash[2:4]}/{hash}.enc`
- [x] Deduplication via `Head()` check before upload
- [x] Tests: small file = 1 chunk, large file (10MB) = multiple chunks,
  deterministic chunking, exact boundary, empty file, reassembly

### Note: Fixed-size vs CDC

v1 uses fixed-size chunking (4MB max) instead of FastCDC. This is correct
for all operations but doesn't give dedup benefits on edits within large
files. FastCDC (`jotfs/fastcdc-go`) can be swapped in later — the Chunker
interface is the same, only the split-point logic changes.

---

## Milestone 6: Manifest ✓

### Checklist

- [x] `skyfs/manifest.go` — Manifest + FileEntry types
- [x] `LoadManifest()` / `SaveManifest()` — encrypted with key derived from identity
- [x] Returns empty manifest if none exists yet (first use)
- [x] `Set()`, `Remove()`, `ListPrefix()`
- [x] Tests: save/load round-trip, encrypted at rest, wrong identity can't
  decrypt, prefix filtering, sorted listing

---

## Milestone 7: CLI ✓

### Checklist

- [x] `cmd/skyfs/main.go` — subcommand dispatch via `flag`
- [x] `skyfs init --bucket <name> [--region r] [--endpoint url] [--path-style]`
- [x] `skyfs put <file> [--as <remote-path>]`
- [x] `skyfs get <path> [--out <local-path>]`
- [x] `skyfs ls [prefix]` — tabwriter output
- [x] `skyfs rm <path>`
- [x] `skyfs info` — identity, file count, total size, namespaces
- [x] Error handling: missing config, file not found, clean error messages
- [x] `formatSize()` for human-readable sizes (B/KB/MB/GB)

---

## Milestone 8: Tests + Hardening ✓

### Checklist

- [x] 55 tests total, all passing
- [x] Edge cases tested:
  - Empty file (0 bytes)
  - Binary files
  - Unicode filenames (Japanese, German, emoji, Cyrillic)
  - Deeply nested paths (7 levels)
  - Paths with spaces
  - Exact chunk boundary (2 × MaxChunkSize)
  - Large file (10MB, multiple chunks)
  - Overwrite existing file
- [x] `go vet ./...` clean
- [x] `gofmt` on all files
- [x] Security:
  - Identity file written with 0600 permissions
  - Config directory 0700
  - Random nonces per encryption (tested: non-deterministic)
  - Hash verification on every chunk decrypt
  - Wrong key / tampered data returns error
  - Plaintext never appears in S3 (tested: raw blob inspection)
- [x] Correctness:
  - All errors wrapped with context (`fmt.Errorf("doing x: %w", err)`)
  - ErrNotFound / ErrFileNotFound sentinel errors

---

## Streaming Design

Streaming is built in from day one. The memory model:

```
File level:   io.Reader / io.Writer — never load full file into memory
Chunk level:  []byte — bounded at max 4MB (MaxChunkSize)
Crypto level: []byte — AES-256-GCM operates on one chunk at a time
```

A 10GB file streams through the chunker, which yields chunks up to 4MB.
Each chunk is encrypted and uploaded independently. At no point is more
than ~4MB of file data in memory. The same applies to download: chunks
are fetched, decrypted, and written to the output writer one at a time.

---

## Config File Format

```json
{
  "bucket": "my-sky-bucket",
  "region": "us-west-004",
  "endpoint": "https://s3.us-west-004.backblazeb2.com",
  "identity_file": "~/.skyfs/identity.key"
}
```

Credentials via environment variables:
`S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`. Not stored in config file.

---

## S3 Layout (v1)

```
s3://bucket/
  manifests/
    current.enc                     encrypted manifest (single-writer, overwritten)

  blobs/
    ab/cd/abcdef1234...enc          encrypted file chunks (content-addressed)
    ef/gh/efgh5678ab...enc

  keys/
    namespaces/
      journal.ns.enc                namespace key wrapped for user
      default.ns.enc
```

v1 does not use: `ops/` (no ops log), `packs/` (no pack files),
`pack-index.enc`, or manifest snapshots. These are v2.

---

## What's Next (v2)

- [ ] FastCDC content-defined chunking (replace fixed-size)
- [ ] Ops log for concurrent multi-device writes
- [ ] Pack files for bundling small chunks
- [ ] Local SQLite index for sync state
- [ ] Key rotation commands
- [ ] Blob garbage collection (orphaned chunks after remove)
