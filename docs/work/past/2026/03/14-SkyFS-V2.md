---
created: 2026-03-14
model: claude-opus-4-6
---

# SkyFS V2 — Multi-Device Sync

## Problems Solved

### Milestone 1: FastCDC Chunking
- Replaced fixed-size 4MB chunking with FastCDC content-defined chunking (jotfs/fastcdc-go)
- Edits in the middle of a large file now only affect nearby chunks
- Appending preserves all prior chunks
- Caught and fixed: FastCDC library reuses internal buffer between calls — data must be copied

### Milestone 2: Multi-Party Key Wrapping
- `WrapKey` now takes only a public key (no private key required)
- Implemented Edwards-to-Montgomery point conversion using filippo.io/edwards25519
- `BytesMontgomery()` handles the birational map from Ed25519 to X25519
- Private key conversion uses SHA-512 of seed with X25519 clamping (matching Ed25519 internals)
- Enables wrapping keys for any identity — prerequisite for skydb and access control

### Milestone 3: Ops Log
- Replaced single-writer manifest with append-only operation log
- Each write produces a unique S3 key: `ops/{timestamp}-{device}-{seq}.enc`
- State built by replaying ops on top of latest snapshot
- Deterministic sort: `(timestamp, device, seq)` — same result regardless of read order
- Conflict detection via `prev_checksum` — two ops branching from same state detected
- Multi-device support: `NewWithDevice()` for explicit device IDs
- All ops encrypted with manifest-derived key

### Milestone 4: v1 Backward Compatibility
- `loadLatestSnapshot` checks for v2 snapshots first, falls back to v1 `manifests/current.enc`
- Existing v1 buckets work without migration

### Milestone 5: Snapshot Compaction
- `Compact()` writes current state as snapshot, deletes old ops
- Configurable snapshot retention (default: keep 3)
- Idempotent: safe if two devices compact simultaneously
- `skyfs compact` CLI command

### Milestone 6: Pack Files
- `PackWriter` accumulates encrypted chunks into ~16MB pack files
- `PackIndex` maps chunk_hash → {pack, offset, length}
- `ReadPackedChunk` uses `GetRange` for single-chunk reads
- Added `GetRange(ctx, key, offset, length)` to Backend interface
- Implemented range reads in both S3 and MemoryBackend
- Auto-flush when buffer exceeds target size

### Milestone 7: Local SQLite Index
- `OpenIndex()` creates/opens SQLite database at `~/.skyfs/index.db`
- Tables: `remote_files`, `chunks`, `sync_state`
- `SyncFromManifest()` replaces all entries atomically (transaction)
- `ListFiles()`, `LookupFile()`, `FileCount()` for fast local queries
- `SetState()`/`GetState()` for sync metadata
- Pure Go via modernc.org/sqlite (no CGo)

### Milestone 8: Key Rotation + Access Control
- `GrantAccess()`: wrap namespace key for another identity's public key
- `RevokeAccess()`: delete wrapped key + rotate namespace key
- `RotateNamespaceKey()`: re-encrypt all chunks with new derived file keys
- `ListAuthorizedKeys()`: find per-identity key wrappings
- Per-identity key files: `keys/namespaces/{ns}.{identity_short}.ns.enc`

### Milestone 9: Blob Garbage Collection
- `GC()` walks blobs, compares against manifest references, deletes orphans
- Dry-run mode: report without deleting
- Dedup-safe: shared blobs preserved if any file references them
- `skyfs gc [--dry-run]` CLI command

### Milestone 10: CLI Improvements
- Added `skyfs compact [--keep n]` command
- Added `skyfs gc [--dry-run]` command
- Removed v1 init manifest creation (v2 uses ops log from the start)

## Decisions Made

- **FastCDC buffer copy required** — the library reuses its internal buffer between `Next()` calls; must copy data out
- **SHA-512 + clamp for Ed25519→X25519 private** — matches Ed25519 internal scalar derivation, compatible with BytesMontgomery() public key conversion
- **Ops sorted by (timestamp, device, seq)** — deterministic total order even with clock skew
- **Chunks encrypted individually before packing** — packs are concatenation of already-encrypted data, no pack-level encryption needed
- **Key rotation re-encrypts chunk data** — because file keys are HKDF-derived from namespace key + content hash, changing the namespace key changes all file keys
- **Revoked users' wrapped keys deleted, re-granting required after rotation** — storing public keys for automatic re-wrapping deferred

## Files Created/Modified

```
New:
  skyfs/op.go, op_test.go           ops log + state builder + conflict detection
  skyfs/compact.go, compact_test.go  snapshot compaction
  skyfs/pack.go, pack_test.go        pack files + pack index
  skyfs/index.go, index_test.go      SQLite local index
  skyfs/access.go, access_test.go    key rotation + grant/revoke
  skyfs/gc.go, gc_test.go            blob garbage collection

Modified:
  skyfs/chunk.go                     FastCDC integration
  skyfs/crypto.go                    Edwards-to-Montgomery conversion, WrapKey signature change
  skyfs/keys.go                      WrapNamespaceKey signature change
  skyfs/skyfs.go                     ops log integration, device ID, state builder
  skyadapter/adapter.go              GetRange method
  skyadapter/s3/s3.go                GetRange implementation + MemoryBackend
  cmd/skyfs/main.go                  compact + gc commands

Dependencies added:
  github.com/jotfs/fastcdc-go        content-defined chunking
  filippo.io/edwards25519            Ed25519→X25519 point conversion
  modernc.org/sqlite                 local index
```

## Test Count

96 tests total (up from 55 in v1). All passing.
