---
created: 2026-03-18
model: claude-opus-4-6
---

# Daemon V2.5: Inbox/Outbox Sync

Replace manifest-based sync with two persistent queues. The
filesystem IS the state. No manifest needed for file listing.

## Architecture

```
LOCAL CHANGE:
  watcher → outbox (append) → outbox worker → S3

REMOTE CHANGE:
  poller → inbox (append) → inbox worker → filesystem

CIRRUS UI:
  file browser → reads filesystem directly (not manifest/RPC)
  activity view → reads inbox + outbox (pending/completed)
  sync status icon → outbox empty = synced
```

### Outbox (`~/.sky10/fs/drives/<id>/outbox.jsonl`)

Append-only log of local changes waiting to push to S3.

```jsonl
{"op":"put","path":"doc.txt","checksum":"abc...","namespace":"Test","local_path":"/Users/bf/Cirrus/Test/doc.txt","ts":1773867120}
{"op":"delete","path":"old.txt","checksum":"def...","namespace":"Test","ts":1773867125}
```

**Outbox worker**: reads entries, uploads/deletes to S3, removes
entry on success. Retries on failure.

### Inbox (`~/.sky10/fs/drives/<id>/inbox.jsonl`)

Append-only log of remote changes waiting to apply locally.

```jsonl
{"op":"put","path":"photo.jpg","checksum":"ghi...","namespace":"Test","device":"qdumgpq0...","ts":1773867130}
{"op":"delete","path":"gone.txt","device":"qdumgpq0...","ts":1773867135}
```

**Inbox worker**: reads entries, downloads from S3 / deletes
locally, removes entry on success.

### Watcher Edge Case: Directory Trash

When a watched directory disappears, the watcher emits delete
events for every file that was in it. We know the files from...
the filesystem scan at watcher startup (it adds all subdirs).
Actually: the outbox worker needs to know the checksum/namespace
to write the delete op. This comes from the last known state.

**Minimal state file** (`~/.sky10/fs/drives/<id>/state.json`):
just a map of path → {checksum, namespace}. Updated after every
successful inbox/outbox operation. NOT used for UI — only for:
- Detecting what changed since last sync
- Providing checksum/namespace for delete ops
- Detecting directory-trash missed deletes

### Cirrus UI Changes

1. **Default view: Activity** — shows recent inbox/outbox entries
   (what synced, what's pending, errors)
2. **File browser: reads filesystem** — shows ~/Cirrus/Test/
   contents directly, overlays sync status per file:
   - Green checkmark = synced (not in outbox)
   - Arrow up = uploading (in outbox)
   - Arrow down = downloading (in inbox)
   - Red = error (failed entry in outbox/inbox)
3. **Devices** — needs to be fixed (broken again)

### No Manifest

The manifest is gone. The filesystem is truth. The state file
is minimal — just checksums for diff detection, not for UI.

## Milestones

### M1: Inbox/Outbox Types and Persistence
- [ ] `OutboxEntry` struct (op, path, checksum, namespace, local_path, ts)
- [ ] `InboxEntry` struct (op, path, checksum, namespace, device, ts)
- [ ] `SyncLog` — append-only JSONL file, read all, remove entry, append
- [ ] Load/save to `~/.sky10/fs/drives/<id>/outbox.jsonl` and `inbox.jsonl`
- [ ] Unit tests for append/read/remove

### M2: State File
- [ ] `DriveState` struct — map of path → {checksum, namespace}
- [ ] Load/save to `~/.sky10/fs/drives/<id>/state.json`
- [ ] Updated after each successful inbox/outbox operation
- [ ] Used by watcher to detect changes + provide delete info

### M3: Outbox Worker
- [ ] Goroutine reads outbox entries
- [ ] Put: upload file to S3, write op
- [ ] Delete: write delete op to S3 (using checksum/namespace from entry)
- [ ] Remove entry on success, retry on failure
- [ ] Process pending entries on startup (crash recovery)

### M4: Inbox Worker
- [ ] Goroutine reads inbox entries
- [ ] Put: download file from S3, write to disk
- [ ] Delete: remove local file
- [ ] Update state file after each operation
- [ ] Remove entry on success

### M5: Watcher → Outbox
- [ ] Watcher sends events to outbox (not manifest)
- [ ] Compute checksum, get namespace from config
- [ ] On directory trash: emit deletes for known files (from state)
- [ ] Update state file on local changes

### M6: Poller → Inbox
- [ ] Poller fetches remote ops, writes to inbox
- [ ] Filter: only other devices, only after last seen timestamp
- [ ] Persist last seen timestamp in state file

### M7: DaemonV2.5
- [ ] Wire everything: watcher → outbox, poller → inbox
- [ ] Two workers: outbox worker, inbox worker
- [ ] Push events to Cirrus on state changes
- [ ] Remove DaemonV2, old daemon, manifest code

### M8: Cirrus Activity View
- [ ] New default view showing recent sync activity
- [ ] Pending uploads/downloads
- [ ] Completed operations
- [ ] Errors with retry option

### M9: Cirrus File Browser → Filesystem
- [ ] Read filesystem directly instead of RPC list
- [ ] Sync status overlay per file (check outbox/inbox)
- [ ] Remove RPC list/info calls

### M10: Fix Devices View
- [ ] Debug why devices aren't showing
- [ ] Ensure device list works reliably

### M11: Tests
- [ ] Outbox: write entry, process, verify S3 op written, entry removed
- [ ] Inbox: write entry, process, verify file downloaded, entry removed
- [ ] Crash recovery: entries survive restart, get retried
- [ ] Rapid create/delete: outbox drains correctly
- [ ] Long-running daemon: stays responsive for 30s
- [ ] Directory trash: all files get delete entries in outbox
