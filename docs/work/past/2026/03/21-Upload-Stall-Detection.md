---
created: 2026-03-21
model: claude-opus-4-6
---

# Upload Stall Detection

## Problem

The daemon on a second machine kept freezing during large uploads (86 pending
outbox files). Process alive, 310MB RAM, **0% CPU** — completely idle for 30+
minutes. Last log line: `outbox: draining pending=86` then silence.

### Root Cause

`Backend.Put()` in the S3 adapter had **no timeout at all**. The code comment
said "TCP keepalive detects dead connections" — this is wrong.

TCP keepalive only fires on **idle** connections. When a goroutine is actively
writing a 4MB chunk body to a dead TCP socket (e.g. after laptop sleep/wake),
the `write()` syscall blocks at the OS level waiting for TCP **retransmission**
to give up — which takes **15-30 minutes on macOS**. During this time:

1. The goroutine holds a semaphore slot (5 total)
2. 0% CPU because it's in a kernel wait
3. No logs because the code never returns
4. Other goroutines (poller, reconciler) may also be stuck on dead connections
5. If all 5 semaphore slots are held, the entire daemon is frozen

### Why transfer.Reader didn't help (before this fix)

The transfer module's idle timeout detects stalls **within** a Read() call —
designed for downloads where the network is the data source. For uploads, the
data source is `bytes.NewReader` (in-memory), so Read() completes instantly.
The stall is on the **write side** (HTTP client → TCP socket), which the
read-side timeout can't see.

### Why context.WithTimeout is suboptimal

A flat 2-minute timeout per chunk would work but kills slow-but-working
connections. A 4MB chunk at 500 Kbps takes ~64 seconds — fine. But the timeout
doesn't distinguish between "dead connection, zero progress" and "slow
connection, steady progress."

## Solution

Added **read-gap stall detection** to `transfer.Reader`. Two modes now:

| Mode | Use case | How it works |
|------|----------|-------------|
| Download (existing) | Network → Reader → our code | Goroutine-per-Read: fires if a single `Read()` blocks too long |
| Upload (new) | Memory → Reader → HTTP client → socket | Background monitor: fires if **consumer stops calling** `Read()` for too long |

### How upload stall detection works

1. `transfer.Reader` wraps the request body (`bytes.NewReader`)
2. `OnStall(cancel)` registers a `context.CancelFunc`
3. A background goroutine checks `lastRead` timestamp every `idleTimeout/4`
4. If no `Read()` call for 30 seconds → consumer is stalled
5. Calls `cancel()` → context cancellation closes the TCP socket
6. Blocked `write()` syscall returns with error
7. Semaphore slot freed, outbox worker retries on fresh connection

### Why this works for uploads

Go's `net/http` streams request bodies via `io.Copy`: read 32KB from body,
write 32KB to socket, repeat. When the socket write blocks (dead connection,
send buffer full), the HTTP client stops calling `Read()`. The gap between
Read() calls is the stall signal.

For small payloads (< TCP send buffer ~256KB), the body fits in one Read and
the write goes into the send buffer instantly. `ResponseHeaderTimeout` (15s)
covers the response phase.

### API

```go
// Upload mode: detect consumer stalls
ctx, cancel := context.WithCancel(ctx)
tr := transfer.NewReader(body, size)
tr.SetIdleTimeout(30 * time.Second)
tr.OnStall(cancel)    // fires cancel when consumer stops reading
defer tr.Close()       // stops monitor goroutine

// Download mode (unchanged): detect source stalls
tr := transfer.NewReader(networkBody, size)
tr.SetIdleTimeout(30 * time.Second)
// no OnStall — uses goroutine-per-Read timeout
```

## Files Changed

- `pkg/transfer/transfer.go` — `OnStall()`, `Stalled()`, `Close()`,
  `monitorStall()`, `signalDone()` added to Reader
- `pkg/transfer/transfer_test.go` — 3 new tests: stall detection, no false
  positive on EOF, monitor cleanup on Close
- `pkg/adapter/s3/s3.go` — `Backend.Put()` wraps body with transfer.Reader +
  30s stall detection; reports `ErrIdleTimeout` instead of `context.Canceled`

## Key Insight

TCP keepalive ≠ dead connection detection for active writes. Keepalive probes
are only sent when the connection is **idle**. During active writes to a dead
socket, only TCP retransmission timeout applies (15-30 min). The only way to
interrupt a blocked `write()` syscall in Go is closing the socket via context
cancellation.
