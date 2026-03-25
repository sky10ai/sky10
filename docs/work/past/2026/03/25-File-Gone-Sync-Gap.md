---
created: 2026-03-25
model: claude-opus-4-6
---

# File-Gone Sync Gap

## Problem

When a file is queued for upload but deleted from disk before the outbox
worker processes it ("file gone"), the local ops log retains a stale `put`
op (from `AppendLocal`). If the same file reappears with identical content
(e.g., `rm -rf` then re-clone of a git repo), the watcher handler's dedup
check sees matching checksum in the local log and skips it. The blob is
never uploaded to S3. Other devices get the op but can't download the file.

### Root cause

The watcher handler's dedup at `watcher_handler.go:64` trusts the local
log as proof that a file has been synced. But a "file gone" failure means
the local log has a put op whose blob was never stored in S3.

### How we found it

Data dump analysis from two devices syncing the `create-t3-app` repo:
- 683 files detected by watcher, 340 uploaded, 343 "file gone"
- Other machine had 303 snapshot files vs 539 on this machine
- All 238 missing files had `modified` timestamps matching the clone time
- Logs showed `watcher: unchanged` for files that reappeared after re-clone

## Fix A: Outbox worker records delete on file-gone

**File:** `pkg/fs/outbox_worker.go` (uploadFile method)

When `os.Open` fails, append `AppendLocal(delete)` before returning.
The CRDT's delete supersedes the stale put. When the file reappears,
the watcher sees no matching put (last op is delete) and re-queues it.

## Fix B: Periodic integrity sweep

**File:** `pkg/fs/reconciler.go` (new `integritySweep` method)

Scans snapshot entries where `device == self && chunks == nil`. The
"no chunks" signal is reliable: `AppendLocal` creates put entries
without chunks; after successful upload, the op goes to S3 with chunks
and the poller imports it back. Entries that never completed the
round-trip have `chunks == nil`.

The sweep runs every 5 minutes via a ticker in `Reconciler.Run`. For
each chunkless entry, if the file exists on disk, it's re-queued in
the outbox. No S3 calls needed.

### Why both

- **A** prevents the common case (file deleted during upload burst)
- **B** catches any path to blob-missing (partial uploads, crashes, S3
  errors) as a self-healing safety net

## Tests

- `TestOutboxWorkerFileGoneAppendsDelete` — verifies local log gets
  delete op when file is gone
- `TestOutboxWorkerFileGoneThenReappear` — end-to-end: file gone →
  delete recorded → file reappears → watcher re-queues (not skipped)
- `TestIntegritySweepRequeuesChunklessFile` — sweep re-queues file
  with no chunks that exists on disk
- `TestIntegritySweepSkipsCompletedAndRemote` — sweep skips files
  from other devices and files with chunks (already uploaded)

## Files changed

- `pkg/fs/outbox_worker.go` — AppendLocal(delete) in file-gone path
- `pkg/fs/reconciler.go` — integritySweep method + 5min ticker in Run
- `pkg/fs/outbox_worker_test.go` — 2 regression tests
- `pkg/fs/reconciler_test.go` — 2 regression tests
