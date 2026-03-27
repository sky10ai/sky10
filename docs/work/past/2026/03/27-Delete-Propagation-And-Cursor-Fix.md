---
created: 2026-03-27
model: claude-opus-4-6
---

# Delete Propagation Through Compaction & Cursor Display Fix

## Problem

Two machines diverged permanently: local had 16,666 files, remote had 33,368.
The local Test drive had 156 files while remote had 16,665. After both machines
"settled" (daemons idle, outbox drained), the gap persisted.

### Misdiagnosis: cursor stuck at zero

Initial investigation showed `last_remote_op: 0` on all drives on both machines.
This appeared to mean the poller cursor was never persisted and kept resetting on
restart. A partial fix was written (persist cursor as a special `Cursor` entry in
ops.jsonl) but left the code in a non-compiling state.

**This was wrong.** The `last_remote_op: 0` was caused by a bug in `rpcDriveList`
(`rpc.go:893-894`): it created a fresh `LocalOpsLog` and called `LastRemoteOp()`
before triggering `Snapshot()`. A new `LocalOpsLog` has `lastRemote=0` because the
cache is cold — `rebuildLocked()` hasn't been called yet. The actual running
daemon's cursor was fine.

Evidence: the drive_test ops.jsonl had 20,047 remote entries with timestamps up to
1774582375. The cursor *should* have been recovered by `rebuildLocked`, and was —
just not by the RPC's throwaway instance.

### Root cause: delete propagation lost through compaction

The actual divergence was caused by `CatchUpFromSnapshot` being one-directional:

1. Local machine deleted `archive/` directory → `delete_dir` entries uploaded to
   S3 `ops/`
2. S3 compaction ran → folded the delete into the snapshot (archive/ files
   disappear from the snapshot), then deleted individual ops from `ops/`
3. Remote machine's poller read `ops/` → the delete_dir ops were already gone
4. Remote machine's `CatchUpFromSnapshot` loaded the S3 snapshot → iterated only
   files **present** in the snapshot. It never checked whether the local snapshot
   had files that the S3 snapshot **didn't**. So the delete was never propagated.

The remote machine kept 16,665 Test files that should have been deleted, because
it never learned about the deletion.

### Data from ops.jsonl analysis

drive_test (Test drive, local machine):
- 20,823 entries total (776 local, 20,047 remote)
- 17,088 entries with wrong namespace "Node" (cross-contamination from earlier bug)
- 27 `delete_dir` entries, including multiple `archive` deletes
- Latest `delete_dir archive`: ts=1774306355
- 308 `archive/` puts after the delete (re-created files)

drive_Node (Node drive, local machine):
- 91,080 entries total (36,378 local, 54,702 remote)
- 157 entries with wrong namespace "Test" (minor cross-contamination)

## Changes

### Fix 1: `LastRemoteOp` triggers rebuild on cold cache

`pkg/fs/opslog/local.go` — `LastRemoteOp()` now calls `rebuildLocked()` if the
cache is nil, so it returns the correct cursor value even on a freshly constructed
`LocalOpsLog`. This fixes both the RPC display bug and any other caller that reads
the cursor before triggering a snapshot build.

### Fix 2: `rpcDriveList` call order

`pkg/fs/rpc.go` — Swapped `Snapshot()` before `LastRemoteOp()` in `rpcDriveList`.
Belt-and-suspenders fix alongside the `LastRemoteOp` change.

### Fix 3: Bidirectional `CatchUpFromSnapshot`

`pkg/fs/opslog/local.go` — Added a second pass after the existing injection loop.
For each file in the local snapshot that is:
- Matching the namespace filter (if set)
- **Absent** from the S3 snapshot
- Has a timestamp **older** than the snapshot timestamp

...a `Delete` entry is injected with `Device: "_catchup"` and
`Timestamp: snapshotTS`. This ensures the delete wins LWW over the old put.

The `timestamp < snapshotTS` guard prevents deleting files that were created
locally after the snapshot was built (not yet synced to S3).

### Reverted: cursor persistence partial edit

Reverted the half-written cursor persistence code in `SetLastRemoteOp` that
referenced an undefined `Cursor` EntryType. The cursor persistence approach
(special entry in ops.jsonl) is still a valid improvement but wasn't the root
cause and doesn't need to ship with this fix.

## Tests

All test-first — written before fixes, verified to fail, then fixes applied.

| Test | What it covers |
|------|---------------|
| `TestLastRemoteOpColdCache` | Fresh `LocalOpsLog` returns correct cursor without calling `Snapshot()` first |
| `TestCatchUpFromSnapshotPropagatesDeletes` | Files absent from S3 snapshot with old timestamps are deleted locally; files with timestamps after snapshot survive |
| `TestCatchUpDeleteRespectsNamespace` | Delete propagation respects namespace filter — only deletes matching-namespace files |

## Decisions

- **`Device: "_catchup"` on injected deletes**: Uses a synthetic device ID that
  can't collide with real device IDs (which are 16-char hex derived from public
  keys). This means catch-up deletes contribute to `lastRemote` in
  `rebuildLocked`, which is desirable — it advances the cursor past the snapshot.

- **Not persisting cursor yet**: The cursor persistence idea (special `Cursor`
  entry in ops.jsonl) is still worth doing for robustness, but it's not the root
  cause of the divergence. Deferred to avoid scope creep.

- **Files only, not directories**: The delete propagation pass only handles files
  (`localSnap.files`), not directories (`localSnap.dirs`). Directory deletes
  propagate differently (via `delete_dir` ops) and the current divergence is
  caused by missing file deletes. Can extend later if needed.

## Files changed

- `pkg/fs/opslog/local.go` — `LastRemoteOp()` rebuild, `CatchUpFromSnapshot` delete pass, revert cursor partial edit
- `pkg/fs/opslog/local_test.go` — 3 regression tests
- `pkg/fs/rpc.go` — `rpcDriveList` call order fix
