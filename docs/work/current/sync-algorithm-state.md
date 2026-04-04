---
created: 2026-03-19
model: claude-opus-4-6
---

# Sync Algorithm — Current State (v0.9.9)

## Architecture

```
LOCAL CHANGE:
  kqueue -> WatcherHandler -> outbox.jsonl -> OutboxWorker -> S3

REMOTE CHANGE:
  PollerV2 (30s timer) -> S3 ops/ -> inbox.jsonl -> InboxWorker -> filesystem

STATE:
  state.json: path -> {checksum, namespace} + last_remote_op cursor
```

Four goroutines: watcher loop, outbox worker, inbox worker, poller.

## Data Flows

### Local create/modify

1. kqueue fires -> 300ms debounce -> WatcherHandler
2. Checksum file, check against state -> if changed: update state, append to outbox, poke worker
3. OutboxWorker reads outbox, calls `store.Put` (chunks + op to S3), removes entry

### Remote create (other device)

1. Poller lists `ops/`, reads new ops since `last_remote_op`
2. Skips own device ops, skips already-have (matching checksum in state)
3. Appends to inbox with chunk hashes, pokes inbox worker
4. InboxWorker calls `store.GetChunks` (direct download, no state load), writes to `/tmp/sky10/inbox/`, atomic rename to final path, updates state

### Local delete

1. kqueue DELETE -> WatcherHandler removes from state, appends delete to outbox
2. OutboxWorker writes delete op directly to S3 (no `loadCurrentState`)

### Remote delete

1. Poller sees OpDelete, checks if file is in state -> if yes, appends to inbox
2. InboxWorker deletes local file, removes from state

### Daemon startup (seed)

1. Scan local directory, compare against state
2. New files -> FileCreated events, modified -> FileModified, missing -> FileDeleted
3. Events sent to WatcherHandler which updates state and queues outbox entries
4. Does NOT update state before sending events (would cause handler to skip)

## Components

### WatcherHandler (`watcher_handler.go`)
- Processes kqueue events, updates state, writes outbox entries
- Deduplicates events by path within batch
- Handles directory trash (synthetic deletes for all files under trashed dir)
- No S3 calls

### OutboxWorker (`outbox_worker.go`)
- Drains outbox.jsonl to S3
- Uploads: `store.Put` with `SetPrevChecksum` from state (no `loadCurrentState`)
- Deletes: writes op directly via `WriteOp` (no `store.Remove`)
- Retries on failure (5s backoff), removes entry on success
- Crash recovery: drains pending on startup

### PollerV2 (`poller_v2.go`)
- Polls `ops/` prefix every 30s
- Reads ops since `state.LastRemoteOp` cursor
- Skips own device ops, filters by namespace
- PUT: skip if state has same checksum, else inbox
- DELETE: only inbox if file is in state
- Passes chunk hashes through to inbox entries

### InboxWorker (`inbox_worker.go`)
- Drains inbox.jsonl to local filesystem
- Downloads via `store.GetChunks` (direct chunk download, no state load)
- Writes to `/tmp/sky10/inbox/` then atomic rename (prevents watcher race)
- Skips entries without chunks (stale, would trigger loadCurrentState)
- Retries on failure (5s backoff)

### DriveState (`drivestate.go`)
- `~/.sky10/fs/drives/<id>/state.json`
- Maps path -> {checksum, namespace}
- Tracks `last_remote_op` cursor for poller
- Mutex-protected, no S3 calls

### Store (`skyfs.go`)
- `Put`: chunks file, encrypts, uploads chunks + writes op. Uses `SetPrevChecksum` to avoid state load
- `Get`: loads full state from S3 (EXPENSIVE, avoid in daemon)
- `GetChunks`: downloads using chunk hashes + namespace directly (safe for daemon)
- `loadCurrentState`: cached after first call, invalidated on writeOp

## S3 Call Budget (steady state per cycle)

| Operation | S3 Calls |
|-----------|----------|
| Poller (no new ops) | 1 LIST |
| Poller (N new ops) | 1 LIST + N GET |
| Upload 1 file | 1 HEAD (dedup) + 1 PUT (chunk) + 1 PUT (op) |
| Download 1 file | 1 GET (chunk) |
| Delete op | 1 PUT (op) |
| Auto-approve (cached) | 1 LIST + 1 GET |

## File Locations

```
~/.sky10/fs/drives/{drive-id}/
  outbox.jsonl      # local changes queue
  inbox.jsonl       # remote changes queue
  state.json        # checksum state + poller cursor

S3:
  ops/{ts}-{device}-{seq}.enc    # append-only ops log
  blobs/{hash[0:2]}/{hash[2:4]}/{hash}.enc  # encrypted chunks
  manifests/snapshot-{ts}.enc    # periodic state snapshots
  keys/namespaces/{ns}.{device}.ns.enc  # wrapped namespace keys
  devices/{id}.json              # device registry
  debug/{device}/{ts}.json       # debug dumps
```

## Known Issues

### 1. Poller replay from scratch is broken
When `last_remote_op=0` (empty state), poller processes ops incrementally.
DELETE ops check state, but state is empty during replay because files
haven't been downloaded yet. Deletes get skipped, bringing back ghost files.

**Root cause**: Poller does incremental processing, not full replay.
A proper fix would build final state from ALL ops (like a CRDT) before
generating inbox entries.

### 2. No conflict resolution
Concurrent edits from two devices: last writer wins by timestamp.
No merge, no `.conflict` file creation. `DetectConflicts` exists in
op.go but isn't wired into V2.5.

### 3. Ops log grows forever
Compact exists (`skyfs.compact` RPC) but isn't automated. 90+ ops
accumulate, making any accidental `loadCurrentState` call expensive.

### 4. `store.Get` still exists
Used by RPC `rpcGet` (manual file download). If called,
triggers full ops load even with caching (first call is expensive).
Only safe daemon path is `GetChunks`.

### 5. Auto-approve S3 overhead
Every 20s, checks all invites. Completed invites cached in memory
but cache lost on restart. Each uncached invite = ~6 S3 calls.
10s timeout per cycle prevents hangs.
