---
created: 2026-03-19
model: claude-opus-4-6
---

# V3: Local Ops Log + CRDT Reconciliation

## Goal

Replace the manually-maintained `state.json` and incremental inbox with a
local ops log that serves as the single source of truth. The CRDT snapshot
replaces all derived state. The inbox becomes a computed diff. Known issues
#1 (broken replay from scratch) and #3 (ops log growth) are fixed as
side effects.

## Architecture

```
V2.5 (current):
  LOCAL:  kqueue → WatcherHandler → state.json + outbox.jsonl → S3
  REMOTE: Poller → S3 → inbox.jsonl → InboxWorker → filesystem + state.json
  STATE:  state.json (manually maintained)

V3 (proposed):
  LOCAL:  kqueue → WatcherHandler → ops.jsonl + outbox.jsonl → S3
  REMOTE: Poller → S3 → ops.jsonl → Reconciler → filesystem
  STATE:  ops.jsonl → CRDT snapshot (in memory, cached)
```

## What changes

| Component | V2.5 | V3 |
|-----------|------|----|
| State | `state.json` (DriveState) | CRDT snapshot from `ops.jsonl` |
| Inbox | `inbox.jsonl` (SyncLog) | Computed diff (snapshot vs filesystem) |
| Outbox | `outbox.jsonl` (SyncLog) | Same — upload work queue |
| WatcherHandler | Updates DriveState + outbox | Appends to local log + outbox |
| Poller | Writes inbox entries | Appends remote ops to local log |
| InboxWorker | Drains inbox.jsonl | Becomes Reconciler (diff-based) |
| OutboxWorker | Same | Same |
| Seed | Scan vs state → synthetic events | Scan vs snapshot → diff |
| Echo prevention | Race-prone (state updated after write) | Natural (op in log before file on disk) |

## Files on disk

```
~/.sky10/fs/drives/{id}/
  ops.jsonl         # local ops log — all ops, both local and remote
  outbox.jsonl      # upload work queue (blobs + S3 op sync)
```

`state.json` and `inbox.jsonl` are removed.

## Key design decisions

### Local log format
Plain JSONL of `opslog.Entry`. No encryption — that's for S3. Entries carry
the full clock tuple (timestamp, device, seq) for CRDT resolution.

### Snapshot is the state
`GetFile(path)` → snapshot lookup. `LastRemoteOp` → max timestamp of remote
entries in the log. No separate state struct.

### Inbox is a diff
`diff(snapshot, filesystem)` produces the work list. No persistent inbox
file. After a crash, re-derive by re-diffing. Idempotent.

### Echo prevention
Remote ops are in the local log (and thus the snapshot) BEFORE the file is
written to disk. When the watcher fires, checksum matches snapshot → skip.
No pause needed.

### Reconciler replaces InboxWorker
Instead of processing entries one-by-one, the Reconciler:
1. Builds CRDT snapshot from local log
2. Scans filesystem (or reads cached scan)
3. Computes diff: files to download, files to delete
4. Executes diff: download blobs, atomic write, delete

This naturally "compacts" intermediate states — if a file was created then
deleted remotely, the diff shows nothing. No wasted downloads.

### Outbox stays separate
The outbox carries upload context (local file path) that the ops log
doesn't need. The op is in the local log immediately; the outbox tracks
which ops still need their blobs pushed to S3.

## Milestones

### M1: LocalOpsLog type
Create `LocalOpsLog` — JSONL-backed ops log with CRDT snapshot.
- Append(entry)
- Snapshot() → built via `buildSnapshot` (CRDT, already exists)
- LastRemoteOp() → derived from max remote timestamp
- Lookup(path) → snapshot delegation
- Persist to `ops.jsonl`, cache snapshot in memory
- Tests: append, snapshot, crash recovery (re-read from file), CRDT properties

No daemon changes. Just the new type + tests.

### M2: Wire LocalOpsLog into WatcherHandler
- WatcherHandler writes ops to LocalOpsLog instead of DriveState
- Checksum comparison uses `localLog.Lookup(path)` instead of `state.GetFile()`
- Still writes to outbox for upload tracking
- OutboxWorker reads prev checksum from LocalOpsLog snapshot

Tests: watcher handler with local log, verify ops written, verify outbox
entries created.

### M3: Wire LocalOpsLog into Poller
- Poller appends remote ops to LocalOpsLog instead of inbox.jsonl
- Uses `localLog.LastRemoteOp()` as cursor
- Skips own device ops (same as today)
- No more inbox writes — just log append

Tests: poller with local log, verify remote ops appended, verify cursor
advances.

### M4: Reconciler (replaces InboxWorker)
- New component: `Reconciler`
- Builds diff: `snapshot.Files()` vs filesystem scan
- For each file in snapshot but not on disk (or wrong checksum): download
- For each file on disk but not in snapshot: delete (remote delete)
- Uses `store.GetChunks()` for downloads (same as InboxWorker today)
- Atomic writes to prevent watcher echo
- Triggered after poller appends new remote ops

Tests: reconciler with mock filesystem, verify correct downloads/deletes,
verify intermediate states are compacted (create+delete = no work).

### M5: Seed via diff
- On startup: scan filesystem + build snapshot from local log
- Diff tells you:
  - Files on disk but not in snapshot → new local files → append to log + outbox
  - Files in snapshot but not on disk → need download → reconciler handles it
  - Files with different checksums → modified → append to log + outbox
- Replaces current `seedStateFromDisk()` which generates synthetic events

Tests: seed with pre-existing local log, seed with empty log, seed with
local modifications.

### M6: Remove DriveState + inbox
- Delete `drivestate.go`, `inbox_worker.go`
- Remove `state.json` and `inbox.jsonl` references
- Remove `SyncLog[InboxEntry]` usage
- Update DaemonV2_5 (or new DaemonV3) to wire new components
- Migration: on startup, if `state.json` exists but `ops.jsonl` doesn't,
  bootstrap `ops.jsonl` from S3 ops log, then delete `state.json`

Tests: full daemon integration tests, migration from V2.5 state.

### M7: Local compaction
- Compact the local `ops.jsonl`: snapshot the log, rewrite file with only
  the snapshot entries (one synthetic put per file)
- Keeps the file from growing unbounded
- Can be triggered periodically or when file exceeds a size threshold
- Different from S3 compaction — this is local-only, no S3 writes

Tests: compact local log, verify snapshot unchanged, verify file shrinks.

## Issues this fixes

- **#1 Broken replay from scratch**: Reconciler builds full snapshot first,
  diffs against filesystem. No incremental processing, no ghost files.
- **#3 Ops log growth**: Local compaction (M7) keeps `ops.jsonl` bounded.
  S3 compaction remains separate and can be automated.
- **Echo/bounce prevention**: Ops in log before files on disk. Watcher
  checksum matches snapshot → skip. No races.
- **Offline reads**: Snapshot available from local log without S3.
- **Wasted downloads**: Reconciler diffs final state, not intermediate ops.
  Create+delete = no download.

## Out of scope (future)

- P2P sync (device-to-device without S3)
- Offline writes (local ops synced to S3 when back online) — partially
  enabled by this architecture but needs outbox persistence across
  extended offline periods
- Rolling 30-day trash compact (S3 blob garbage collection)
- Conflict file creation (`.conflict` copies)
