# Dependency Decisions

Decided: 2026-03-14

## Principle

stdlib first. Only add external deps when the stdlib genuinely can't do it.

## Stdlib (covers most of skyfs)

| Need | Package | Notes |
|------|---------|-------|
| AES-256-GCM | `crypto/aes` + `crypto/cipher` | Data encryption, nonce prepended to ciphertext |
| Ed25519 | `crypto/ed25519` | Identity, signing |
| X25519 ECDH | `crypto/ecdh` | Key exchange for wrapping (stdlib since Go 1.20) |
| SHA-256 | `crypto/sha256` | Content addressing |
| Random bytes | `crypto/rand` | Key generation, nonces |
| JSON | `encoding/json` | Manifest, config, ops |
| CLI | `flag` | Subcommand dispatch, no framework needed |

## golang.org/x/crypto (quasi-stdlib)

| Need | Package | Why not stdlib |
|------|---------|----------------|
| HKDF | `golang.org/x/crypto/hkdf` | Key derivation in the hierarchy. Not in stdlib. |

## External

| Need | Package | Why this one | Why not alternatives |
|------|---------|-------------|---------------------|
| S3 client | `aws-sdk-go-v2` (`service/s3`, `config`) | Standard S3 client. Modular — import only what's needed. Every S3-compatible provider (B2, R2, MinIO) documents setup with this SDK. | minio-go/v7 has gotten heavy (~15 transitive deps including kerberos, json-iterator). Custom thin client tempting but Sig V4 signing has edge cases. |
| CDC chunking | `github.com/jotfs/fastcdc-go` | FastCDC algorithm, small focused lib. | Deferred — v1 uses fixed-size 4MB chunking. FastCDC adds dedup on edits within large files; swap in for v2. |
| SQLite | `modernc.org/sqlite` | Pure Go, no CGo. Easier cross-compilation. | Deferred to v2 — not needed until sync state tracking. mattn/go-sqlite3 requires CGo. |

## What we explicitly chose NOT to use

| Tool | Why not |
|------|---------|
| cobra | Three CLI subcommands don't justify a framework. `flag` + manual dispatch. |
| fsnotify | kqueue is used directly for file watching. |
| errgroup | Useful later for concurrent chunk uploads. `sync` is fine for v1. |
| testify | CLAUDE.md says use stdlib `testing`. Table-driven tests. |
| gin/echo/chi | No HTTP server in skyfs. |

## Rules

- Don't pick a library based on features another package needs.
  Example: don't pick aws-sdk-go-v2 for presigned URLs — that's skylink's
  concern, not skyfs's. Pick it because it's the right S3 client.

- The skyadapter interface isolates the S3 client. If we hate aws-sdk-go-v2,
  it's a one-file swap behind the interface.

- sqlite is deferred, not rejected. When skyfs needs local sync state (v2),
  add it then.
