---
created: 2026-03-20
model: claude-opus-4-6
---

# Checksum Fix & Cirrus Stability

Fixed cross-device echo loop caused by checksum scheme mismatch,
syncStatus RPC flood in Cirrus, and added reset/compact to the UI.
Released as v0.10.1 and v0.10.2.

## Problem

After V3 launch (v0.10.0), cross-device sync appeared to work but had
three critical bugs:

1. **Echo loop**: Every file was re-uploaded on every poll cycle, infinitely.
   Device A uploads file → Device B's poller picks it up → watcher sees
   the downloaded file → echo prevention fails → re-uploads → Device A
   picks it up → repeat forever.

2. **syncStatus flood**: Cirrus `subscribeToEvents()` spawned an independent
   `Task` for each `state.changed` event, each calling `refresh()` (4+ RPC
   calls). During sync bursts, hundreds of concurrent RPC tasks starved
   the daemon.

3. **Ghost entries in ops log**: Files with `checksum=a7ffc6f8...` (empty
   hash), size=0, zero chunks sitting in the snapshot. These came from
   pre-reset state and could never be downloaded or reconciled.

## Root cause: Checksum scheme mismatch

Two different checksum schemes were in use:

- `fileChecksum()` (scan.go): `SHA3-256(file content)` → e.g. `f504...`
- `store.Put()` (skyfs.go): `SHA3-256(concatenated chunk hashes)` → e.g. `2641...`

Echo prevention in `WatcherHandler` (line 63) compared `fileChecksum()`
against the ops log entry's checksum. For remote ops (which stored the
hash-of-chunk-hashes scheme), these never matched.

For a single-chunk file: content hash = `f504...`, but S3 op checksum =
`SHA3-256("f504...")` = `2641...`. Always different.

## What was fixed

### v0.10.1: Cirrus stability (`09269b9`)
- **Debounced subscribe events**: `debouncedRefresh()` cancels pending
  refresh and waits 500ms before firing, coalescing rapid events
- **Reset + compact RPC**: Added `reset()` and `compact()` to Swift
  client, wired to DevTools buttons
- **Fixed test target**: Set `PRODUCT_MODULE_NAME` in project.yml
- **Updated AppState tests**: Aligned with filesystem-based `refresh()`

### v0.10.2: Echo loop fix (`b126480`)
- **Content hash in store.Put**: `TeeReader` hashes raw content during
  chunking so the S3 op checksum matches `fileChecksum()`. No extra
  file reads — hashing happens during the chunker's read.
- **Backwards-compat checks**: Watcher, poller, and reconciler also
  compare chunk hashes for old-scheme ops still in the log
  (single-chunk: `chunks[0]` == content hash)
- **UI file count**: Sidebar and status bar use scanned file count
  (disk) instead of ops log snapshot count

### `sky10 fs reset` command (`a86472f`)
- `rpcReset()`: Deletes all S3 ops + snapshots + local state files
- `fsResetCmd`: CLI wrapper via `sky10 fs reset`
- Keeps device keys and config intact

## Files modified

- `pkg/fs/skyfs.go` — TeeReader for content hash during Put
- `pkg/fs/watcher_handler.go` — Chunk hash fallback in echo prevention
- `pkg/fs/poller_v2.go` — Chunk hash fallback in dedup check
- `pkg/fs/reconciler.go` — `checksumMatch()` helper, chunk hash fallback
- `pkg/fs/rpc.go` — `rpcReset()`, opslog import for rpcList/rpcInfo
- `commands/fs.go` — `fsResetCmd()`
- `cirrus/macos/cirrus/Models/AppState.swift` — Debounced refresh
- `cirrus/macos/cirrus/Services/SkyClient.swift` — reset/compact RPCs
- `cirrus/macos/cirrus/Services/SkyClientProtocol.swift` — Protocol update
- `cirrus/macos/cirrus/Views/DevTools/DevToolsView.swift` — Reset/compact buttons
- `cirrus/macos/cirrus/Views/Browser/SidebarView.swift` — Disk-based file count
- `cirrus/macos/cirrus/Views/Shared/SyncStatusBar.swift` — Disk-based file count
- `cirrus/macos/cirrus/Views/Shared/Previews.swift` — Mock updates
- `cirrus/macos/cirrus-tests/MockSkyClient.swift` — Mock updates
- `cirrus/macos/cirrus-tests/AppStateTests.swift` — Filesystem-based tests
- `cirrus/macos/project.yml` — PRODUCT_MODULE_NAME fix

## Known remaining issue

The ops log snapshot can contain ghost entries — files with empty hash
(`a7ffc6f8...`), zero size, zero chunks. These come from stale state
before reset and can never be downloaded. The reconciler skips them
("no chunks, skipping") but they inflate the snapshot file count.
`rpcInfo` still counts them. Fix needed: either clean them from the
ops log during compaction, or have the reconciler emit delete ops for
unfulfillable entries.
