---
created: 2026-03-18
model: claude-opus-4-6
---

# Daemon V2: Channel-Based Architecture

Rewrite the daemon's internal architecture so that no goroutine ever
blocks another. S3 is completely isolated from the UI/manifest path.

## Problem

The current daemon has S3 calls scattered across multiple goroutines:
watcher handler, reconciler, initial sync, RPC handlers, auto-approve,
poller. When any S3 call is slow, it cascades — blocks the goroutine,
exhausts the connection pool, makes other goroutines wait, and the
entire daemon becomes unresponsive.

## Design

### Four Goroutines, Three Channels

```
                          ┌──────────────┐
   filesystem ──kqueue──▶ │   Watcher    │──FileEvent──▶ manifestCh
                          └──────────────┘
                          ┌──────────────┐
   manifestCh ──────────▶ │  Manifest    │──S3Job─────▶ s3WorkCh
                          │   Worker     │
   remoteCh ─────────────▶│              │──save──────▶ disk
                          └──────────────┘
                          ┌──────────────┐
   s3WorkCh ─────────────▶│  S3 Worker   │──result────▶ (log only)
                          │  (pool of N) │
                          └──────────────┘
                          ┌──────────────┐
   timer ────────────────▶│   Poller     │──[]Op──────▶ remoteCh
                          └──────────────┘
```

**Watcher goroutine** — reads kqueue events, debounces, sends
`FileEvent` to `manifestCh`. Never touches S3. Never blocks.

**Manifest worker** — single goroutine, owns the manifest exclusively.
Reads from `manifestCh` (local changes) and `remoteCh` (remote ops).
Updates manifest, saves to disk, enqueues S3 jobs to `s3WorkCh`.
This is what the RPC reads. Always responsive.

**S3 worker pool** — N goroutines (e.g. 3) reading from `s3WorkCh`.
Executes uploads, deletes, op writes. If S3 is slow, jobs queue up.
Nothing else is affected. Retries on failure.

**Poller** — timer-based, fetches new remote ops from S3, sends
parsed ops to `remoteCh`. The manifest worker processes them.

### S3Job Types

```go
type S3Job struct {
    Type      S3JobType // Upload, Delete, WriteOp
    Path      string
    LocalPath string    // for uploads
    Op        *Op       // for op writes
}
```

### RPC Reads

`rpcList` and `rpcInfo` read the manifest file from disk. Zero
channels, zero S3, zero blocking. Already implemented in v0.7.x.

### Reconciliation

Every 30 seconds, the manifest worker scans the local directory
and compares against its in-memory state. Differences become
FileEvents fed back into the normal pipeline. No S3 in this path.

### Error Handling

S3 failures don't affect the manifest. The S3 worker logs errors
and can retry. A persistent failure queue could be added later.
The manifest always reflects local truth.

## Milestones

### M1: Define Types and Channels
- [ ] `S3Job` struct (Upload, Delete, WriteOp)
- [ ] Channel types: `manifestCh chan FileEvent`, `s3WorkCh chan S3Job`, `remoteCh chan []Op`
- [ ] New `DaemonV2` struct with channels and goroutine lifecycle

### M2: Manifest Worker
- [ ] Single goroutine owns the `DriveManifest`
- [ ] Reads `FileEvent` from `manifestCh` → update manifest + enqueue S3Job
- [ ] Reads `[]Op` from `remoteCh` → download files + update manifest
- [ ] Saves manifest to disk after each batch
- [ ] Expose manifest for RPC reads (already done — reads disk file)

### M3: S3 Worker Pool
- [ ] N goroutines reading from `s3WorkCh`
- [ ] Execute: upload file, delete file, write op
- [ ] Respect semaphore/concurrency limits (already have sem)
- [ ] Log errors, don't crash
- [ ] Optional: retry queue for transient failures

### M4: Watcher Integration
- [ ] Watcher sends `FileEvent` to `manifestCh` (not `localWork`)
- [ ] Remove old `uploadWorker`, `processLocalEvents`
- [ ] Batch timer stays (300ms debounce) but sends to `manifestCh`

### M5: Poller Integration
- [ ] Poller sends `[]Op` to `remoteCh`
- [ ] Manifest worker processes remote ops: download + update manifest
- [ ] Downloads go through S3 worker (read from S3, write to disk)

### M6: Reconciliation
- [ ] Runs in manifest worker on timer (every 30s)
- [ ] Scans local dir, diffs against manifest
- [ ] Injects synthetic `FileEvent`s for missed changes
- [ ] No S3 in reconcile path — S3 work goes through s3WorkCh

### M7: Initial Sync
- [ ] On startup: manifest worker does one reconcile pass
- [ ] Poller does one immediate poll
- [ ] These seed the manifest from local + remote state
- [ ] No blocking — daemon is responsive from first RPC call

### M8: Tests
- [ ] Slow S3 mock: configurable latency per call (100ms-5s)
- [ ] Flaky S3 mock: random failures at configurable rate
- [ ] Test: create 20 files rapidly, verify manifest has all within 2s
- [ ] Test: delete 10 files, verify manifest updated within 2s
- [ ] Test: S3 down for 10s, verify manifest still works, uploads queue
- [ ] Test: daemon runs for 30s with Cirrus-like polling, stays responsive
- [ ] Test: concurrent file changes + remote ops don't deadlock

### M9: Cleanup
- [ ] Remove old daemon.go code (processLocalEvents, reconcile, etc.)
- [ ] Remove uploadWorker
- [ ] Remove onActivity/onStateChanged callbacks (replace with channel)
- [ ] Update drive.go to create DaemonV2
- [ ] Move completed plan to docs/work/past/
