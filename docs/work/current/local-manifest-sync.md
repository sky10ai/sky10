---
created: 2026-03-17
model: claude-opus-4-6
---

# Local Manifest Sync

Three-way sync using a persistent local manifest as the "last known agreed state."
Without it, the daemon can't distinguish "I deleted this" from "this is new from
another device."

## Problem

The current sync is stateless between restarts:
- Deleted files get re-downloaded (can't tell delete from new remote file)
- New remote files don't download reliably (filter logic confused without history)
- `SyncState` was built but never properly persisted/loaded across daemon restarts
- The diff is two-way (local vs remote) when it needs to be three-way
  (local vs manifest vs remote)

## Design

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

- `last_remote_op`: timestamp of the last S3 op we've seen — fetch only newer ops on startup
- `files`: map of path → {checksum, size, modified} at last successful sync

### Three-Way Diff Algorithm

Compare three sources: local filesystem, local manifest (previous state), remote ops.

**Local changes** = diff(local_fs, manifest)
**Remote changes** = ops where timestamp > manifest.last_remote_op AND device != mine

Merge rules:
| Local | Remote | Action |
|-------|--------|--------|
| add | — | upload |
| modify | — | upload |
| delete | — | write delete op |
| — | add | download |
| — | modify | download |
| — | delete | delete local |
| modify | modify | CONFLICT |
| delete | modify | conflict (remote wins default) |
| modify | delete | conflict (local wins default) |
| add | add | conflict |

## Milestones

### M1: Local Manifest Infrastructure
- [ ] Define `DriveManifest` struct (files map + last_remote_op)
- [ ] Load/save to `~/.sky10/fs/drives/<drive-id>/manifest.json`
- [ ] Create manifest directory on drive creation
- [ ] Unit tests for load/save/empty state

### M2: Three-Way Diff
- [ ] New `ThreeWayDiff` function: (localFS, manifest, remoteOps) → actions
- [ ] Action types: Upload, Download, DeleteLocal, DeleteRemote, Conflict
- [ ] Handle all merge rules from table above
- [ ] Table-driven unit tests for every merge case
- [ ] Test: file unchanged on both sides → no action

### M3: Wire Into Daemon
- [ ] Replace `SyncOnce` two-way diff with three-way diff
- [ ] Load manifest on daemon startup
- [ ] Save manifest after each successful sync pass
- [ ] Update manifest entries on watcher upload/download
- [ ] Update `last_remote_op` after processing remote ops
- [ ] Remove old `SyncState` / `LocalChecksums` code

### M4: Delete Support
- [ ] Local delete → write delete op to S3
- [ ] Remote delete op → delete local file + remove from manifest
- [ ] Test: delete on Device A, verify removed on Device B
- [ ] Test: delete while daemon is stopped, verify op written on restart

### M5: Conflict Handling
- [ ] Detect conflicts per merge rules
- [ ] Default resolution: keep both (rename conflicting file with `.conflict.<device>` suffix)
- [ ] Surface conflicts to Cirrus UI (existing conflict alert)
- [ ] Test: simultaneous edit on two devices → conflict file created

### M6: Integration Tests (MinIO)
- [ ] Test: Device A deletes file, Device B sees it removed
- [ ] Test: Device B creates file while Device A is offline, A gets it on restart
- [ ] Test: both devices edit same file → conflict
- [ ] Test: daemon restart preserves manifest, doesn't re-download deleted files
- [ ] Test: first sync on fresh device downloads everything

### M7: Cleanup
- [ ] Remove old `SyncState` / `syncstate.go`
- [ ] Remove `LocalChecksums` from `SyncEngine`
- [ ] Remove empty-file-wipe hack in `DiffLocalRemote` (three-way makes it unnecessary)
- [ ] Update CLAUDE.md if sync architecture docs reference old approach
- [ ] Move completed plan to `docs/work/past/`
