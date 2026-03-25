---
created: 2026-03-24
model: claude-opus-4-6
---

# Post-Compaction Bootstrap Gap + Watcher Dangling Symlink Fix

## Problem 1: Poller misses files after S3 compaction

After `Compact()` runs, all individual ops are folded into a snapshot file
(`manifests/snapshot-{ts}.enc`) and deleted from `ops/`. The S3
`OpsLog.Snapshot()` handles this correctly — it loads the snapshot base,
then replays any newer ops on top.

But the **poller** only reads `ops/` via `ReadBatched`. After compaction,
`ops/` is empty, so a fresh device (cursor=0) gets nothing. All files
captured in the snapshot are invisible to it. The device only sees ops
written *after* the compact.

**Impact**: A new device joining after compaction misses all previously
synced files. Only files added after the compact appear.

**Root cause**: The poller + local log path was designed before compaction
existed. It assumes all state is in individual ops, not snapshots.

**Fix needed**: On first sync (cursor=0, empty local log), the poller
should bootstrap from the S3 snapshot before reading ops. This way the
local log starts with the full compacted state.

**Tests added** (documenting the gap — these will need updating when fixed):
- `TestPollerBatchAfterCompactSeesNothing` — fresh poller gets 0 files
- `TestPollerBatchCompactThenNewOps` — only post-compact ops visible
- `TestPollerBatchMultiCompactOnlyLatestOps` — multiple compactions
- `TestIntegrationPollerAfterCompactMinIO` — full flow with reconciler
- `TestIntegrationMultiCompactPollMinIO` — multiple compacts + poll
- `TestIntegrationCompactDeletesThenPollMinIO` — compact with deletes

**Also added** (regression tests for the parts that DO work):
- `TestCompactThenNewOpsSnapshot` — S3 Snapshot merges correctly
- `TestCompactDeletePostCompactSnapshot` — deletes after compact work

## Problem 2: Watcher crashes on dangling symlinks (node_modules)

The Node drive daemon failed to start:
```
creating daemon for Node: creating watcher:
  "/Users/bf/Cirrus/Node/t3-test/cli/node_modules/better-auth":
  no such file or directory
```

`better-auth` is a bun-created symlink pointing to
`../../node_modules/.bun/better-auth@1.3.7.../` which doesn't exist
(the `.bun/` directory wasn't fully synced after compaction — see
Problem 1 above).

**Root cause**: NOT `filepath.WalkDir` as initially suspected. The error
comes from **fsnotify's kqueue backend**. When `fsnotify.Watcher.Add(dir)`
is called, kqueue internally calls `os.Stat()` (which follows symlinks)
on each entry in the directory. A dangling symlink causes `Stat` to fail
with ENOENT, and `Add` returns the error. `addRecursive` propagated it
as fatal, killing the entire drive daemon startup.

**Fix**: Skip `watcher.Add` errors for non-root directories. Log a
warning so failures are visible in `daemon.log`. Return `nil` (not
`SkipDir`) so child directories are still visited — they may be
watchable even if the parent isn't.

**Downstream risks of this fix**:
- A directory containing dangling symlinks is not watched. New files
  created *directly* in it won't trigger events until the next daemon
  restart (seed scan picks them up). Child directories are still watched.
- If system watch limits are hit (inotify `max_user_watches`), later
  directories silently lose watches with only a log warning.
- `handleEvent` (runtime) already silently ignored `addRecursive`
  errors — the initial setup was inconsistently more strict. This fix
  makes them consistent.

**Tests added**:
- `TestWatcherAddRecursiveSkipsDanglingSymlinks`
- `TestWatcherAddRecursiveSkipsPermissionDenied`

## Files changed

- `pkg/fs/watcher.go` — `addRecursive` skips non-root errors with warning
- `pkg/fs/watcher_test.go` — 2 regression tests
- `pkg/fs/poller_batch_test.go` — 3 unit tests for compact gap
- `pkg/fs/opslog/compact_test.go` — 2 regression tests for snapshot merge
- `pkg/fs/integration_compact_test.go` — 3 MinIO integration tests

## Future work

1. **Bootstrap poller from S3 snapshot** — the main fix for Problem 1.
   On first sync (cursor=0), load the S3 snapshot into the local log
   before reading ops.
2. **Consider fsnotify fork or upstream fix** — `watchDirectoryFiles`
   should use `Lstat` not `Stat`, or skip entries that fail.
3. **Watch limit monitoring** — detect when approaching system limits
   and warn more aggressively (or fail the drive with a clear error
   instead of silently degrading).
