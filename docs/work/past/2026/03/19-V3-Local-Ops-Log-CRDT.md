---
created: 2026-03-19
model: claude-opus-4-6
---

# V3: Local Ops Log + CRDT Reconciliation

Major architecture overhaul: `ops.jsonl` replaces `state.json` + `inbox.jsonl`
as the single source of truth. CRDT snapshot replaces all derived state.
Released as v0.10.0.

## Problem

V2.5 had three separate state stores that could diverge:
- `state.json` (DriveState) — manually maintained file map
- `inbox.jsonl` — pending remote changes
- `outbox.jsonl` — pending uploads

Known issues: broken replay from scratch (#1), unbounded ops log growth (#3),
race-prone echo prevention, wasted downloads for intermediate states.

## Architecture change

```
V2.5:
  LOCAL:  kqueue → WatcherHandler → state.json + outbox.jsonl → S3
  REMOTE: Poller → S3 → inbox.jsonl → InboxWorker → filesystem + state.json

V3:
  LOCAL:  kqueue → WatcherHandler → ops.jsonl + outbox.jsonl → S3
  REMOTE: Poller → S3 → ops.jsonl → Reconciler → filesystem
  STATE:  ops.jsonl → CRDT snapshot (in memory, cached)
```

## What was built

### M1: LocalOpsLog type (`06b8f32`)
- JSONL-backed ops log with LWW-Register-Map CRDT snapshot
- `Append`, `AppendLocal` (auto device/ts/seq), `Snapshot`, `Lookup`
- Incremental cache updates on warm cache, full rebuild from file on cold
- Crash recovery: re-read from file, recover seq counter + lastRemote cursor
- 14 tests including CRDT permutation test (all 24 orderings)

### M2: Wire into WatcherHandler + OutboxWorker (`2b2fddf`)
- WatcherHandler reads/writes LocalOpsLog instead of DriveState
- Echo prevention via `localLog.Lookup()` checksum comparison
- OutboxWorker reads prev checksum from LocalOpsLog
- Daemon seed reads from `localLog.Snapshot()` instead of `state.Files`
- 8 watcher handler tests, 4 outbox tests updated

### M3: Wire into Poller (`e094259`)
- Poller appends remote ops to LocalOpsLog
- Uses `localLog.LastRemoteOp()` as S3 cursor
- Remote ops appended AFTER inbox check so Lookup sees pre-op state
- Inbox writes kept temporarily for InboxWorker (removed in M4)
- 6 poller tests

### M4: Reconciler replaces InboxWorker (`16ec7aa`)
- Diffs `snapshot.Files()` vs `ScanDirectory()`:
  - Files in snapshot but not on disk → download via `store.GetChunks`
  - Files on disk but not in snapshot → delete (remote delete)
- Naturally compacts intermediate states: create+delete = no work
- Atomic writes to temp dir + rename (same safety as InboxWorker)
- Poller simplified: no inbox, just appends + pokes reconciler
- DriveState fully removed from daemon
- 7 reconciler tests, 6 poller tests

### M5: Seed via diff (`a3e76ab`)
- Direct diff against snapshot — no synthetic WatcherHandler events
- Files on disk not in snapshot → new → log + outbox
- Files with different checksum → modified → log + outbox
- Files in snapshot not on disk:
  - Our device → local delete (user deleted while offline)
  - Other device → pending download (reconciler will fetch)
- **Fixed data loss bug**: previously remote files pending download were
  treated as local deletes on restart, causing deletion from S3
- 4 seed-specific tests

### M6: Remove DriveState + inbox (`b24b7b6`)
- Deleted: `drivestate.go`, `drivestate_test.go`, `inbox_worker.go`,
  `inbox_worker_test.go`
- Removed `InboxEntry`, `NewInboxPut`, `NewInboxDelete` from synclog.go
- Updated rpc.go: `rpcList`/`rpcInfo` read from LocalOpsLog snapshot
- Migration: `migrateStateToOpsLog()` converts state.json → ops.jsonl
- Net -829 lines deleted

### M7: Local compaction (`2707385`)
- `Compact()` rewrites ops.jsonl with one put per file in snapshot
- Drops superseded puts, deletes for re-created files
- Atomic write (temp file + rename) — crash-safe
- 4 compaction tests

## Files created
- `pkg/fs/opslog/local.go` — LocalOpsLog implementation
- `pkg/fs/opslog/local_test.go` — 21 tests
- `pkg/fs/reconciler.go` — Reconciler (replaces InboxWorker)
- `pkg/fs/reconciler_test.go` — 7 tests

## Files deleted
- `pkg/fs/drivestate.go` — DriveState type
- `pkg/fs/drivestate_test.go`
- `pkg/fs/inbox_worker.go` — InboxWorker type
- `pkg/fs/inbox_worker_test.go`

## Files modified
- `pkg/fs/daemon_v2_5.go` — New wiring, seed rewrite, migration
- `pkg/fs/daemon_v2_5_test.go` — 15 tests (7 existing updated + 8 new)
- `pkg/fs/poller_v2.go` — LocalOpsLog + reconciler poke
- `pkg/fs/poller_v2_test.go` — 6 tests
- `pkg/fs/outbox_worker.go` — LocalOpsLog for prev checksum
- `pkg/fs/outbox_worker_test.go` — 4 tests
- `pkg/fs/watcher_handler.go` — LocalOpsLog for echo prevention
- `pkg/fs/watcher_handler_test.go` — 8 tests
- `pkg/fs/rpc.go` — LocalOpsLog snapshots, reset RPC
- `pkg/fs/rpc_drive_test.go` — Updated for LocalOpsLog
- `pkg/fs/synclog.go` — Removed InboxEntry
- `pkg/fs/opslog/opslog.go` — LWW-Register-Map CRDT in buildSnapshot
- `commands/fs.go` — Added `sky10 fs reset` command

## Key decisions
- **LWW-Register-Map CRDT**: each path is an independent last-writer-wins
  register. Clock tuple `(timestamp, device, seq)` — highest wins regardless
  of processing order. Makes buildSnapshot commutative.
- **Device-based offline delete detection**: seed distinguishes "our device
  deleted this" (propagate delete) from "other device uploaded this" (download it).
- **Reconciler is stateless**: diffs snapshot vs disk each time. No persistent
  work queue. Failed downloads retry on next poke (30s poll interval).
- **Local compaction separate from S3**: ops.jsonl compaction is local-only,
  no network. S3 compaction remains separate.
