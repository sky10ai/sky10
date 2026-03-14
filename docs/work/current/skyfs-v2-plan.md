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
- Content-addressed chunks with dedup (fixed-size)
- Encrypted manifest (single file, single writer)
- CLI: init, put, get, ls, rm, info
- Deterministic builds

## What v2 Adds

1. **FastCDC** — content-defined chunking for dedup on edits within large files
2. **Multi-party key wrapping** — wrap keys for any public key without the private key
3. **Ops log** — append-only operation log replacing single-writer manifest
4. **v1 → v2 migration** — upgrade existing buckets to ops log format
5. **Snapshot compaction** — keep ops log bounded
6. **Pack files** — bundle small chunks, reduce S3 round-trips
7. **Local SQLite index** — sync state, fast local queries, offline capable
8. **Key rotation + access control** — rotate, grant, revoke namespace keys
9. **Blob GC** — reclaim storage from orphaned chunks
10. **CLI improvements** — progress, batch ops, status, error recovery

## Out of Scope (v2)

- File watching / sync daemon (v3 / skyshare)
- FUSE mount (v3)
- Compression before encryption (v3 — evaluate zstd per-chunk)
- Relay push notifications (skylink concern)
- Mobile
- Versioning / point-in-time restore (v3)

---

## Milestone 1: FastCDC Chunking

Drop-in replacement for fixed-size chunking. The `Chunker` interface stays
the same — only the split-point logic changes.

### Why First

Independent of everything else. Immediate improvement to dedup. If a user
edits a paragraph in a 50MB PDF, only the chunks around the edit change
instead of re-uploading a full 4MB fixed-size chunk.

### Tasks

- [ ] Add `github.com/jotfs/fastcdc-go` dependency
- [ ] Update `skyfs/chunk.go` internals:
  - [ ] `NewChunker(r io.Reader)` uses FastCDC rolling hash
  - [ ] Configure: min 256KB, avg 1MB, max 4MB
  - [ ] Same `Next() (Chunk, error)` interface — no API change
  - [ ] `Chunk` struct unchanged
- [ ] Remove `ChunkThreshold` constant (CDC handles all sizes naturally)
- [ ] Tests:
  - [ ] Small file (< 256KB) → single chunk
  - [ ] Large file splits at content-defined boundaries
  - [ ] Insert bytes in middle of large file → only nearby chunks change
  - [ ] Append to a file → all prior chunks unchanged
  - [ ] All existing chunk tests still pass
- [ ] Benchmark: `go test -bench=. ./skyfs/` chunking throughput (target > 500MB/s)
- [ ] Update `docs/learned/chunking-strategy.md`

### Acceptance

All existing tests pass unchanged. New tests prove content-defined dedup on
edits. Benchmark confirms throughput is acceptable.

---

## Milestone 2: Multi-Party Key Wrapping

Fix the v1 limitation where `WrapKey` requires both public AND private keys.
Enable wrapping a data key for someone else's public key without needing
their private key.

### Why Second

Needed before skydb (agents sharing encrypted state). Needed for key
rotation (re-wrapping for remaining users after revocation). Needed for
`skyfs grant`. Independent of the ops log work.

### Tasks

- [ ] Evaluate conversion approach:
  - [ ] Option A: `filippo.io/edwards25519` for the birational map (~1 new dep)
  - [ ] Option B: Implement Ed25519→X25519 conversion directly using `crypto/ed25519`
    internal representation (~30 lines, no new dep, but relies on field encoding)
  - [ ] Decision: pick one, document in `docs/learned/`
- [ ] Implement `edPubToX25519(ed25519.PublicKey) (*ecdh.PublicKey, error)`
  - [ ] Edwards y-coordinate → Montgomery u-coordinate conversion
  - [ ] Handle edge cases: low-order points, identity point
- [ ] Update `WrapKey` signature:
  - [ ] Before: `WrapKey(dataKey []byte, recipientPub ed25519.PublicKey, recipientPriv ed25519.PrivateKey)`
  - [ ] After: `WrapKey(dataKey []byte, recipientPub ed25519.PublicKey)`
  - [ ] Private key no longer needed
- [ ] Update `WrapNamespaceKey` signature to match
- [ ] Update all callers (Store.getOrCreateNamespaceKey)
- [ ] Keep `UnwrapKey` signature unchanged (still needs private key, already works)
- [ ] Tests:
  - [ ] Wrap for identity A using only A's public key → unwrap with A's private key
  - [ ] Wrap for identity B using only B's public key → A cannot unwrap
  - [ ] Wrap same key for two different identities → each can unwrap independently
  - [ ] All existing wrap/unwrap tests still pass (update calls to remove priv arg)
- [ ] Update `docs/learned/key-wrapping.md` with final approach

### Acceptance

`WrapKey` takes only a public key. Cross-identity wrapping works. All
existing tests pass after signature update.

---

## Milestone 3: Ops Log

The core of v2. Replace the single mutable manifest with an append-only
operation log. Multiple devices write ops with unique keys — no conflicts
at the storage level.

### Design

```
s3://bucket/ops/
  {timestamp}-{device-id}-{seq}.enc     each op is a unique S3 key

s3://bucket/manifests/
  snapshot-{timestamp}.enc              periodic compacted snapshots
```

The ops log is the source of truth. Snapshots are materialized caches for
fast startup.

### Tasks

- [ ] `skyfs/device.go` — device identity
  - [ ] `GenerateDeviceID() string` — short random ID (e.g. 8 hex chars)
  - [ ] Store device ID in `~/.skyfs/config.json`
  - [ ] Generated during `skyfs init`, persists across sessions
- [ ] `skyfs/op.go` — operation types
  - [ ] Define `Op` struct:
    ```go
    type Op struct {
        Type         OpType   `json:"op"`
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
  - [ ] `OpType` enum: `OpPut`, `OpDelete`
  - [ ] `OpKey(op) string` — generates `ops/{timestamp}-{device}-{seq}.enc`
- [ ] `skyfs/oplog.go` — ops log read/write
  - [ ] `WriteOp(ctx, backend, op, key) error` — encrypt op, upload to `ops/`
  - [ ] `ReadOps(ctx, backend, sinceTimestamp, key) ([]Op, error)` — list ops after timestamp, decrypt each
  - [ ] `ReadAllOps(ctx, backend, key) ([]Op, error)` — all ops (for migration/rebuild)
  - [ ] Ops encrypted with manifest key (same derivation as v1 manifest)
- [ ] `skyfs/state.go` — state builder
  - [ ] `BuildState(snapshot *Manifest, ops []Op) *Manifest` — replay ops in timestamp order
  - [ ] Handle: put overwrites previous entry, delete removes it
  - [ ] `LoadCurrentState(ctx, backend, identity) (*Manifest, error)` — load latest snapshot + replay ops
- [ ] **Clock skew handling**:
  - [ ] Ops sorted by `(timestamp, device, seq)` — deterministic total order
  - [ ] If clock is wildly off, ops still merge correctly (just out of causal order)
  - [ ] Log warning if clock skew > 5 minutes detected between ops from different devices
- [ ] **Conflict detection**:
  - [ ] Two ops for same path with same `prev_checksum` = conflict
  - [ ] Default: last-writer-wins (LWW) by timestamp
  - [ ] `Conflict` struct: `{ Path, DeviceA, DeviceB, OpA, OpB }`
  - [ ] `DetectConflicts(ops []Op) []Conflict`
  - [ ] Conflicts logged to stderr, not fatal
- [ ] Update `Store.Put`:
  - [ ] Load current state to get `prev_checksum` for the path
  - [ ] Write op to `ops/` instead of modifying manifest directly
- [ ] Update `Store.Get` / `Store.List` / `Store.Remove`:
  - [ ] Use `LoadCurrentState` instead of `LoadManifest`
- [ ] **Partial upload recovery**:
  - [ ] If chunk upload succeeds but op write fails → orphaned chunks (harmless, GC cleans up)
  - [ ] If op write succeeds but was for only some chunks → op is never valid (chunks field incomplete)
  - [ ] Solution: upload all chunks first, then write op atomically
- [ ] Tests:
  - [ ] Single device: put/get/list/rm work as before via ops
  - [ ] Two devices write different files → both appear in state
  - [ ] Two devices write same file → LWW resolves, conflict detected
  - [ ] Ops replayed in deterministic order regardless of insertion order
  - [ ] State from (snapshot + ops) == state from (all ops replayed)
  - [ ] Op encryption: raw S3 content is not plaintext
  - [ ] Empty ops log + no snapshot → empty state (fresh bucket)

### Acceptance

Multi-device writes work without data loss. Conflicts detected and logged.
All v1 tests pass (now running through ops log path). Single-device
performance comparable to v1.

---

## Milestone 4: v1 → v2 Migration

Upgrade existing v1 buckets to the ops log format.

### Tasks

- [ ] `skyfs migrate` CLI command:
  - [ ] Read v1 manifest (`manifests/current.enc`)
  - [ ] Write it as first snapshot (`manifests/snapshot-{timestamp}.enc`)
  - [ ] Delete old `manifests/current.enc`
  - [ ] Set device ID in config if missing
- [ ] Auto-detect on startup:
  - [ ] If `manifests/current.enc` exists and no snapshots → prompt to migrate
  - [ ] Or auto-migrate with a log message
- [ ] Backward compat: if both v1 manifest and snapshots exist, prefer snapshots
- [ ] Tests:
  - [ ] Create v1 bucket (manifest + blobs) → migrate → read via ops path
  - [ ] Files intact after migration
  - [ ] Idempotent: running migrate twice is safe

### Acceptance

`skyfs migrate` upgrades a v1 bucket. All existing files accessible after
migration. No data loss.

---

## Milestone 5: Snapshot Compaction

Prevent the ops log from growing forever. Compact ops into a manifest
snapshot periodically.

### Tasks

- [ ] `skyfs/compact.go`:
  - [ ] `Compact(ctx, store) error`:
    1. Load latest snapshot
    2. Replay all ops since snapshot
    3. Write new snapshot: `manifests/snapshot-{timestamp}.enc`
    4. Delete ops older than new snapshot
    5. Keep last 2-3 snapshots for safety
- [ ] Compaction determinism:
  - [ ] Two devices compacting simultaneously must produce the same snapshot
  - [ ] Ops replayed in exact same order → same manifest → same encrypted output
    (note: encryption is non-deterministic due to random nonces, but logical content is identical)
  - [ ] Safe even if a new op lands between compact read and compact write
    (the new op will be newer than the snapshot timestamp, so it's preserved)
- [ ] Auto-compact triggers:
  - [ ] After `Store.Put` if ops count > configurable threshold (default 1000)
  - [ ] Threshold stored in config: `"compact_threshold": 1000`
- [ ] `skyfs compact` CLI command for manual compaction
- [ ] Tests:
  - [ ] 100 ops → compact → snapshot has correct state, ops deleted
  - [ ] Old snapshots retained (last 2-3)
  - [ ] State identical before and after compaction
  - [ ] Op written during compaction is preserved
  - [ ] Compact on empty ops log is a no-op

### Acceptance

Ops log stays bounded. Compaction is safe. No data lost during compaction.

---

## Milestone 6: Pack Files

Bundle small chunks into larger S3 objects. Reduces API calls and speeds up
initial sync.

### Design

```
s3://bucket/packs/
  pack_001.enc = [chunk_a_enc | chunk_b_enc | ... | chunk_n_enc | index]

pack-index.enc = { chunk_hash → { pack_file, offset, length } }
```

Each chunk is individually encrypted BEFORE packing. The pack is a
concatenation of already-encrypted chunks. This means:
- No additional pack-level encryption needed
- Range reads return already-encrypted data, decrypted by the file key
- Pack format is just concatenation + an index trailer

### Tasks

- [ ] Extend `skyadapter.Backend` interface:
  - [ ] Add `GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)`
  - [ ] Implement in S3 backend (S3 `Range` header)
  - [ ] Implement in MemoryBackend (slice the byte array)
- [ ] `skyfs/pack.go`:
  - [ ] `PackWriter` struct:
    - [ ] `Add(chunkHash string, encryptedData []byte)` — buffer chunk
    - [ ] `Flush(ctx, backend) error` — write pack when buffer ≥ 16MB target
    - [ ] Track: `{ chunkHash → offset, length }` for each packed chunk
  - [ ] `PackIndex` type: `map[string]PackLocation`
    ```go
    type PackLocation struct {
        Pack   string `json:"pack"`
        Offset int64  `json:"offset"`
        Length int64  `json:"length"`
    }
    ```
  - [ ] `LoadPackIndex(ctx, backend, identity) (*PackIndex, error)`
  - [ ] `SavePackIndex(ctx, backend, index, identity) error`
  - [ ] Pack index encrypted with manifest key
- [ ] Packing rules:
  - [ ] Chunks < 256KB → always pack
  - [ ] Chunks 256KB–4MB → pack until pack reaches ~16MB
  - [ ] Chunks > 4MB → individual blobs in `blobs/` (as v1)
- [ ] Update `Store.Put`:
  - [ ] Small encrypted chunks go through PackWriter
  - [ ] PackWriter flushes to S3 when full
  - [ ] Flush any remaining on `Store.Close()` or explicit flush
- [ ] Update `Store.Get`:
  - [ ] Check pack index for chunk location
  - [ ] If packed: `GetRange` into pack file
  - [ ] If not packed: fetch from `blobs/` as before
- [ ] Backward compatibility:
  - [ ] Existing `blobs/` chunks still readable (no pack index entry → fetch directly)
  - [ ] No migration needed — new chunks get packed, old ones stay as blobs
- [ ] Tests:
  - [ ] Small chunks (< 256KB) packed into single S3 object
  - [ ] Range read retrieves correct chunk data from pack
  - [ ] Large chunks still stored as individual blobs
  - [ ] Pack index save/load round-trip
  - [ ] Mixed: some chunks packed, some individual → all retrievable
  - [ ] v1 blobs still readable without pack index entry

### Acceptance

Small files upload faster (fewer S3 requests). Initial sync pulls fewer
objects. v1 data still accessible. All existing tests pass.

---

## Milestone 7: Local SQLite Index

Cache manifest + sync state locally. Fast queries without S3 round-trips.
Offline capable.

### Tasks

- [ ] Add `modernc.org/sqlite` dependency
- [ ] `skyfs/index.go` — local index:
  - [ ] Schema:
    ```sql
    CREATE TABLE remote_files (
        path TEXT PRIMARY KEY,
        chunks TEXT,         -- JSON array of chunk hashes
        size INTEGER,
        modified TEXT,
        checksum TEXT,
        namespace TEXT
    );
    CREATE TABLE local_files (
        path TEXT PRIMARY KEY,
        size INTEGER,
        modified TEXT,
        checksum TEXT,
        synced BOOLEAN
    );
    CREATE TABLE chunks (
        hash TEXT PRIMARY KEY,
        location TEXT,       -- "blob" or "pack:pack_001:offset:length"
        size INTEGER,
        cached BOOLEAN
    );
    CREATE TABLE sync_state (
        key TEXT PRIMARY KEY,
        value TEXT
    );
    ```
  - [ ] `sync_state` tracks: `last_op_timestamp`, `last_snapshot`, `device_id`
- [ ] `OpenIndex(path string) (*Index, error)` — open or create SQLite DB
- [ ] Index location: `~/.skyfs/index.db`
- [ ] `(*Index) Sync(ctx, store) error`:
  - [ ] Read ops since `last_op_timestamp`
  - [ ] Replay into `remote_files` table
  - [ ] Update chunk locations
  - [ ] Update `last_op_timestamp`
- [ ] `(*Index) ListFiles(prefix string) ([]FileEntry, error)` — query local DB
- [ ] `(*Index) LookupChunk(hash string) (ChunkLocation, error)` — check cache/location
- [ ] Update `Store` to use index when available:
  - [ ] `Store.List` → query index if synced, fall back to S3
  - [ ] `Store.Get` → check local chunk cache before downloading
  - [ ] `Store.Put` → update index after successful upload
- [ ] `skyfs sync` CLI command:
  - [ ] Pull latest state into local index
  - [ ] Print: files changed, new, deleted since last sync
- [ ] Tests:
  - [ ] Index reflects remote state after sync
  - [ ] `List` queries index, not S3
  - [ ] Chunk cache hit avoids S3 download
  - [ ] Index survives restart (persisted in SQLite)
  - [ ] Corrupt/missing index → rebuild from remote state

### Acceptance

`skyfs ls` is instant after sync. `skyfs sync` pulls incremental changes.
Chunk cache reduces bandwidth.

---

## Milestone 8: Key Rotation + Access Control

Rotate namespace keys without re-encrypting file data. Grant and revoke
access for other identities.

### Tasks

- [ ] `skyfs/access.go` — access control operations:
  - [ ] `RotateNamespaceKey(ctx, store, namespace) error`:
    1. Generate new namespace key
    2. Load all file entries in namespace from manifest
    3. For each file: decrypt file key with old ns key, re-wrap with new ns key
    4. Wrap new namespace key for all authorized user keys
    5. Upload new wrapped file keys + new namespace key wrapping
    6. Delete old namespace key wrapping
    - Cost: re-wrap N tiny keys (symmetric ops). File data untouched.
  - [ ] `GrantAccess(ctx, store, namespace, recipientPub) error`:
    1. Load namespace key (unwrap with our private key)
    2. Wrap namespace key for recipient's public key (milestone 2)
    3. Upload wrapped key to `keys/namespaces/{ns}.{recipient_id}.ns.enc`
  - [ ] `RevokeAccess(ctx, store, namespace, recipientPub) error`:
    1. Delete recipient's wrapped namespace key
    2. Rotate namespace key (since revoked party may have cached it)
    3. Re-wrap for all remaining authorized keys
- [ ] Namespace key storage update for multi-party:
  - [ ] v1: `keys/namespaces/{ns}.ns.enc` (single user)
  - [ ] v2: `keys/namespaces/{ns}.{identity_short_id}.ns.enc` (per authorized identity)
  - [ ] `ListAuthorizedKeys(ctx, backend, namespace) ([]string, error)`
- [ ] CLI commands:
  - [ ] `skyfs rotate-ns <namespace>`
  - [ ] `skyfs grant <namespace> <sky://k1_...>`
  - [ ] `skyfs revoke <namespace> <sky://k1_...>`
  - [ ] `skyfs access <namespace>` — list who has access
- [ ] Tests:
  - [ ] Rotate: authorized keys can still read after rotation
  - [ ] Revoke: revoked key cannot unwrap new namespace key
  - [ ] Revoke: revoked key CAN still read data encrypted with old key
    (can't un-ring the bell — same as Slack kicking someone from a channel)
  - [ ] Grant: new identity can read after grant
  - [ ] Grant + put + get round-trip with second identity
  - [ ] Rotate speed: 1000 files in namespace rotated in < 5 seconds
  - [ ] File data unchanged after rotation (compare blob checksums)

### Acceptance

Key rotation is fast (seconds for thousands of files). Grant/revoke works.
File data never re-encrypted during normal rotation.

---

## Milestone 9: Blob Garbage Collection

Clean up orphaned chunks no longer referenced by any file.

### Tasks

- [ ] `skyfs/gc.go`:
  - [ ] `GC(ctx, store, dryRun bool) (*GCResult, error)`:
    1. Load current state (snapshot + all ops)
    2. Collect all referenced chunk hashes into a set
    3. List all keys in `blobs/` prefix
    4. List all chunks in pack index
    5. Delete unreferenced individual blobs
    6. Mark dead chunks in pack index
  - [ ] `GCResult`: `{ BlobsDeleted, BytesReclaimed, PackDeadChunks }`
- [ ] Safety checks:
  - [ ] Check ops log for recent activity (< 5 min) → warn, require `--force`
  - [ ] Never delete a chunk that appears in ANY op (including uncompacted ops)
  - [ ] Two-phase: mark-then-sweep with configurable grace period
- [ ] Pack repacking:
  - [ ] If a pack has > 50% dead chunks → repack (copy live chunks to new pack, delete old)
  - [ ] `skyfs gc --repack` flag
- [ ] CLI:
  - [ ] `skyfs gc` — run GC
  - [ ] `skyfs gc --dry-run` — show what would be deleted
  - [ ] `skyfs gc --repack` — also repack sparse packs
  - [ ] Print: blobs deleted, bytes reclaimed, packs repacked
- [ ] Tests:
  - [ ] Put file → rm file → gc → blob deleted
  - [ ] Dedup: blob referenced by two files → rm one → gc → blob kept
  - [ ] Packed chunk: dead chunk marked, pack repacked when threshold hit
  - [ ] Dry run: nothing deleted, counts reported
  - [ ] Safety: recent op in flight → GC warns

### Acceptance

`skyfs gc` reclaims storage. No referenced data ever deleted. Dry-run is safe.

---

## Milestone 10: CLI Improvements + Hardening

Polish for real-world usage. Error recovery. Progress output.

### Tasks

- [ ] Progress output:
  - [ ] `skyfs put` — bytes uploaded / total, chunks done / total, speed (MB/s)
  - [ ] `skyfs get` — bytes downloaded / total, speed
  - [ ] Use stderr for progress, stdout for final result
  - [ ] `--quiet` flag to suppress progress
- [ ] Batch operations:
  - [ ] `skyfs put dir/ --as remote-dir/` — recursive put
  - [ ] Walk directory tree, put each file, single manifest update at end
  - [ ] Skip unchanged files (compare local checksum with manifest)
- [ ] Status command:
  - [ ] `skyfs status` — show sync state, pending ops count, last sync time, conflicts
- [ ] Error recovery:
  - [ ] Interrupted upload: chunks may be orphaned, op never written → safe (GC cleans up)
  - [ ] Interrupted download: partial local file → delete and retry
  - [ ] Corrupt local index: `skyfs sync --rebuild` to recreate from remote
- [ ] Config improvements:
  - [ ] `S3_BUCKET`, `S3_REGION`, `S3_ENDPOINT` env vars as alternatives to config file
  - [ ] `--config` flag to specify config path
- [ ] Tests:
  - [ ] Batch put: directory with 100 files → all stored, manifest correct
  - [ ] Batch put: skip unchanged files (no re-upload)
  - [ ] Index rebuild from remote matches normal sync
- [ ] `go vet ./...` clean, `gofmt` clean, all tests pass
- [ ] Update README with v2 commands

### Acceptance

CLI is usable for daily workflows. Progress visible on large transfers.
Errors are recoverable. Batch operations work.

---

## S3 Layout (v2)

```
s3://bucket/
  ops/                                  append-only operation log
    {timestamp}-{device}-{seq}.enc        ~200 bytes each, unique keys

  manifests/                            periodic compacted snapshots
    snapshot-{timestamp}.enc              full file tree at point in time
                                          keep last 2-3

  blobs/                                large chunks stored individually
    ab/cd/abcdef1234...enc

  packs/                                bundled small chunks
    pack_001.enc
    pack_002.enc

  pack-index.enc                        chunk → pack+offset+length mapping

  keys/
    namespaces/
      {ns}.{identity_short}.ns.enc      namespace key per authorized identity
```

---

## Dependency Additions (v2)

```
new:
  github.com/jotfs/fastcdc-go          content-defined chunking
  modernc.org/sqlite                    local sync state index
  filippo.io/edwards25519              Ed25519 → X25519 point conversion (evaluate)

unchanged:
  github.com/aws/aws-sdk-go-v2         S3 client
  golang.org/x/crypto/hkdf             key derivation
  stdlib for everything else
```

---

## Order of Implementation

```
1. FastCDC             independent, immediate value
2. Multi-party wrap    independent, unblocks 8
├── can parallelize 1 and 2
3. Ops log             critical path, enables multi-device
4. v1→v2 migration     depends on 3, enables existing users to upgrade
5. Compaction          depends on 3, keeps ops bounded
├── 6-9 can parallelize after 3-5 are solid
6. Pack files          independent perf optimization
7. SQLite index        depends on 3, enables fast local queries
8. Key rotation        depends on 2+3, security
9. Blob GC             depends on 3+6, storage reclamation
10. CLI improvements   depends on all, polish
```

---

## V3 Thoughts

Things that don't belong in v2 but should be on the radar:

- **Sync daemon** — `skyfs watch ~/Documents` with fsnotify. Background process
  that auto-syncs on file changes. This is essentially skyshare without the GUI.
  Needs: ops log (v2), local index (v2), change detection (fsnotify).

- **FUSE mount** — `skyfs mount ~/Sky` exposes encrypted storage as a local
  filesystem. Read = decrypt on demand. Write = encrypt + upload. Needs careful
  caching (can't hit S3 on every `read()` syscall). Go FUSE libraries: `bazil.org/fuse`
  or `hanwen/go-fuse`.

- **Compression before encryption** — zstd per-chunk before AES-GCM. Saves storage
  and bandwidth. Must compress BEFORE encrypting (encrypted data is incompressible).
  ~50-70% reduction on text/markdown. Negligible on already-compressed formats (JPEG, MP4).
  Add content-type detection to skip compression for incompressible formats.

- **Versioning / snapshots** — point-in-time restore. The ops log already contains
  full history. `skyfs snapshot list` shows compacted snapshots. `skyfs restore --at <timestamp>`
  rebuilds state at any point. Mostly UI on top of what v2 already stores.

- **Selective sync** — only sync certain namespaces or prefixes to a device.
  Mobile doesn't need the full dataset. `skyfs sync --namespace journal` or
  `skyfs sync --prefix docs/`. Needs local index (v2) to track what's synced vs skipped.

- **Bandwidth throttling** — `--max-upload 5MB/s` for metered connections.
  Token bucket rate limiter wrapping the io.Reader on uploads.

- **Multi-region replication** — replicate encrypted blobs to multiple S3 buckets
  in different regions. Disaster recovery. The ops log makes this natural: each
  region replays the same ops independently.

- **Web UI** — browser-based file manager for encrypted storage. Decrypt in the
  browser via WebCrypto API. No server sees plaintext. Could be a static site
  that talks directly to S3 with presigned URLs. Separate repo (skyshare-web).

- **Agent hooks** — skylink integration. Notify agents when files change.
  Agent reads via skyfs library, processes, writes results back. The ops log
  provides a natural event stream for agents to subscribe to.

- **Streaming encryption** — for files > 4MB chunks, consider STREAM construction
  (like libsodium's secretstream) instead of AES-GCM per chunk. Allows
  encrypting/decrypting without buffering a full chunk. Lower memory ceiling.
  Evaluate whether the complexity is worth it vs the current 4MB ceiling.
