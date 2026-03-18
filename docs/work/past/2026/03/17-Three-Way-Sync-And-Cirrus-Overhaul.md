---
created: 2026-03-17
model: claude-opus-4-6
---

# Three-Way Sync & Cirrus Overhaul

Major reliability and UI overhaul spanning multi-device sync, daemon
stability, and the Cirrus desktop app.

## Problems Solved

### Stateless sync was fundamentally broken
The sync engine had no memory between restarts. It compared local
filesystem state against remote state (two-way diff) with no concept
of "previous known state." This meant:
- Deleted files got re-downloaded (can't tell "I deleted this" from
  "this is new from another device")
- Empty local files (from broken downloads) overwrote real remote
  content
- No reliable conflict detection

### Daemon kept freezing
The RPC server would go unresponsive after running for a few minutes.
Root cause: `defer cancel()` in the S3 `Get` method cancelled the
HTTP context before the response body was read. Every S3 read was a
race condition — sometimes the read won, sometimes the cancel did.
When cancel won, the connection hung forever.

Secondary cause: `getOrCreateNamespaceKey` held a mutex during S3
network calls. A slow request blocked ALL other RPC calls.

### Multi-device key sharing was broken
- `deriveManifestKey()` used per-device private key seeds, so each
  device encrypted ops with a different key
- Unauthorized devices could overwrite namespace keys
- Auto-approve only checked for `default` namespace key, missing
  drive-specific namespaces
- Joined devices weren't registering themselves

### Cirrus UI was basic
- Title said "Sky" not "Cirrus"
- Flat file list with no folder structure
- Namespace exposed to users (internal concept)
- No drive-based sidebar
- Detail panel took half the window
- Menu bar icon never animated
- Preferences didn't open from menu
- Windows didn't come to foreground

## Design: Three-Way Sync

### Local Manifest

Per-drive file at `~/.sky10/fs/drives/<drive-id>/manifest.json`:

```json
{
  "last_remote_op": 1773706034,
  "files": {
    "notes.txt": { "checksum": "abc...", "size": 42, "modified": "2026-03-17T..." },
    "photos/cat.jpg": { "checksum": "def...", "size": 387301, "modified": "2026-03-16T..." }
  }
}
```

- `last_remote_op`: timestamp of the last S3 op we've seen
- `files`: map of path → {checksum, size, modified} at last sync

### Algorithm (Pseudocode)

```
# On daemon startup for drive "Test":

load local_manifest from ~/.sky10/fs/drives/Test/manifest.json
  # If missing (first run): empty manifest, last_remote_op = 0

# PHASE 1: Detect local changes (what happened while daemon was off)
scan local_fs = walk ~/Cirrus/Test/
diff local_changes = compare local_fs vs local_manifest.files
  for each file in local_fs:
    if not in manifest → local_add
    if checksum differs → local_modify
  for each file in manifest:
    if not in local_fs → local_delete

# PHASE 2: Detect remote changes (what other devices did)
fetch remote_ops where timestamp > local_manifest.last_remote_op
  # Only new ops since we last checked
diff remote_changes = parse ops
  for each op:
    if op.device == my_device → skip (we wrote this)
    if op.type == put → remote_add_or_modify
    if op.type == delete → remote_delete

# PHASE 3: Merge (three-way)
for each path in (local_changes ∪ remote_changes):

  if only local_add → upload to S3, write op
  if only local_modify → upload to S3, write op
  if only local_delete → write delete op to S3

  if only remote_add → download to local
  if only remote_modify → download to local
  if only remote_delete → delete local file

  if local_modify + remote_modify → CONFLICT
    rename local to .conflict.<device>.<ext>
    upload conflict copy
    download remote version to original path

  if local_delete + remote_modify → download (remote wins)
  if local_modify + remote_delete → upload (local wins)
  if both same checksum → no action (first-sync reconciliation)
  if local_delete + remote_delete → no action

# PHASE 4: Update manifest
local_manifest.files = current local_fs state (after all actions)
local_manifest.last_remote_op = max timestamp from remote_ops
save local_manifest to disk

# ONGOING (while running):
on local file event:
  upload, write op, update manifest entry
on remote poll (new ops):
  download/delete, update manifest entry
```

### Merge Rules

| Local | Remote | Action |
|-------|--------|--------|
| add | — | upload |
| modify | — | upload |
| delete | — | write delete op |
| — | add | download |
| — | modify | download |
| — | delete | delete local |
| modify | modify | CONFLICT (keep both) |
| delete | modify | download (remote wins) |
| modify | delete | upload (local wins) |
| add | add (same checksum) | no action |
| add | add (diff checksum) | CONFLICT |
| delete | delete | no action |

## Plan & Milestones (all complete)

### M1: Local Manifest Infrastructure ✅
- [x] `DriveManifest` struct with `SyncedFile` entries
- [x] Load/save to `~/.sky10/fs/drives/<drive-id>/manifest.json`
- [x] 8 unit tests for CRUD, persistence, permissions, corruption

### M2: Three-Way Diff ✅
- [x] `ThreeWayDiff` function: (localFS, manifest, remoteOps) → actions
- [x] 5 action types: Upload, Download, DeleteLocal, DeleteRemote, Conflict
- [x] 16 table-driven tests for every merge case

### M3: Wire Into Daemon ✅
- [x] Daemon uses `threeWaySync` instead of old two-way `SyncOnce`
- [x] Manifest loaded on startup, saved after each sync pass
- [x] Live watcher/poller events update manifest entries

### M4: Delete Support ✅
- [x] Local delete → write delete op to S3
- [x] Remote delete op → delete local file + remove from manifest

### M5: Conflict Handling ✅
- [x] Rename local to `.conflict.<device>.<ext>`, upload conflict copy
- [x] Download remote version to original path

### M6: Integration Tests (MinIO) ✅
- [x] Delete syncs across devices
- [x] Offline file sync (B writes while A is off)
- [x] Manifest persists across restart (deletes stay deleted)
- [x] First sync downloads everything
- [x] Conflict creates `.conflict` file

### M7: Cleanup ✅
- [x] Removed `sync.go`, `syncstate.go`, old integration tests
- [x] Extracted `SyncConfig` to `syncconfig.go`
- [x] CLI `fs sync --once` uses new three-way sync

## Other Fixes

### Daemon Stability
- S3 `Get`: `cancelOnClose` wrapper defers context cancel until body read
- 30-second timeout on all S3 operations
- Mutex only held for in-memory cache, not during network I/O
- Non-blocking daemon: initial sync, uploads, watcher all separate goroutines
- `RegisterDevice` runs in background goroutine
- Drives auto-start on daemon launch

### Multi-Device
- Ops encrypted with shared namespace key, not per-device key
- Unauthorized devices get "access denied" instead of overwriting keys
- Auto-approve wraps ALL namespace keys
- New namespaces auto-wrapped for all registered devices
- Local key cache at `~/.sky10/fs/keys/` for recovery
- Device registry tracks version, last_seen, alias

### Cirrus Browser
- Tree view with expandable folders
- Finder-style column browser
- Toggle persisted via @AppStorage
- Drives in sidebar, not namespaces
- Slim toggle-able inspector panel
- Custom 3-frame cloud animation (drifting hump)
- Preferences and Open Cirrus bring windows to foreground

### Config Restructure
```
~/.sky10/
├── keys/
│   └── key.json
└── fs/
    ├── config.json
    ├── drives.json
    └── drives/
        └── <drive-id>/
            └── manifest.json
```

## Files Created
- `pkg/fs/threeway.go` — three-way diff algorithm
- `pkg/fs/drivemanifest.go` — persistent per-drive manifest
- `pkg/fs/syncconfig.go` — extracted SyncConfig
- `pkg/fs/testharness_test.go` — MinIO test harness
- `pkg/fs/integration_manifest_test.go` — three-way integration tests
- `pkg/fs/concurrency_test.go` — mutex/deadlock tests
- `pkg/fs/multidevice_test.go` — multi-device regression tests
- `cirrus/macos/cirrus/Views/Browser/FileTreeView.swift`
- `cirrus/macos/cirrus/Views/Browser/FileListView.swift`
- Cloud animation PNGs (3 frames × 2 scales)

## Files Removed
- `pkg/fs/sync.go` — old two-way SyncEngine (961 lines)
- `pkg/fs/syncstate.go` — old SyncState
- `pkg/fs/integration_sync_test.go` — old engine tests

## Releases
v0.5.0 through v0.6.2 (12 releases)
