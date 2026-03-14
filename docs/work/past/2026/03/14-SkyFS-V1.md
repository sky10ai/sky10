---
created: 2026-03-14
model: claude-opus-4-6
---

# SkyFS V1 — Initial Implementation

## Problems Solved

### Project Scaffolding
- Initialized Go module (`github.com/sky10/sky10`)
- Established package layout: `skyfs/`, `skyadapter/`, `cmd/skyfs/`, `internal/config/`
- Chose dependencies: aws-sdk-go-v2 for S3, x/crypto/hkdf for key derivation, stdlib for everything else

### Storage Backend Abstraction
- Defined `skyadapter.Backend` interface with streaming I/O (`io.Reader`/`io.ReadCloser`)
- Implemented S3 backend supporting any S3-compatible store (B2, R2, MinIO)
- Built in-memory `MemoryBackend` for unit tests — no S3 needed to run test suite
- Credential resolution: `S3_*` env vars first, falls back to AWS SDK chain

### Cryptography
- AES-256-GCM encryption with random nonce prepended to ciphertext
- Ed25519 identity (keypair generation, save/load with 0600 permissions)
- Asymmetric key wrapping via ephemeral ECDH: Ed25519 → X25519 conversion, HKDF-SHA256 derived wrapping key
- Three-layer key hierarchy: user key → namespace key → file key (HKDF derivation)

### Streaming File Storage
- Streaming from day one — never loads full file into memory
- Chunker iterator: `NewChunker(io.Reader)` yields bounded chunks (max 4MB)
- Content-addressed blob storage with two-level prefix (`blobs/ab/cd/...`)
- Deduplication via `Head()` check before upload
- Encrypted manifest tracks file tree → chunk mappings

### CLI
- `skyfs init` — generate keypair, configure bucket, create initial manifest
- `skyfs put/get/ls/rm/info` — full file lifecycle
- Version injection via `-ldflags` at build time

### Build System
- Makefile with deterministic builds: `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`
- `make reproduce` — builds twice, compares SHA-256 to prove determinism
- Cross-compilation targets for linux/darwin × amd64/arm64

### Testing
- 55 tests covering: crypto round-trips, key wrapping, chunking, dedup, namespace isolation, encrypted-at-rest verification, unicode paths, binary files, empty files, chunk boundaries
- `go vet` clean, `gofmt` clean

## Decisions Made

- **Fixed-size chunking over FastCDC** — correct for all operations, deferred CDC to v2
- **Ed25519 → X25519 via SHA-256 of seed** — avoids internal Ed25519 implementation details
- **Single manifest file** — no ops log in v1 (single-writer assumption)
- **No SQLite** — deferred to v2 when sync state tracking is needed
- **S3_* env vars** — project is S3-compatible-first, not AWS-first

## Files Created

```
cmd/skyfs/main.go
skyfs/skyfs.go, crypto.go, identity.go, keys.go, chunk.go, manifest.go
skyfs/*_test.go, edge_test.go
skyadapter/adapter.go
skyadapter/s3/s3.go, s3_test.go
internal/config/config.go, config_test.go
Makefile, LICENSE, README.md, CLAUDE.md
docs/work/skyfs-v1-plan.md
docs/learned/dependencies.md, streaming.md, key-wrapping.md, chunking-strategy.md
```
