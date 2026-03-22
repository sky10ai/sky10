---
created: 2026-03-21
model: claude-opus-4-6
---

# First-Class Directory Operations (`delete_dir` + `create_dir`)

## Problem

Deleting a directory on one machine didn't propagate correctly to the other.
The system was file-only — ops log, snapshot, scan, and reconciler all ignored
directories. When machine A deleted `Test/`, machine B would delete the
individual files but leave the empty `Test/` directory behind.

`HandleDirectoryTrash` decomposed a directory delete into N individual
`FileDeleted` events. This worked for files but nothing removed the directory
itself, because directories were invisible to every component.

## Solution

Added `delete_dir` as a first-class operation throughout the sync protocol.

### CRDT (opslog)

- New `DeleteDir` entry type
- `buildSnapshot` handles `DeleteDir` as a prefix delete with **tombstone
  semantics**: when processed, it removes all files under the prefix whose
  clock is beaten by the delete's clock
- **Directory tombstone map** (`dirTombstones`) ensures order-independence.
  If `DeleteDir` is processed before the puts it covers, the tombstone
  prevents those puts from being applied. This makes `DeleteDir` commutative
  — all permutations produce the same result.
- `coveredByDirTombstone()` walks up the path hierarchy checking each
  ancestor for a tombstone

### Watcher

- `HandleDirectoryTrash` emits **one** `DeleteDir` op instead of N individual
  `FileDeleted` events. One op to the local log, one entry to the outbox.

### Outbox Worker

- New `writeDeleteDirOp` handler writes the `delete_dir` op to S3

### Reconciler

- `reconcileDirectories()` runs after file downloads/deletes
- Builds a set of "live" directory paths (directories that have at least one
  file in the snapshot)
- Walks the local filesystem for subdirectories
- Removes directories not in the live set, deepest first
- Uses `os.Remove` (not `RemoveAll`) — only removes empty dirs, safe against
  new files the watcher hasn't processed yet
- Also checks outbox for pending `delete_dir` ops to avoid re-downloading
  files under a directory being deleted

### All Daemon Versions

- V1 (`daemon.go`), V2 (`daemonv2.go`), V2.5 (`daemon_v2_5.go`) all handle
  remote `OpDeleteDir` ops

## Files Changed

- `pkg/fs/opslog/opslog.go` — `DeleteDir` type, tombstone logic in `buildSnapshot`
- `pkg/fs/op.go` — `OpDeleteDir` type, `BuildState` handling
- `pkg/fs/watcher_handler.go` — Rewritten `HandleDirectoryTrash`
- `pkg/fs/outbox_worker.go` — `writeDeleteDirOp`
- `pkg/fs/reconciler.go` — `reconcileDirectories`, `underPendingDeleteDir`
- `pkg/fs/daemon.go`, `daemonv2.go` — `OpDeleteDir` in remote op handlers

## Tests

- 3 CRDT unit tests: `TestCRDTDeleteDir`, `TestCRDTDeleteDirLaterPutWins`,
  `TestCRDTDeleteDirOrderIndependent` (all 6 permutations)
- 2 reconciler tests: `TestReconcilerRemovesStaleDirectories`,
  `TestReconcilerDeleteDirEndToEnd`
- Updated watcher handler tests for new `delete_dir` behavior
- 2 MinIO integration tests: `TestIntegrationDeleteDirSyncsAcrossDevices`,
  `TestIntegrationDeleteDirThenRecreate`

## Decisions

- **Tombstone map vs per-file clocks**: Regular `Delete` works with per-file
  clocks because it targets a single known path. `DeleteDir` is a prefix
  operation — it affects paths it hasn't seen yet. Without tombstones, the
  CRDT result depends on processing order (violates commutativity). The
  tombstone map fixes this.
- **`os.Remove` vs `os.RemoveAll`**: Used `os.Remove` for directory cleanup
  because it only succeeds on empty directories. If the user created new files
  that the watcher hasn't processed, `os.Remove` fails safely. `os.RemoveAll`
  would destroy untracked work.
- **One op vs N ops**: Emitting one `delete_dir` op instead of N individual
  file deletes is both more efficient (1 S3 write) and semantically correct
  (the intent was to delete a directory, not N files).

## `create_dir` — Empty Directory Sync (v0.13.0)

Empty directories weren't synced because the entire system was file-only.
Creating an empty folder on one machine was invisible to the other.

### CRDT

- New `CreateDir` entry type with `DirInfo` (namespace, device, seq, modified)
- `buildSnapshot` tracks dirs in a separate `dirs` map with per-dir clocks
- `DeleteDir` also removes sub-directories from the dirs map
- `CreateDir` respects `DeleteDir` tombstones (exact match + ancestors)
- Snapshot save/load includes dirs (`manifestJSON.Dirs` field)
- Local log compaction writes `CreateDir` entries for dirs

### Watcher

- New `DirCreated` event type emitted directly (no debounce) when a
  directory is created
- Handler emits `CreateDir` op to local log + outbox

### Seed

- `ScanEmptyDirectories()` finds directories with no visible files
- Seed emits `CreateDir` for empty dirs not already in snapshot

### Reconciler

- `createDirectories()` creates dirs from snapshot that don't exist on disk
- `reconcileDirectories()` respects explicit dir entries — won't prune an
  empty dir that has a `create_dir` in the snapshot

### Cirrus UI (v0.13.1)

- RPC `skyfs.list` returns `dirs` alongside `files`
- `scanLocalDirectory` detects empty directories on disk
- `buildTree` renders empty dirs as folder nodes
- Swift types updated: `ListFilesResult` wraps files + dirs

## Bugfixes

### `HandleDirectoryTrash` ignored empty dirs (v0.13.2)

Deleting an empty directory that was tracked via `create_dir` silently did
nothing. `HandleDirectoryTrash` only checked `snap.Files()` for tracked
content under the prefix. Empty dirs with no files were invisible.

Fix: also check `snap.Dirs()` for the directory itself and sub-dirs.

### Daemon crash on blocked S3 (v0.13.3)

The S3 credential check on startup (`backend.List(ctx, "ops/")`) was fatal.
If Little Snitch blocked the connection, the daemon exited before the user
could approve the firewall dialog, creating an infinite restart loop.

Fix: log a warning instead of exiting. Drives retry on their poll intervals.

## Tests

- 3 CRDT unit tests: `TestCRDTDeleteDir`, `TestCRDTDeleteDirLaterPutWins`,
  `TestCRDTDeleteDirOrderIndependent` (all 6 permutations)
- 3 CRDT create_dir tests: `TestCRDTCreateDir`,
  `TestCRDTDeleteDirRemovesCreateDir`, `TestCRDTCreateDirAfterDeleteDir`
- 2 reconciler tests: `TestReconcilerRemovesStaleDirectories`,
  `TestReconcilerDeleteDirEndToEnd`
- 2 reconciler create_dir tests: `TestReconcilerCreatesDirectories`,
  `TestReconcilerKeepsExplicitEmptyDir`
- Regression: `TestWatcherHandlerDeleteEmptyCreatedDir`
- Updated watcher handler tests for `delete_dir` and `DirCreated` behavior
- 4 MinIO integration tests:
  - `TestIntegrationDeleteDirSyncsAcrossDevices`
  - `TestIntegrationDeleteDirThenRecreate`
  - `TestIntegrationCreateDirSyncsAcrossDevices`
  - `TestIntegrationDeleteEmptyDirSyncsAcrossDevices` (regression)

## Releases

- v0.12.0 — `delete_dir` first-class operation
- v0.13.0 — `create_dir` first-class operation
- v0.13.1 — Empty dirs visible in Cirrus UI
- v0.13.2 — Fix empty dir delete propagation
- v0.13.3 — Fix daemon crash on blocked S3
