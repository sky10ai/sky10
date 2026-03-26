---
created: 2026-03-25
model: claude-opus-4-6
---

# Parallel Chunk Downloads

## Problem

`downloadChunks` fetched chunks sequentially within a single file. Each chunk
completed its full pipeline (S3 fetch + decrypt + decompress + verify + write)
before the next S3 request started. Network latency dominated; CPU work between
chunks was fast. The S3 connection sat idle between chunks.

With 4 concurrent file downloads and 1 chunk per file in flight, only 4 of the
5 S3 semaphore slots were ever used.

## Solution

Bounded prefetch pipeline: up to 3 chunks are fetched concurrently per file.

### Architecture

A sequential producer goroutine acquires prefetch semaphore slots in chunk order,
launching a fetch goroutine for each. Each goroutine runs the full pipeline
(S3 fetch + decrypt + decompress + verify) and deposits plaintext into a
per-chunk result channel. The consumer reads channels in order and writes to the
output.

```
producer (sequential) ──> goroutine 0 ──> slots[0]
                     ──> goroutine 1 ──> slots[1]   ──> consumer (in-order write)
                     ──> goroutine 2 ──> slots[2]
```

Key design decisions:

- **Consumer releases semaphore, not producer.** Bounds memory to
  `ahead(3) x MaxChunkSize(4MB) = 12MB` per file. The producer can't get more
  than 3 chunks ahead of what the consumer has written.
- **Sequential producer.** Prevents out-of-order semaphore acquisition. The
  initial implementation launched all goroutines at once — goroutine 15 could
  grab a slot before goroutine 3, deadlocking the consumer (head-of-line
  blocking). Fixed by acquiring slots in chunk order from a single producer
  goroutine.
- **Single-chunk fast path.** Files with 1 chunk (common for <1MB files) skip
  all goroutine machinery.

### Prerequisite fixes

1. **`Backend.GetRange` missing semaphore** — every other S3 method used
   `acquire`/`release` except `GetRange`. Would cause unbounded concurrency
   with parallel chunk downloads. Added `acquire`/`release` + `cancelOnClose`
   matching the `Get` pattern.

2. **`MemoryBackend.GetRange` missing lock** — read `m.objects` without
   `RLock`. Data race under concurrent access. Added `RLock`/`RUnlock`.

3. **S3 semaphore 5 -> 8** — with 4 files x 3 ahead = 12 requests queuing,
   the old semaphore of 5 bottlenecked the prefetch pipeline. Bumped to 8
   (HTTP transport allows 10 per host, leaving 2 for poller/ops).

### Race fixes

4. **fastcdc-go global table race** — `NewChunker()` writes to a package-level
   lookup table on every call (`table[i] ^= seed`). With `Seed=0` the XOR is a
   no-op, but the race detector flags unsynchronized writes. The parallel
   download tests (which call `Store.Put` from multiple goroutines) made this
   reliably trigger. Fixed with `RWMutex`: write lock around `NewChunker()`,
   read lock around `Next()`.

5. **Watchdog.Heartbeat race** — called from multiple goroutines (outbox
   worker, poller, reconciler) without synchronization. Added mutex.

## Files Changed

| File | Change |
|------|--------|
| `pkg/fs/skyfs.go` | Extract `fetchChunk` method; rewrite `downloadChunks` with prefetch pipeline |
| `pkg/fs/skyfs_test.go` | 3 new tests: cancellation, error mid-stream, ordering (20MB) |
| `pkg/adapter/s3/s3.go` | `GetRange` semaphore + `cancelOnClose`; `MemoryBackend.GetRange` lock; semaphore 5->8 |
| `pkg/fs/chunk.go` | `RWMutex` around fastcdc global table access |
| `pkg/fs/debug.go` | Mutex on Watchdog fields |

## Commits

- `dc53d12` Parallel chunk downloads: prefetch up to 3 chunks concurrently
- `276857b` Fix deadlock in parallel chunk downloads: sequential producer
- `35e5f2e` Fix data races in fastcdc chunker and Watchdog
