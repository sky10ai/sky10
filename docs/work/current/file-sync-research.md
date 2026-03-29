---
created: 2026-03-29
model: claude-opus-4-6
---

# File Synchronization Architecture Research

Deep technical research on how Dropbox, Tresorit, Box, Syncthing, and others
implement file synchronization across devices. Focus on architecture patterns
relevant to sky10's ops-log CRDT approach.

---

## 1. Dropbox

### Server-Side Architecture

**Server File Journal (SFJ)** — the central metadata database. Append-only
rows, each representing a specific file version:

| Column | Purpose |
|--------|---------|
| Namespace ID (NSID) | Identifies the user/shared-folder namespace |
| Namespace Relative Path | File path within the namespace |
| Blocklist | Ordered list of SHA-256 block hashes |
| Journal ID (JID) | Monotonically increasing sequence within a namespace |

A "namespace" is an abstraction for a root directory tree. Each user has a
root namespace; each shared folder is its own namespace mountable into one or
many root namespaces. Every file is uniquely identified by (namespace, path).

Each client keeps a **cursor (JID) per namespace** representing how far it has
consumed the SFJ. The server returns only entries newer than the cursor.

**Two server types:**
- **Metadata servers**: manage users, namespaces, SFJ, coordinate commits
- **Block servers**: content-addressed key-value store (SHA-256 hash -> encrypted block data)

### Client-Side Architecture (Nucleus)

Nucleus (the rewritten sync engine, shipped ~2020, written in Rust) uses a
**three-tree model**:

1. **Remote Tree** — latest cloud state per the server
2. **Local Tree** — last observed state of the user's disk
3. **Synced Tree** — the last known fully-synced state (the "merge base")

The Synced Tree is the key innovation. Without it, you cannot distinguish
"file deleted locally" from "file added remotely" — both look like "file exists
on server but not on disk." With the merge base, the direction of every change
is unambiguous.

**The Planner** is the core algorithm. It takes three trees as input and outputs
batched operations that can safely execute concurrently:
- "Create this folder on disk"
- "Commit this edit to the server"
- "Download this file"
- "Delete this file locally"

It iterates until all three trees converge to the same state.

**Concurrency model:** Nearly all control logic runs on a single thread.
Only I/O, hashing, and network are offloaded to background threads. This
makes the system deterministic and testable.

**Node identity:** Files/folders have globally unique IDs (not path-based).
A move is a single attribute update on the node, not delete + create. This
prevents the old engine's bug where a move interrupted by a network failure
would produce a delete without the corresponding add.

### Upload Protocol

1. Client computes blocklist (SHA-256 of each 4MB chunk)
2. Client sends `commit(namespace, path, blocklist)` to metadata server
3. Metadata server checks which blocks exist; responds "need blocks" for
   missing ones
4. Client uploads missing blocks directly to block servers
5. Client retries commit; metadata server writes SFJ row

**Metadata is written AFTER blob upload succeeds.** The commit is the atomic
point of visibility — until it succeeds, no other client sees the file.

### Download / Change Detection

1. Client maintains longpoll connection (blocks until server has changes)
2. Server signals "new changes available"
3. Client calls `list` with per-namespace cursors
4. Server returns new SFJ rows since cursor
5. Client checks local block cache, downloads missing blocks from block servers
6. Client reconstructs file from blocks, updates Local Tree

**Streaming sync optimization:** Metadata server keeps temporary state in
memcache for in-progress uploads. Downloading clients can prefetch blocks
from partially-committed uploads, overlapping download with upload.

### Delete Handling

Deletes are SFJ entries with an empty blocklist. When a client sees a delete
in the journal:
1. Planner compares against Synced Tree to determine it's a remote delete
   (not a local create)
2. Deletes local file
3. Updates all three trees

### Conflict Resolution

- Concurrent edits: last writer wins by JID (server serializes commits)
- Concurrent folder moves: whoever commits first wins; other device's
  sync engine sees the resolved state and converges
- File edited locally + edited remotely: Dropbox creates a "conflicted copy"
  file with the losing version
- The three-tree model makes conflict detection precise: if both Local and
  Remote differ from Synced, that's a conflict

### Convergence Guarantee

The Planner is proven to converge through the CanopyCheck testing framework.
Given any three valid trees, the Planner produces a finite sequence of
operations that brings all three trees to the same state. This was verified
via property-based testing (QuickCheck-style) with millions of random inputs.

### Event Processing (Cape)

Cape is Dropbox's async event processing framework:
- SFJ changes produce events keyed by (NSID, JID)
- Events flow: SFJ -> Cape Frontend (RPC) -> Kafka -> Dispatcher -> Workers
- Processes billions of events/day at p95 < 100ms latency
- Powers search indexing, audit logs, API notifications, permission propagation
- At-least-once delivery; background refresh workers detect missed events

---

## 2. Tresorit

### Encryption Architecture

**Key hierarchy:**
- Each user has an RSA-4096 key pair (private key encrypted with password-derived key)
- Each shared folder ("tresor") has a symmetric AES-256 key
- The tresor key is encrypted with each member's RSA public key (stored as
  "group key file" in the tresor)
- Each file has its own independent, freshly generated AES-256 key
- File keys are encrypted with the tresor key
- Integrity protected with HMAC-SHA-512

The server never sees encryption keys or plaintext. Zero-knowledge architecture.

### Sync Architecture

**Full-file sync only.** This is the critical constraint imposed by E2E
encryption:
- Any modification requires the entire file to be re-encrypted and re-uploaded
- No delta/block-level sync is possible because the server cannot compare
  encrypted blocks across versions (different keys = different ciphertext)
- No server-side deduplication (unlike Dropbox's content-addressed blocks)

The server acts as a central store-and-forward relay. Clients:
1. Monitor local folders for changes
2. Encrypt entire file with fresh AES-256 key
3. Upload encrypted blob + encrypted file key
4. Server stores blob and notifies other clients
5. Other clients download encrypted blob, decrypt with tresor key chain

### Conflict Resolution

Simple conflict-copy approach:
- When a file is modified on two devices concurrently, a conflict file is
  created: `filename (user@email.com's conflict).ext`
- Both versions are preserved; no automatic merge
- Users must manually resolve

### Delete Handling

Not publicly documented in detail. Based on the architecture, deletes must be
communicated as metadata operations (the server tracks which files exist in a
tresor). The server can see that a file entry was removed from a tresor's
metadata even though it cannot see the file contents.

### Versioning

Server maintains version history. Users can access previous versions and roll
back. Each version is a separate encrypted blob.

### What Tresorit Cannot Do (due to E2E encryption)

- Block-level deduplication (ciphertext is random-looking)
- Delta sync / partial uploads (can't compare encrypted blocks)
- Server-side merge or conflict resolution
- Content-based routing or search on server
- Convergent encryption would enable dedup but leaks information (confirmation
  attacks)

### Relevance to sky10

Sky10 faces the same constraints. However, sky10 uses content-defined chunking
with per-chunk encryption, which means:
- Unchanged chunks produce the same ciphertext (same key + same plaintext)
- Changed chunks get new ciphertext
- This enables block-level dedup and delta sync even with E2E encryption
- The tradeoff: chunk boundaries are deterministic, which leaks chunk sizes
  (but not content)

Tresorit's approach is simpler but less efficient. Sky10's chunked approach
is closer to what Dropbox does, but with client-side encryption.

---

## 3. Box

### Architecture Overview

Box Drive uses a **virtual filesystem** mounted in the OS (kernel-space
component delegates to user-space application). Files are not fully synced
to disk — they're fetched on demand from Box's cloud.

### Sync Engine: Shadow Architecture

The engine uses **Logical Item Shadows (LIS)** — in-memory representations
of file trees containing metadata for all files and folders.

**Two monitors with two shadows:**
- **Box Monitor** + **Box LIS**: represents the engine's understanding of
  what exists on Box.com
- **Local Monitor** + **Local LIS**: represents the engine's understanding
  of the local filesystem

**Reconciliation flow:**
- **Local-to-Box (purple flow)**: local change detected -> update Local LIS ->
  propagate to Box.com -> update Box LIS
- **Box-to-local (green flow)**: Box notification received -> update Box LIS ->
  apply to local filesystem -> update Local LIS

The engine determines if something is a real change by checking against the
LIS: if the LIS already reflects the state, it's an "echoback" (the engine
itself caused the change) and is dropped.

### VLLIS Optimization

Box Drive 2.1 replaced the Local LIS with **VLLIS (Virtual Local LIS)** —
since Box Drive IS the filesystem (virtual FS), it has perfect knowledge of
local state without maintaining a separate shadow. The VLLIS queries the
filesystem on-demand instead of caching in memory.

This eliminated startup time and memory overhead for the local shadow
(significant for users with ~200K files).

### Event System

Box server uses an event stream with `stream_position` cursor:
- `ITEM_UPLOAD`, `ITEM_CREATE`, `ITEM_TRASH`, `ITEM_MOVE`, etc.
- Client polls with last `stream_position`, receives only new events
- Long polling available: OPTIONS /events returns a real-time server URL;
  GET blocks until change occurs
- Events retained 2 weeks to 2 months
- Events may arrive duplicated or out of order; deduplicate by `event_id`
- Three stream types: `all`, `changes`, `sync`

### Conflict Handling

Not extensively documented. The shadow-comparison approach detects conflicts
when both LIS trees show divergent states for the same item. Box creates
conflict copies similar to Dropbox.

### Server Role

Box.com is the authoritative server. All state flows through Box's cloud.
Not peer-to-peer. The server maintains the canonical file tree and pushes
events to clients.

---

## 4. Syncthing (open-source, peer-to-peer)

### Architecture

Fully peer-to-peer using the **Block Exchange Protocol (BEP)** over TLS 1.3.
No central server. Written in Go.

Each device maintains a **local model** (metadata + block hashes for all files
in each shared folder). The union of all local models, selecting files with the
highest version, forms the **global model**. Each device converges toward the
global model.

### Block System

- Files split into blocks: 128 KiB to 16 MiB (powers of two)
- Each block identified by SHA-256 hash
- Block lists exchanged via Index/Index Update messages
- Only blocks with mismatched hashes are transferred

### Version Vectors

Each file has a **version vector** — an array of (device_id, counter) tuples:
- Device ID = first 64 bits of SHA-256 of device certificate
- Counter = simple incrementing integer starting at 0
- When a device modifies a file, it increments its own counter in the vector
- Comparison: if all counters in A >= corresponding counters in B, then A
  dominates B. If neither dominates, they are concurrent (conflict).

The file with the highest version vector wins in the global model.

### FileInfo Structure

```
name:          UTF-8 NFC path (folder-relative)
type:          file / directory / symlink
size:          bytes
permissions:   Unix bits
modified_s/ns: Unix timestamp with nanoseconds
modified_by:   last modifier device short ID
deleted:       boolean flag (tombstone)
invalid:       unavailable for sync
version:       version vector
sequence:      monotonic local clock value
block_size:    bytes per block
blocks:        array of (offset, size, hash)
symlink_target: target path (if symlink)
```

### Protocol Messages

1. **Hello**: device name, client name/version
2. **ClusterConfig**: folder + device configuration, known index states
3. **Index / Index Update**: full or incremental file metadata exchange
4. **Request**: block data request (folder, name, offset, size, hash)
5. **Response**: block data delivery
6. **DownloadProgress**: partial file availability
7. **Ping**: keepalive (every 90 seconds)
8. **Close**: connection termination

### Delete Handling

Deletes are explicit tombstones in the file metadata: `deleted: true`, empty
block list, modification time set to time of deletion. Tombstones propagate
through Index Update messages like any other file state.

### Conflict Resolution

- Concurrent modifications: file with older mtime gets renamed to
  `filename.sync-conflict-YYYYMMDD-HHMMSS-ModifiedBy.ext`
- Equal mtime: larger device ID value wins
- Conflict files propagate as normal files
- No automatic merge

### Convergence

Each device independently computes the global model from all received indexes
and pulls missing/outdated blocks from peers. Convergence is guaranteed as
long as devices are eventually connected and version vectors are exchanged.

### Change Detection

- Filesystem watchers (10s accumulation window, deletes delayed 1 extra minute)
- Periodic full scans (default: hourly) as a fallback
- Files rehashed (SHA-256 blocks) on detected change

### Moves

BEP does not have a dedicated move message. Moves are represented as
delete (old path) + create (new path). However, block deduplication means
the "new" file's blocks already exist on the receiving device, so no data
transfer occurs — only metadata.

### NAT Traversal

Relay servers forward encrypted traffic when direct connections fail.
Community-contributed relay pool. Relay servers see encrypted traffic only
(TLS 1.3 end-to-end between devices).

---

## 5. rsync

### Algorithm

Not a sync protocol but a delta-transfer algorithm:

1. Receiver splits its copy into fixed-size chunks
2. Computes two checksums per chunk: rolling checksum (Adler-32 variant)
   and strong hash (MD5 or newer)
3. Sends checksums to sender
4. Sender slides a rolling window over its version, computing rolling checksum
   at every byte position
5. On rolling checksum match, verifies with strong hash
6. Sends only the non-matching regions (deltas)

**Key property:** The rolling checksum can be updated in O(1) per byte as the
window slides, making the scan efficient.

**Limitations for sync:**
- One-directional (sender -> receiver)
- No conflict detection
- No state tracking between runs
- No delete propagation
- Must re-scan everything each time

---

## 6. Academic: CRDTs for File Systems

### Kleppmann et al. — "A Highly-Available Move Operation for Replicated Trees" (2021)

The problem: concurrent tree moves can create cycles or lose nodes. Dropbox
and Google Drive have shipped bugs caused by this.

The solution: a CRDT algorithm for tree structures that:
- Handles arbitrary concurrent creates, deletes, moves, renames
- Guarantees no cycles are introduced
- Guarantees all replicas converge to the same state
- Formally verified with Isabelle/HOL proof assistant
- Uses an undo-redo mechanism: when applying a move that would create a
  cycle, undo that move and let the other concurrent move win

### ElmerFS — "CRDTs for Truly Concurrent File Systems" (2021)

A geo-replicated file system using CRDTs for all file system structures:
- Directory structure represented as CRDTs
- Conflict resolution designed to be intuitive to users
- Addresses rename/move conflicts, concurrent creates at same path
- Research prototype, not production system

### CrossFS (2024-2025)

Cross-domain distributed file system using CRDTs for metadata sync:
- Hybrid Tree indexing structure for distributed environments
- CRDT-based metadata synchronization for strong eventual consistency
- 33% reduction in query latency, 31% reduction in write amplification
- Adaptive caching + hybrid sync model balancing consistency vs availability

### Shapiro et al. — CRDT Foundations (2011)

Two CRDT approaches, both providing Strong Eventual Consistency (SEC):

**State-based (CvRDT):** Replicas merge states using a join-semilattice.
Requires states to form a partially ordered set with a least upper bound.

**Operation-based (CmRDT):** Replicas exchange operations. Requires all
concurrent operations to commute. Assumes causal delivery.

For file sync, the relevant CRDT type is the **LWW-Register-Map** (also called
LWW-Element-Dictionary): each key (file path) maps to a value (file metadata)
with a timestamp. Concurrent writes resolved by highest timestamp.

---

## 7. Comparative Analysis

### Approaches to State

| System | State Model | Central Authority |
|--------|-------------|-------------------|
| Dropbox | Three trees (remote, local, synced) | Server (SFJ) |
| Box | Two shadows (Box LIS, Local LIS) | Server (Box.com) |
| Tresorit | Full-file cloud mirror | Server (metadata only) |
| Syncthing | Local model + global model | None (peer-to-peer) |
| sky10 | Ops log + CRDT snapshot | S3 (ops log is source of truth) |

### Change Detection / Notification

| System | Mechanism |
|--------|-----------|
| Dropbox | Longpoll + SFJ cursor (JID per namespace) |
| Box | Event stream + stream_position + long polling |
| Tresorit | Not documented; likely polling |
| Syncthing | Filesystem watchers + periodic full scan |
| sky10 | kqueue watcher + S3 polling (30s) |

### Ops Log / Event Log / Changelog

| System | Approach |
|--------|----------|
| Dropbox | SFJ is an append-only journal; JIDs are monotonic per namespace |
| Box | Event stream with stream_position; events expire after 2 weeks |
| Tresorit | Not documented |
| Syncthing | No log; version vectors on each file track causal history |
| sky10 | Append-only ops log (ops.jsonl locally, ops/ prefix on S3) |

**Dropbox's SFJ is the closest analog to sky10's ops log.** Both are
append-only, both use monotonic cursors for incremental consumption, both
allow replay from scratch to reconstruct current state.

### Conflict Resolution

| System | Strategy |
|--------|----------|
| Dropbox | Server serializes commits (last commit wins); conflict copies for concurrent edits |
| Box | Server authoritative; conflict copies |
| Tresorit | Conflict copies (no auto-merge possible due to E2E encryption) |
| Syncthing | Version vectors; older mtime becomes conflict copy |
| sky10 | LWW by (timestamp, device, seq) tuple |

### Delete Handling

| System | Mechanism |
|--------|-----------|
| Dropbox | Empty-blocklist SFJ entry; three-tree diff determines direction |
| Box | ITEM_TRASH event through event stream |
| Tresorit | Server metadata update (details not public) |
| Syncthing | Tombstone in FileInfo (deleted=true); propagates like any file state |
| sky10 | OpDelete in ops log; CRDT snapshot removes path |

### Metadata vs Blob Ordering

| System | Order |
|--------|-------|
| Dropbox | Blob first, then metadata commit (commit is point of visibility) |
| Box | Upload, then server notification |
| Tresorit | Full encrypted file upload, then metadata |
| Syncthing | P2P block exchange; metadata (Index) sent first, blocks requested by receiver |
| sky10 | Blob (chunks) first, then op to S3 (same as Dropbox) |

### Convergence Mechanism

| System | How |
|--------|-----|
| Dropbox | Planner iterates three-tree diff until convergence; formally tested |
| Box | Shadow reconciliation loop |
| Tresorit | Server is single source of truth; clients mirror it |
| Syncthing | Global model from version vector max; devices pull missing blocks |
| sky10 | CRDT snapshot from ops log; reconciler diffs snapshot vs disk |

---

## 8. Key Lessons for sky10

### What sky10 does well (validated by research)

1. **Ops log as source of truth** — matches Dropbox's SFJ approach. Both are
   append-only, cursor-based, replayable.

2. **LWW-Register-Map CRDT** — academically sound. Provides strong eventual
   consistency. Same approach used by Syncthing (version vectors) and Cassandra.

3. **Blob before metadata** — matches Dropbox's pattern. Chunks uploaded first,
   then op written. This ensures no client ever sees metadata pointing to
   nonexistent data.

4. **Reconciler as stateless diff** — similar to Dropbox's Planner (diffs three
   trees) and Syncthing (diffs local model vs global model). Stateless
   reconciliation is robust against crashes.

5. **Chunked encryption with deterministic chunk boundaries** — better than
   Tresorit's full-file approach. Enables delta sync with E2E encryption.

### What sky10 could learn

1. **Three-tree model (Dropbox)** — sky10's reconciler diffs snapshot vs disk.
   It lacks a "synced tree" / merge base. This means it cannot distinguish:
   - "File in snapshot but not on disk" = could be remote add OR local delete
   - sky10 handles this with device attribution (checking which device last
     modified the file), but a merge base would be more general

2. **Node IDs instead of paths (Dropbox)** — sky10 uses paths as the primary
   key. Renames/moves are delete + create. Dropbox's node-ID approach makes
   moves atomic and prevents the "interrupted move = data loss" bug. Worth
   considering if sky10 needs robust rename/move support.

3. **Longpoll / push notifications (Dropbox, Box)** — sky10 polls S3 every 30s.
   Dropbox and Box use longpoll for near-instant notification. S3 doesn't
   support longpoll natively, but a lightweight notification service (SNS,
   WebSocket relay, or MQTT) could reduce sync latency from 30s to sub-second.

4. **Version vectors (Syncthing)** — sky10's LWW uses a single timestamp +
   device + seq tuple. Version vectors provide strictly more information: they
   track causal relationships and can detect true concurrency (neither version
   dominates). LWW can silently drop concurrent updates; version vectors can
   detect them and create conflict copies. However, version vectors are more
   complex and sky10's tuple approach is adequate for most cases.

5. **Automated compaction (Dropbox)** — Dropbox's SFJ is maintained by the
   server. sky10's ops log on S3 grows forever unless manually compacted.
   Automated compaction (e.g., when ops count exceeds threshold) would prevent
   cold-start performance degradation.

6. **Event processing pipeline (Dropbox Cape)** — for future features like
   search indexing, audit logs, or API notifications, an event-driven pipeline
   reading from the ops log would be valuable.

---

## Sources

### Dropbox
- [Rewriting the heart of our sync engine](https://dropbox.tech/infrastructure/rewriting-the-heart-of-our-sync-engine)
- [Testing sync at Dropbox](https://dropbox.tech/infrastructure/-testing-our-new-sync-engine)
- [Streaming File Synchronization](https://dropbox.tech/infrastructure/streaming-file-synchronization)
- [Broccoli: Syncing faster by syncing less](https://dropbox.tech/infrastructure/-broccoli--syncing-faster-by-syncing-less)
- [Introducing Cape](https://dropbox.tech/infrastructure/introducing-cape)
- [Efficiently enumerating Dropbox with /delta](https://dropbox.tech/developers/efficiently-enumerating-dropbox-with-delta)
- [Detecting Changes Guide](https://developers.dropbox.com/detecting-changes-guide)
- [How the Datastore API Handles Conflicts - Part 1](https://dropbox.tech/developers/how-the-datastore-api-handles-conflicts-part-1-basics-of-offline-conflict-handling)
- [How the Datastore API Handles Conflicts - Part 2](https://dropbox.tech/developers/how-the-dropbox-datastore-api-handles-conflicts-part-two-resolving-collisions)
- [Fighting clock skew when syncing password payloads](https://dropbox.tech/application/dropbox-passwords-clock-skew-payload-sync-merge)
- [Content Hash reference](https://www.dropbox.com/developers/reference/content-hash)
- [Dropbox Nucleus/Sync rewrite discussion (InfoQ)](https://www.infoq.com/news/2020/04/dropbox-testing-sync-engine/)

### Box
- [Box Drive: Engineering faster starts and less memory](https://medium.com/box-tech-blog/box-drive-engineering-faster-starts-and-less-memory-3916df34ce27)
- [Box Drive: Undoing a bad engineering decision](https://medium.com/box-tech-blog/box-drive-undoing-a-bad-engineering-decision-7b9c3e8c10de)
- [Box Events API - Long Polling](https://developer.box.com/guides/events/user-events/polling)
- [Box User Events: REST API & Python](https://medium.com/box-developer-blog/box-user-events-rest-api-python-3eb3d4d8d55)

### Tresorit
- [The basics of synchronization](https://support.tresorit.com/hc/en-us/articles/360005460033-The-basics-of-synchronization)
- [What is a conflict file?](https://support.tresorit.com/hc/en-us/articles/216114327-What-is-a-conflict-file)
- [Tresorit Encryption Whitepaper (PDF)](https://cdn.tresorit.com/202208011608/tresorit-encryption-whitepaper.pdf)
- [Tresorit Security](https://tresorit.com/security)

### Syncthing
- [Block Exchange Protocol v1 specification](https://docs.syncthing.net/specs/bep-v1.html)
- [Understanding Synchronization](https://docs.syncthing.net/users/syncing.html)
- [Syncthing conflict resolution (community forum)](https://forum.syncthing.net/t/how-does-conflict-resolution-work/15113)

### Academic Papers
- [Kleppmann et al. - A highly-available move operation for replicated trees (2021)](https://martin.kleppmann.com/papers/move-op.pdf)
- [CRDTs for truly concurrent file systems (HotStorage 2021)](https://dl.acm.org/doi/10.1145/3465332.3470872)
- [CrossFS: CRDT-Based Metadata Synchronization (ACM TOS 2024)](https://dl.acm.org/doi/10.1145/3777470)
- [Shapiro et al. - Convergent and Commutative Replicated Data Types (2011)](https://inria.hal.science/inria-00555588/document)
- [General-Purpose Secure CRDTs (2023)](https://eprint.iacr.org/2023/584.pdf)
- [Convergent Encryption (Wikipedia)](https://en.wikipedia.org/wiki/Convergent_encryption)

### Other
- [File synchronisation algorithms (Ian Howson)](https://ianhowson.com/blog/file-synchronisation-algorithms/)
- [rsync algorithm (Wikipedia)](https://en.wikipedia.org/wiki/Rsync)
