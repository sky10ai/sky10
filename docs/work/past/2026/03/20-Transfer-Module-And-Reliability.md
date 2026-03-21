---
created: 2026-03-20
model: claude-opus-4-6
---

# Transfer Module & Download Reliability

New `pkg/transfer` package for non-blocking streaming progress, plus
fixes for S3 timeouts, RPC deadlocks, and poller state recovery.
Released as v0.10.3, v0.10.4, v0.11.0.

## Problems

1. **S3 wall-clock timeout**: `http.Client{Timeout: 30s}` killed active
   transfers after 30 seconds regardless of whether bytes were flowing.
   Large files (>4MB chunks on slow connections) always failed. JP's
   outbox had 115 files stuck because uploads kept timing out.

2. **RPC server deadlock**: `broadcastLoop` held `s.mu` while writing
   to subscriber connections. A dead Cirrus connection blocked the write
   indefinitely, locking out all new RPC connections.

3. **Poller skipped own ops on fresh log**: After deleting ops.jsonl,
   the poller skipped the device's own S3 ops (including deletes),
   causing deleted files to reappear.

4. **No download progress visibility**: No way to know what files were
   being downloaded or if transfers were stalled.

## What was built

### pkg/transfer (`94c1b58`, `55f0288`, `cfabe05`)

New package: transport-agnostic streaming wrappers with progress and
idle timeout.

- **Reader**: wraps `io.Reader` with atomic byte counting, non-blocking
  progress channel (buffer-1, drain-and-replace for latest value)
- **Writer**: same pattern for `io.Writer`
- **Idle timeout**: each `Read` runs in a goroutine. If no bytes arrive
  within the timeout, the underlying reader is closed (unblocking the
  stuck read), the goroutine exits cleanly, `ErrIdleTimeout` returned.
  No leaked goroutines.
- **Non-blocking**: progress updates never block Read/Write. Slow
  consumers get the latest state, intermediate updates dropped.
- 10 tests including stalled reader, goroutine cleanup, false positive

### S3 timeout fix (`82ef1e6`)

- Removed `http.Client{Timeout: 30s}` — wall-clock timeout that killed
  active transfers
- Removed `withTimeout` context wrapping on PUT and GET
- Kept transport-level timeouts: `ResponseHeaderTimeout: 15s`,
  `TLSHandshakeTimeout: 10s`, `IdleConnTimeout: 60s`
- TCP keepalive (Go default 30s) detects dead connections
- Metadata ops (LIST, HEAD, DELETE) keep 30s `withTimeout`

### Wired into downloads (`c02cd70`)

- `downloadChunks`: shared method for `Get` and `GetChunks` (-54 lines)
- Every chunk read wrapped with `transfer.Reader` + 30s idle timeout
- Stalled S3 GET → reader closed → goroutine exits → error propagates
- Reconciler retries on next poll cycle (30s)

### Reconciler download timeout (`22a9c64`)

- 2-minute `context.WithTimeout` per file download
- Safety net for multi-chunk downloads that stall across chunks

### RPC deadlock fix (`f98da37`)

- `broadcastLoop` snapshots subscribers under lock, writes without it
- 5-second `SetWriteDeadline` per subscriber connection
- Dead connections time out, get removed, lock never held during I/O

### Poller fresh log fix (`fcaff00`)

- When cursor=0 (fresh local log), import ALL S3 ops including own
- Prevents delete ops from being lost after ops.jsonl deletion
- Regression test: `TestPollerV2FreshLogImportsOwnOps`

## Files created
- `pkg/transfer/transfer.go` — Reader, Writer, Progress, idle timeout
- `pkg/transfer/errors.go` — ErrIdleTimeout
- `pkg/transfer/transfer_test.go` — 10 tests

## Files modified
- `pkg/adapter/s3/s3.go` — Removed wall-clock timeout from PUT/GET
- `pkg/fs/skyfs.go` — downloadChunks with transfer.Reader, content hash
- `pkg/fs/reconciler.go` — 2-minute per-file timeout, checksumMatch
- `pkg/fs/poller_v2.go` — cursor=0 imports own ops
- `pkg/fs/poller_v2_test.go` — Fresh log regression test
- `pkg/fs/rpc.go` — Broadcast deadlock fix
- `pkg/fs/watcher_handler.go` — Chunk hash fallback in echo prevention

## Key decisions
- **No external package for progress**: Go's `io.Reader` composition is
  sufficient. Progress bars (cheggaaa/pb, schollz/progressbar) are CLI-
  specific; we need daemon→RPC→Cirrus, so a custom wrapper is cleaner.
- **Goroutine-per-Read for idle timeout**: Go goroutines are cheap (~2KB).
  The alternative (checking idle after Read returns) doesn't work when
  Read itself is blocked on a stalled TCP connection.
- **Close reader to clean up goroutine**: When idle timeout fires, close
  the underlying reader (implements io.Closer for network streams).
  The blocked Read returns immediately, goroutine exits. No leaks.
- **Wall-clock timeout was the root cause**: Not network issues, not S3
  issues — a 30-second hard timeout killed every transfer over 30s.
  TCP keepalive + idle timeout is the correct approach.
