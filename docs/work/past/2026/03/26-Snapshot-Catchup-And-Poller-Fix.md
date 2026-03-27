---
created: 2026-03-26
model: claude-opus-4-6
---

# Snapshot Catch-Up & Poller Dedup Fix

## Problem

Two convergence failures prevented files from syncing between machines:

1. **CRDT convergence gap**: The poller reads `ops/` but S3 compaction folds
   ops into `manifests/` snapshots and deletes the originals. If the poller
   hasn't read ops before compaction, those ops are permanently invisible —
   the poller never reads `manifests/`.

2. **Poller dedup blind spot**: The poller skips remote ops when the local
   snapshot has an entry with the same checksum, regardless of chunk presence.
   A chunkless local entry (upload pending) blocks a chunked remote entry
   from ever being imported. The file stays chunkless forever — the integrity
   sweep re-queues it every cycle but the poller never imports the fix.

These two bugs together caused 101 files on Machine B to loop in the integrity
sweep indefinitely, and prevented this machine from ever downloading them.

## Why It's Architectural, Not a Bug

The poller was designed to read `ops/` incrementally. Compaction was designed
to fold `ops/` into snapshots. Neither was designed to coordinate with the
other. The implicit contract — "ops stay in `ops/` until all pollers have
read them" — was never enforced because there's no cross-device cursor
tracking.

The system works fine when:
- The poller cursor stays ahead of compaction (normal case)
- No machine is offline or stuck during compaction

It breaks when:
- A poller is stuck (cursor bug, same-second ops)
- A machine is offline during compaction
- A brand-new machine joins after compaction has run

## Investigation

Analyzed a debug dump from Machine B (MBP-M4.local / qdumgpq0y0xqyt0w):
- 101 files in the outbox, all chunkless, re-queued every 5 minutes
- Outbox worker writeback code (v0.17.6) was present and passed tests
- But the integrity sweep kept finding them chunkless on the next cycle

## Solution: Snapshot Catch-Up

On daemon startup, before the poller begins, merge the S3 snapshot into the
local log using LWW clock comparison.

### Algorithm

```
s3_snap    = OpsLog.LoadLatestSnapshot(ctx)     // S3 manifests/snapshot-*.enc
local_snap = LocalOpsLog.Snapshot()             // from ops.jsonl

for each (path, s3_info) in s3_snap.files:
    local_info, exists = local_snap.Lookup(path)
    s3_clock  = (s3_info.Modified.Unix(), s3_info.Device, s3_info.Seq)

    if !exists:
        inject(path, s3_info)                   // new file
    else:
        local_clock = (local_info.Modified.Unix(), local_info.Device, local_info.Seq)
        if s3_clock.beats(local_clock):
            inject(path, s3_info)               // S3 is newer
        // else: local wins or tie — keep local

advance cursor to max timestamp in s3_snap
```

Where `inject` converts the `FileInfo` back to an `Entry` and calls
`LocalOpsLog.Append()`.

### Why This Works

1. **Preserves CRDT semantics.** Injected entries carry the *original*
   `(Timestamp, Device, Seq)` from the S3 snapshot. Future LWW comparisons
   remain correct.

2. **Idempotent.** Running it on every startup is safe. If the local log
   already has the entry, LWW shows "local wins or tie" and nothing is
   injected.

3. **Handles all failure modes:**
   - *Stuck cursor*: S3 snapshot has entries the poller missed → injected
   - *Offline machine*: Compaction happened while away → injected on startup
   - *New machine*: Local log is empty → everything injected
   - *Cursor ahead of snapshot*: Nothing injected, poller handles the rest

4. **No re-upload risk.** Injected entries use `Append()` not `AppendLocal()`.
   They carry another device's `Device` field. The outbox only processes
   locally-generated ops.

### Startup Sequence

```go
// In DaemonV2_5.Run():
d.seedStateFromDisk()
d.catchUpFromSnapshot(ctx)    // NEW
go d.outboxWorker.Run(ctx)
go d.reconciler.Run(ctx)
go d.poller.Run(ctx)
```

### Deletes Are NOT in the S3 Snapshot

The `manifestJSON.Tree` only has live files. If Machine B deletes a file,
the delete gets compacted into "the file is absent from the snapshot."

**Decision:** catch-up only ADDS entries from S3, never removes locally-present
entries absent from S3. Absence is ambiguous (could be delete or stripped
chunkless Put).

### Risks

1. **Performance on large trees.** One encrypted download + decrypt + JSON
   parse on every startup. Negligible for hundreds of files.

2. **Network dependency on startup.** If S3 is unreachable, catch-up fails.
   Non-fatal — log a warning and proceed.

3. **Watcher re-creating chunkless entries.** After catch-up injects chunked
   entries and the reconciler downloads files, the watcher sees new files
   and creates chunkless Puts with fresh timestamps. Harmless: the file is
   on disk, integrity sweep will re-queue, upload confirms chunks.

## Changes

### Poller dedup fix (`pkg/fs/poller_v2.go`)

Added chunk-awareness to the dedup logic: when checksums match but local entry
is chunkless and remote has chunks, don't skip.

Updated `TestPollerV2SkipsAlreadyHave` to include chunks in the pre-populated
entry so it properly tests the "already have complete version" case.

### Snapshot catch-up (`pkg/fs/opslog/local.go`)

New method `CatchUpFromSnapshot(s3Snap, snapshotTS)`:
- Iterates S3 snapshot entries
- Compares against local snapshot using LWW clock comparison (`clockTuple.beats()`)
- Injects winning entries via `Append()` (not `AppendLocal()` — no outbox trigger)
- Advances cursor via `SetLastRemoteOp(snapshotTS)`
- Returns count of injected entries

Exported `LoadLatestSnapshot` on `OpsLog` (`pkg/fs/opslog/opslog.go`).

### Daemon wiring (`pkg/fs/daemon_v2_5.go`)

- Added `store` field to `DaemonV2_5` struct
- New `catchUpFromSnapshot(ctx)` method called in `Run()` and `SyncOnce()`
  between `seedStateFromDisk()` and starting workers
- Non-fatal on failure (logs warning, proceeds with stale local log)
- 30-second timeout on S3 snapshot load
- Pokes reconciler if entries were injected

### S3 delete RPC (`pkg/fs/rpc.go`)

New `skyfs.s3Delete` RPC for deleting individual S3 objects via the daemon
socket. Used by Cirrus S3 browser to clean up old debug dumps.

### Cirrus S3 browser delete

Right-click context menu on S3 browser file rows with destructive Delete
button. Calls `skyfs.s3Delete` and removes the row on success.

Files: `SkyClient.swift`, `SkyClientProtocol.swift`, `MockSkyClient.swift`,
`Previews.swift`, `S3BrowserView.swift`

## Tests

- `TestPollerV2ChunklessNotDeduped` — regression: chunkless local + chunked
  remote with same checksum → must append, not skip
- `TestCatchUpFromSnapshot` — local has chunkless A and complete B; S3 has
  chunked A (higher clock), same B, new C → injects A+C
- `TestCatchUpFromSnapshotIdempotent` — catch-up twice with same snapshot
  injects 1 then 0
- `TestSweepDoesNotLoopAfterOutboxDrain` — full production cycle: chunkless
  → sweep → outbox drain → no re-queue

## Key Code Locations

| Component | File | Relevance |
|-----------|------|-----------|
| clockTuple / beats() | opslog.go | LWW comparison logic |
| buildSnapshot | opslog.go | CRDT materialization |
| LoadLatestSnapshot | opslog.go | Returns (snapshot, baseTS) from manifests/ |
| CatchUpFromSnapshot | local.go | Merge S3 snapshot into local log |
| Poller dedup | poller_v2.go | Chunk-aware checksum comparison |
| Daemon startup | daemon_v2_5.go | seedState → catchUp → poller |
