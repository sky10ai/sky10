# skyfs v2 — Multi-Device Sync

Status: not started
Created: 2026-03-14

## Goal

Make skyfs work across multiple devices. V1 is single-user, single-device.
V2 adds concurrent writes, conflict detection, local sync state, and the
performance optimizations needed to make sync practical.

After v2, a user can run `skyfs` on their laptop and phone, both writing to
the same S3 bucket, with no data loss and no coordination server.

## What v1 Has

- Encrypted file storage with streaming (4MB memory ceiling)
- Ed25519 identity + three-layer key hierarchy
- Content-addressed chunks with dedup
- Encrypted manifest (single file, single writer)
- CLI: init, put, get, ls, rm, info

## What v2 Adds

1. **FastCDC** — content-defined chunking for dedup on edits
2. **Multi-party key wrapping** — wrap keys for other identities without needing their private key
3. **Ops log** — append-only operation log, no more single-writer manifest
4. **Pack files** — bundle small chunks, reduce S3 requests
5. **Local SQLite index** — sync state, fast queries, offline manifest cache
6. **Key rotation** — rotate namespace keys, revoke access
7. **Blob GC** — clean up orphaned chunks
8. **CLI improvements** — progress output, batch operations

## Out of Scope (v2)

- File watching / sync daemon (skyshare, v3)
- FUSE mount
- Mobile
- Relay push notifications (skylink concern)

---

## Milestone 1: FastCDC Chunking

Drop-in replacement for fixed-size chunking. The `Chunker` interface stays
the same — only the split-point logic changes.

### Why First

Independent of everything else. Immediate improvement to dedup. If a user
edits a paragraph in a 50MB PDF, only the chunks around the edit change
instead of re-uploading a full 4MB fixed-size chunk.

### Checklist

- [ ] Add `github.com/jotfs/fastcdc-go` dependency
- [ ] Update `skyfs/chunk.go`:
  - `NewChunker(r io.Reader)` uses FastCDC rolling hash internally
  - Min 256KB, avg 1MB, max 4MB chunk sizes
  - Same `Next() (Chunk, error)` interface
  - `Chunk` struct unchanged
- [ ] Test: small file still = 1 chunk
- [ ] Test: large file splits at content-defined boundaries
- [ ] Test: insert bytes in middle of large file → only nearby chunks differ
- [ ] Test: appending to a file → all prior chunks unchanged
- [ ] Benchmark: chunking throughput (should be > 500MB/s)

### Acceptance

All existing tests pass (chunker interface unchanged). New tests prove
content-defined dedup works on edits.

---

## Milestone 2: Multi-Party Key Wrapping

Fix the v1 limitation where `WrapKey` requires both public AND private keys.
Enable wrapping a data key for someone else's public key without knowing
their private key.

### Why Second

Needed before skydb (agents sharing encrypted state). Also needed for key
rotation (re-wrapping namespace keys for remaining users after a revocation).
Independent of the ops log work.

### Checklist

- [ ] Implement Edwards-to-Montgomery point conversion (Ed25519 pub → X25519 pub)
  - Use `filippo.io/edwards25519` for the birational map
  - Or implement the conversion directly (~20 lines of curve math)
- [ ] Update `WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error)`
  - No longer requires private key
  - Ephemeral ECDH with recipient's X25519 public key (derived from Ed25519 pub)
- [ ] `UnwrapKey` unchanged (already works with just private key)
- [ ] Update `WrapNamespaceKey` signature to match
- [ ] Test: wrap for identity A using only A's public key, unwrap with A's private key
- [ ] Test: wrap for identity B using only B's public key, A cannot unwrap
- [ ] All existing wrap/unwrap tests still pass

### Acceptance

`WrapKey` takes only a public key. All existing tests pass. New tests prove
cross-identity wrapping works.

---

## Milestone 3: Ops Log

The core of v2. Replace the single mutable manifest with an append-only
operation log. Multiple devices can write concurrently without conflicts
at the storage level.

### Design

```
s3://bucket/ops/
  {timestamp}-{device-id}-{seq}.enc     each op is a unique S3 key
```

The ops log is the source of truth. The manifest becomes a periodic snapshot
(materialized cache) for fast startup.

### Checklist

- [ ] `skyfs/op.go` — operation types
  ```go
  type Op struct {
      Type         string   `json:"op"`          // "put" or "delete"
      Path         string   `json:"path"`
      Chunks       []string `json:"chunks,omitempty"`
      Size         int64    `json:"size,omitempty"`
      Checksum     string   `json:"checksum,omitempty"`
      PrevChecksum string   `json:"prev_checksum,omitempty"`
      Namespace    string   `json:"namespace,omitempty"`
      Device       string   `json:"device"`
      Timestamp    int64    `json:"timestamp"`
      Seq          int      `json:"seq"`
  }
  ```
- [ ] `WriteOp(ctx, backend, op, encKey) error` — encrypt + upload to `ops/`
- [ ] `ReadOps(ctx, backend, since, encKey) ([]Op, error)` — list + decrypt ops after timestamp
- [ ] Device ID: generated on `skyfs init`, stored in config
- [ ] Op key format: `ops/{timestamp}-{device}-{seq}.enc`
  - Globally unique: no two devices write the same key
  - Naturally ordered by timestamp
- [ ] Update `Store.Put` to write an op instead of directly modifying manifest
- [ ] Update `Store.Get` / `Store.List` to build state from snapshot + ops
- [ ] `BuildState(snapshot, ops) *Manifest` — replay ops on top of snapshot
- [ ] **Conflict detection** via `prev_checksum`:
  - Two ops for same path with same `prev_checksum` = conflict
  - Default resolution: last-writer-wins (LWW)
  - Detect and log conflicts for user review
- [ ] Tests:
  - Two devices write different files → both appear in state
  - Two devices write same file → LWW resolves, conflict logged
  - Ops replayed in timestamp order
  - State from (snapshot + ops) matches state from (all ops)

### Acceptance

Multi-device writes work without data loss. Conflicts detected and resolved.
v1 single-writer path replaced entirely.

---

## Milestone 4: Snapshot Compaction

Prevent the ops log from growing forever. Periodically compact ops into a
manifest snapshot.

### Checklist

- [ ] `Compact(ctx, store) error`
  - Load latest snapshot
  - Replay all ops since snapshot
  - Write new snapshot: `manifests/snapshot-{timestamp}.enc`
  - Delete ops older than new snapshot
  - Keep last 2-3 snapshots for safety
- [ ] Compaction is idempotent — two devices compacting simultaneously produce
  the same result (deterministic replay order)
- [ ] Auto-compact: trigger after ~1,000 ops or configurable threshold
- [ ] `skyfs compact` CLI command for manual compaction
- [ ] Tests:
  - Compact produces correct snapshot
  - Old ops deleted, old snapshots retained
  - State identical before and after compaction
  - Concurrent compaction is safe

### Acceptance

Ops log stays bounded. Compaction is safe and idempotent.

---

## Milestone 5: Pack Files

Bundle small chunks into larger S3 objects. Reduces API calls and speeds up
initial sync.

### Design

```
s3://bucket/packs/
  pack_001.enc = [chunk_a | chunk_b | ... | chunk_n | index]

pack-index.enc = { chunk_hash → { pack_file, offset, length } }
```

Read a single chunk: S3 range request into the pack file.

### Checklist

- [ ] `skyfs/pack.go` — pack file operations
  - `PackWriter` — accumulates chunks, writes pack when full (~16MB target)
  - `PackReader` — reads a chunk from a pack via range request
- [ ] Add `GetRange(ctx, key, offset, length) (io.ReadCloser, error)` to
  `skyadapter.Backend` interface
- [ ] Implement range reads in S3 backend
- [ ] Pack index: maps `chunk_hash → { pack, offset, length }`
  - Encrypted, stored at `pack-index.enc`
  - Downloaded alongside manifest on sync
- [ ] Packing rules:
  - Chunks < 256KB → always pack
  - Chunks 256KB–4MB → pack until pack reaches ~16MB
  - Chunks > 4MB → individual blobs (as before)
- [ ] Update `Store.Put` to use pack writer for small chunks
- [ ] Update `Store.Get` to check pack index before falling back to individual blob
- [ ] Tests:
  - Small chunks packed into single S3 object
  - Range read retrieves correct chunk from pack
  - Large chunks still stored individually
  - Pack index round-trip
- [ ] Backward compatible: existing individual blobs still readable

### Acceptance

Small files upload faster (fewer S3 requests). Initial sync pulls fewer
objects. All existing tests pass.

---

## Milestone 6: Local SQLite Index

Cache manifest + sync state locally. Enables fast queries without hitting S3
on every operation.

### Checklist

- [ ] Add `modernc.org/sqlite` dependency
- [ ] `skyfs/index.go` — local index
  ```sql
  CREATE TABLE remote_files (
      path TEXT PRIMARY KEY, chunks TEXT, size INTEGER,
      modified TEXT, checksum TEXT, namespace TEXT
  );
  CREATE TABLE local_files (
      path TEXT PRIMARY KEY, size INTEGER, modified TEXT,
      checksum TEXT, synced BOOLEAN
  );
  CREATE TABLE chunks (
      hash TEXT PRIMARY KEY, location TEXT, size INTEGER, cached BOOLEAN
  );
  CREATE TABLE state (
      key TEXT PRIMARY KEY, value TEXT
  );  -- last_op_timestamp, device_id, etc.
  ```
- [ ] Index location: `~/.skyfs/index.db`
- [ ] `SyncIndex(ctx, store) error` — pull ops since last sync, update index
- [ ] Update `Store.List` to query local index instead of downloading manifest
- [ ] Update `Store.Get` to check local chunk cache before downloading
- [ ] `skyfs sync` CLI command — pull latest state into local index
- [ ] Tests:
  - Index reflects remote state after sync
  - List queries index, not S3
  - Chunk cache avoids re-downloading known chunks

### Acceptance

`skyfs ls` is instant (no S3 round-trip). `skyfs sync` pulls incremental
changes efficiently.

---

## Milestone 7: Key Rotation

Rotate namespace keys without re-encrypting file data. Revoke access to
a key without affecting other authorized keys.

### Checklist

- [ ] `skyfs rotate-ns <namespace>` — rotate a namespace key
  1. Generate new namespace key
  2. For each file in namespace: re-wrap file key with new namespace key
  3. Wrap new namespace key for all authorized user keys
  4. Delete old namespace key wrapping
  - Cost per file: re-wrap a 64-byte key (symmetric operation)
  - File data untouched
- [ ] `skyfs revoke <namespace> <identity>` — remove an identity's access
  - Remove their wrapped namespace key
  - Optionally rotate namespace key (if compromised vs just decommissioned)
- [ ] `skyfs grant <namespace> <identity>` — add access
  - Wrap namespace key for new identity's public key (uses milestone 2)
- [ ] Tests:
  - After rotation: existing authorized keys can still read
  - After revoke: revoked key cannot read new data
  - File data unchanged after rotation (only key metadata changes)
  - Grant + read round-trip with new identity

### Acceptance

Key rotation is fast (seconds for thousands of files). Revocation works.
File data never re-encrypted during normal rotation.

---

## Milestone 8: Blob Garbage Collection

Clean up orphaned chunks that are no longer referenced by any file in the
manifest.

### Checklist

- [ ] `skyfs gc` CLI command
  1. Load current manifest (snapshot + ops)
  2. Collect all referenced chunk hashes
  3. List all blobs in `blobs/` and chunks in packs
  4. Delete unreferenced blobs
  5. Optionally repack packs with many dead chunks
- [ ] Dry-run mode: `skyfs gc --dry-run` — list what would be deleted
- [ ] Safety: never GC while ops are in flight (check ops log for recent activity)
- [ ] Tests:
  - Put file, rm file, gc → blob deleted
  - Dedup: blob referenced by two files, rm one, gc → blob kept
  - Pack repack after many deletions

### Acceptance

`skyfs gc` reclaims storage. No referenced data ever deleted.

---

## Milestone 9: CLI Improvements

Polish the CLI for real-world usage.

### Checklist

- [ ] Progress output for put/get (bytes transferred, percentage, speed)
- [ ] `skyfs sync` — pull latest state
- [ ] `skyfs compact` — manual compaction
- [ ] `skyfs gc [--dry-run]` — garbage collection
- [ ] `skyfs rotate-ns <namespace>` — key rotation
- [ ] `skyfs grant/revoke <namespace> <identity>` — access control
- [ ] `skyfs status` — show sync state, pending ops, conflicts
- [ ] Batch put: `skyfs put dir/ --as remote-dir/` (recursive)

### Acceptance

CLI is usable for daily workflows. Progress visible on large transfers.

---

## S3 Layout (v2)

```
s3://bucket/
  ops/                                  append-only operation log
    1707900000-device-a-0001.enc
    1707900005-device-b-0001.enc

  manifests/                            periodic compacted snapshots
    snapshot-1707899000.enc
    snapshot-1707800000.enc

  blobs/                                large chunks (> 4MB)
    ab/cd/abcdef1234...enc

  packs/                                bundled small chunks
    pack_001.enc
    pack_002.enc

  pack-index.enc                        chunk → pack mapping

  keys/
    namespaces/
      journal.ns.enc
      financial.ns.enc
```

---

## Dependency Additions (v2)

```
new external:
  github.com/jotfs/fastcdc-go          content-defined chunking
  modernc.org/sqlite                    local sync state index
  filippo.io/edwards25519              Ed25519 → X25519 point conversion (if needed)

unchanged:
  github.com/aws/aws-sdk-go-v2         S3 client
  golang.org/x/crypto/hkdf             key derivation
  stdlib for everything else
```

---

## Order of Implementation

Milestones are ordered by dependency and value:

```
1. FastCDC          independent, immediate dedup improvement
2. Multi-party wrap independent, unblocks key rotation + skydb
3. Ops log          the big one, enables multi-device
4. Compaction       depends on 3, keeps ops log bounded
5. Pack files       independent of 3-4, performance optimization
6. SQLite index     depends on 3, enables fast local queries
7. Key rotation     depends on 2+3, security feature
8. Blob GC          depends on 3, storage reclamation
9. CLI improvements depends on all above
```

Milestones 1-2 can be done in parallel. Milestone 3 is the critical path.
Milestones 5-8 can be parallelized after 3-4 are done.
