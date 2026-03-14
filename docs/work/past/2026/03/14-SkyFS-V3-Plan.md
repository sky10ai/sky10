# skyfs v3 — Continuous Sync + Production Hardening

Status: **complete**
Completed: 2026-03-14
Created: 2026-03-14

## Goal

Make skyfs a real-time sync engine. V2 has the building blocks (ops log,
local index, conflict detection). V3 adds the daemon that watches a local
directory, auto-syncs changes, polls for remote changes, and keeps
everything in lockstep across devices — without user intervention.

After v3, a user runs `skyfs sync ~/Documents` once and forgets about it.
Files appear on all devices, encrypted, automatically.

## What v2 Has

- Ops log with multi-device concurrent writes
- FastCDC content-defined chunking
- Snapshot compaction
- Pack files with range reads
- Local SQLite index
- Key rotation + access control
- Blob garbage collection
- CLI: init, put, get, ls, rm, info, compact, gc

## What v3 Adds

1. **Sync engine** — bidirectional sync between local directory and S3
2. **File watcher** — fsnotify for instant detection of local changes
3. **Remote poller** — periodic S3 ops/ polling for remote changes
4. **Selective sync** — sync only specific namespaces or prefixes
5. **Compression** — zstd per-chunk before encryption
6. **Versioning** — point-in-time restore from ops log history
7. **Progress + status** — real-time sync status, transfer progress
8. **Daemon mode** — background process with graceful shutdown

## Out of Scope (v3)

- FUSE mount (v4 — different I/O model, needs dedicated caching layer)
- Relay push notifications (skylink — separate package)
- Native UI (skyshare — SwiftUI macOS app, separate repo)
- Agent hooks (skylink concern)

---

## Milestone 1: Sync Engine Core

The bidirectional sync algorithm. Given a local directory and remote state,
compute what needs to upload and what needs to download.

### Tasks

- [ ] `skyfs/sync.go` — sync engine:
  - [ ] `SyncEngine` struct:
    ```go
    type SyncEngine struct {
        store     *Store
        index     *Index
        localRoot string       // local directory to sync
        config    SyncConfig
    }
    ```
  - [ ] `SyncConfig`:
    - [ ] `LocalRoot string` — directory to sync
    - [ ] `PollInterval time.Duration` — remote poll frequency (default 30s)
    - [ ] `Namespaces []string` — selective sync (empty = all)
    - [ ] `Prefixes []string` — selective sync by path prefix
    - [ ] `ConflictStrategy string` — "lww", "keep-both", "manual"
  - [ ] `(*SyncEngine) SyncOnce(ctx) (*SyncResult, error)`:
    1. Scan local directory → compute checksums for changed files
    2. Load remote state (snapshot + ops)
    3. Diff local vs remote:
       - Local new/modified, not in remote → upload
       - Remote new/modified, not in local → download
       - Both modified → conflict (resolve per strategy)
       - Local deleted → write delete op
       - Remote deleted → delete local file
    4. Execute uploads and downloads
    5. Update local index
  - [ ] `SyncResult`:
    ```go
    type SyncResult struct {
        Uploaded   int
        Downloaded int
        Deleted    int
        Conflicts  int
        Errors     []error
    }
    ```
- [ ] `skyfs/diff.go` — diff computation:
  - [ ] `DiffLocalRemote(localFiles, remoteFiles) []DiffEntry`
  - [ ] `DiffEntry`: `{ Path, Type (upload/download/conflict/delete), LocalChecksum, RemoteChecksum }`
  - [ ] Compare using checksums — skip files with matching checksums (no re-upload)
- [ ] Local file scanning:
  - [ ] Walk directory tree
  - [ ] Compute SHA-256 checksum per file (streaming, not in-memory)
  - [ ] Compare with index's `local_files` table
  - [ ] Skip dotfiles and configurable ignore patterns
- [ ] Tests:
  - [ ] New local file → uploaded
  - [ ] New remote file → downloaded to local
  - [ ] Modified local file → re-uploaded (only changed chunks via CDC)
  - [ ] Modified remote file → local file updated
  - [ ] Deleted local file → delete op written
  - [ ] Deleted remote file → local file removed
  - [ ] Unchanged file → skipped (no transfer)
  - [ ] Conflict: both modified → resolved per strategy

### Acceptance

`SyncOnce` correctly syncs a directory bidirectionally. Unchanged files
skipped. Conflicts detected and resolved.

---

## Milestone 2: File Watcher

Detect local file changes in real time via filesystem events.

### Tasks

- [ ] Add `github.com/fsnotify/fsnotify` dependency
- [ ] `skyfs/watcher.go`:
  - [ ] `Watcher` struct wrapping fsnotify
  - [ ] `NewWatcher(root string, ignore []string) (*Watcher, error)`
  - [ ] `(*Watcher) Events() <-chan FileEvent`
  - [ ] `FileEvent`: `{ Path, Type (create/modify/delete/rename) }`
  - [ ] Recursive directory watching (fsnotify watches are per-directory)
  - [ ] Debounce: coalesce rapid events for the same file (editor save = truncate + write)
    - [ ] Wait 500ms after last event for a path before emitting
  - [ ] Ignore patterns:
    - [ ] Dotfiles (`.git/`, `.DS_Store`)
    - [ ] Temp files (`~`, `.swp`, `.tmp`)
    - [ ] Configurable via `.skyfsignore` (gitignore syntax)
  - [ ] `(*Watcher) Close() error`
- [ ] Handle platform specifics:
  - [ ] macOS: FSEvents (via fsnotify)
  - [ ] Linux: inotify (via fsnotify)
  - [ ] Watch new subdirectories as they're created
- [ ] Tests:
  - [ ] Create file → event emitted
  - [ ] Modify file → event emitted
  - [ ] Delete file → event emitted
  - [ ] Rapid writes debounced into single event
  - [ ] Dotfiles ignored
  - [ ] New subdirectory auto-watched

### Acceptance

File watcher detects local changes within 1 second. Debouncing prevents
duplicate syncs on save. Ignore patterns work.

---

## Milestone 3: Remote Poller

Poll S3 for new ops from other devices.

### Tasks

- [ ] `skyfs/poller.go`:
  - [ ] `Poller` struct:
    ```go
    type Poller struct {
        store    *Store
        index    *Index
        interval time.Duration
    }
    ```
  - [ ] `(*Poller) Start(ctx) error` — background goroutine, respects context cancellation
  - [ ] `(*Poller) PollOnce(ctx) ([]Op, error)`:
    1. Get `last_op_timestamp` from local index
    2. `ReadOps(ctx, backend, since, encKey)` — list new ops
    3. If new ops found:
       - Replay into local state
       - Update local index
       - Return ops for sync engine to process (download new/modified files)
    4. Update `last_op_timestamp` in index
  - [ ] `(*Poller) Stop()`
  - [ ] Exponential backoff on S3 errors (don't hammer a failing endpoint)
  - [ ] Cost tracking: count LIST requests per poll cycle
- [ ] Tests:
  - [ ] No new ops → no action
  - [ ] New ops detected → returned for processing
  - [ ] last_op_timestamp advances correctly
  - [ ] Context cancellation stops polling
  - [ ] S3 error → backoff, retry

### Acceptance

Poller detects remote changes within poll interval. Incremental — only
fetches ops since last check. Graceful shutdown via context.

---

## Milestone 4: Daemon Mode

Long-running background process that combines watcher + poller + sync engine.

### Tasks

- [ ] `skyfs/daemon.go`:
  - [ ] `Daemon` struct combining SyncEngine + Watcher + Poller
  - [ ] `NewDaemon(config DaemonConfig) (*Daemon, error)`
  - [ ] `(*Daemon) Run(ctx) error`:
    1. Initial full sync (SyncOnce)
    2. Start file watcher
    3. Start remote poller
    4. Event loop:
       - Local file event → queue for upload
       - Remote poll result → queue for download
       - Batch process queue every N seconds (avoid thrashing on rapid changes)
    5. Graceful shutdown on context cancellation
  - [ ] Upload queue with dedup (same file modified 5 times → upload once)
  - [ ] Download queue with priority (newest ops first)
  - [ ] Error handling:
    - [ ] S3 timeout → retry with backoff
    - [ ] File locked by another process → skip, retry next cycle
    - [ ] Disk full → log error, pause sync, alert user
- [ ] `skyfs sync <dir>` CLI command:
  - [ ] Foreground mode (default): run daemon, Ctrl+C to stop
  - [ ] `--daemon` flag: background mode (write PID file)
  - [ ] `--once` flag: sync once and exit
- [ ] PID file at `~/.skyfs/daemon.pid`
- [ ] `skyfs sync --stop` — signal daemon to stop
- [ ] Signal handling: SIGTERM/SIGINT → graceful shutdown (flush pending ops, close watcher)
- [ ] Tests:
  - [ ] Daemon starts and runs initial sync
  - [ ] Local file change → auto-uploaded within 2 seconds
  - [ ] Remote change → auto-downloaded within poll interval
  - [ ] Graceful shutdown: no partial uploads, no data loss
  - [ ] Multiple rapid local changes → batched into single sync

### Acceptance

`skyfs sync ~/Documents` runs continuously. Local changes appear remotely
within seconds. Remote changes appear locally within poll interval. Clean
shutdown on Ctrl+C.

---

## Milestone 5: Selective Sync

Sync only specific namespaces or path prefixes. Not every device needs
every file.

### Tasks

- [ ] `SyncConfig` extensions:
  - [ ] `Namespaces []string` — only sync these namespaces (empty = all)
  - [ ] `Prefixes []string` — only sync paths matching these prefixes
  - [ ] `ExcludePrefixes []string` — skip these paths
- [ ] Update `SyncOnce`:
  - [ ] Filter remote state by namespace/prefix before diffing
  - [ ] Only watch local subdirectories matching the filter
  - [ ] Don't delete local files outside the sync scope
- [ ] Update local index:
  - [ ] `sync_state` tracks which namespaces/prefixes are synced
  - [ ] `skyfs sync --namespace journal ~/Journal/` — sync only journal namespace
  - [ ] `skyfs sync --prefix docs/ ~/Docs/` — sync only docs/ prefix
- [ ] CLI flags:
  - [ ] `--namespace <ns>` (repeatable)
  - [ ] `--prefix <p>` (repeatable)
  - [ ] `--exclude <p>` (repeatable)
- [ ] Tests:
  - [ ] Namespace filter: only matching files synced
  - [ ] Prefix filter: only matching paths synced
  - [ ] Exclude: matching paths skipped
  - [ ] Remote file outside scope → not downloaded
  - [ ] Local file outside scope → not uploaded

### Acceptance

Selective sync works. Devices only download what they need. Mobile sync
with `--namespace journal` fetches only journal files.

---

## Milestone 6: Compression

Compress chunks before encryption. Saves storage and bandwidth for
compressible content (text, markdown, JSON, code).

### Tasks

- [ ] Evaluate `github.com/klauspost/compress/zstd` for per-chunk compression
- [ ] `skyfs/compress.go`:
  - [ ] `CompressChunk(data []byte) ([]byte, error)` — zstd level 3 (fast)
  - [ ] `DecompressChunk(data []byte) ([]byte, error)`
  - [ ] Content-type detection to skip incompressible formats:
    - [ ] Check first few bytes for magic numbers (JPEG, PNG, MP4, ZIP, etc.)
    - [ ] If incompressible → store uncompressed
  - [ ] Compression header: first byte indicates compression
    - [ ] `0x00` = uncompressed
    - [ ] `0x01` = zstd
    - [ ] Future-proof for other algorithms
- [ ] Update Store.Put pipeline:
  - [ ] After chunking, before encryption: `data = CompressChunk(data)`
  - [ ] Chunk hash computed on ORIGINAL data (before compression)
    — this preserves dedup across compressed/uncompressed versions
- [ ] Update Store.Get pipeline:
  - [ ] After decryption: `data = DecompressChunk(data)`
  - [ ] Verify hash against decompressed data
- [ ] Backward compatibility:
  - [ ] Chunks without compression header → treat as uncompressed (v1/v2 data)
  - [ ] No migration needed — new chunks compressed, old chunks stay as-is
- [ ] Compression stats in CLI:
  - [ ] `skyfs put` → show original size vs compressed size, ratio
- [ ] Tests:
  - [ ] Text file compressed < original size
  - [ ] JPEG not compressed (incompressible detection)
  - [ ] Round-trip: compress → encrypt → decrypt → decompress → matches original
  - [ ] v2 uncompressed chunks still readable (backward compat)
  - [ ] Dedup: same content → same hash regardless of compression

### Acceptance

Text files 50-70% smaller in storage. Incompressible files not bloated.
Backward compatible with v1/v2 data.

---

## Milestone 7: Versioning

Point-in-time restore from ops log history. The data is already there —
this is UI on top of existing infrastructure.

### Tasks

- [ ] `skyfs/version.go`:
  - [ ] `ListVersions(ctx, store, path) ([]Version, error)`:
    - [ ] Scan ops log for all ops touching this path
    - [ ] Return: `{ Timestamp, Device, Checksum, Size }`
  - [ ] `RestoreVersion(ctx, store, path, timestamp, w) error`:
    - [ ] Build state at the given timestamp
    - [ ] Find the file entry at that point
    - [ ] Download and decrypt the chunks from that version
  - [ ] `ListSnapshots(ctx, store) ([]Snapshot, error)`:
    - [ ] List all compacted snapshots
    - [ ] Return: `{ Timestamp, FileCount, TotalSize }`
  - [ ] `RestoreSnapshot(ctx, store, timestamp, localDir) error`:
    - [ ] Rebuild full state at snapshot timestamp
    - [ ] Download all files to local directory
- [ ] CLI commands:
  - [ ] `skyfs versions <path>` — list versions of a file
  - [ ] `skyfs restore <path> --at <timestamp> [--out <file>]` — restore a version
  - [ ] `skyfs snapshots` — list compacted snapshots
- [ ] Tests:
  - [ ] File modified 3 times → 3 versions listed
  - [ ] Restore version N → correct content returned
  - [ ] Snapshot list matches compaction history
  - [ ] Restore after delete → file recoverable from history

### Acceptance

`skyfs versions` shows file history. `skyfs restore` recovers any previous
version. Deleted files recoverable if ops/snapshots not yet cleaned up.

---

## Milestone 8: Progress + Status

Real-time feedback for sync operations and transfer progress.

### Tasks

- [ ] `skyfs/progress.go`:
  - [ ] `ProgressWriter` wrapping io.Writer:
    - [ ] Track bytes written, total expected, speed (bytes/sec)
    - [ ] Callback: `func(bytesWritten, totalBytes int64, speed float64)`
  - [ ] `ProgressReader` wrapping io.Reader (for uploads)
- [ ] Update Store.Put and Store.Get:
  - [ ] Accept optional `ProgressFunc` in options
  - [ ] Report per-chunk progress
- [ ] CLI progress output (stderr):
  - [ ] `skyfs put`: `uploading report.pdf  [=====>    ] 45% 2.3 MB/s`
  - [ ] `skyfs get`: `downloading report.pdf [========> ] 80% 5.1 MB/s`
  - [ ] `skyfs sync`: `syncing 3 files ↑2 ↓1 (1.2 MB remaining)`
  - [ ] Terminal width detection for progress bar sizing
  - [ ] `--quiet` flag to suppress
- [ ] `skyfs status` command:
  - [ ] Sync state: last sync time, pending ops count
  - [ ] Local changes not yet synced
  - [ ] Remote changes not yet downloaded
  - [ ] Active conflicts
  - [ ] Storage usage: total size, blob count, pack count
- [ ] Tests:
  - [ ] ProgressWriter reports correct byte counts
  - [ ] ProgressReader reports correct byte counts
  - [ ] Status command output format

### Acceptance

Large transfers show progress. `skyfs status` gives a clear picture of
sync state. `--quiet` suppresses output for scripts.

---

## Milestone 9: Ignore Patterns + Edge Cases

Handle real-world filesystem edge cases and user-configurable ignore rules.

### Tasks

- [ ] `.skyfsignore` file (gitignore syntax):
  - [ ] Loaded from sync root directory
  - [ ] Supports: `*.tmp`, `build/`, `!important.tmp` (negation)
  - [ ] Standard ignores always applied: `.git/`, `.DS_Store`, `Thumbs.db`, `*.swp`, `*~`
  - [ ] Use `github.com/sabhiram/go-gitignore` or implement minimal matcher
- [ ] Symlink handling:
  - [ ] Default: follow symlinks (sync the target, not the link)
  - [ ] `--no-follow-symlinks` flag to skip
  - [ ] Detect symlink loops → skip with warning
- [ ] File locking:
  - [ ] Detect files open for writing (platform-specific)
  - [ ] Skip locked files, retry next sync cycle
  - [ ] Log warning for repeatedly locked files
- [ ] Permissions:
  - [ ] Store file permissions in manifest metadata (mode bits)
  - [ ] Restore permissions on download
  - [ ] Default: preserve original permissions
- [ ] Large directory handling:
  - [ ] Directories with 100K+ files → incremental scanning, not full walk every time
  - [ ] Use index's `local_files` table as cache, only re-scan modified directories
- [ ] Tests:
  - [ ] `.skyfsignore` patterns respected
  - [ ] Symlink followed → target content synced
  - [ ] Symlink loop detected → skipped
  - [ ] File permissions preserved round-trip
  - [ ] Large directory: incremental scan faster than full walk

### Acceptance

Real-world filesystems handled cleanly. Ignore patterns work. No crashes
on edge cases (symlinks, permissions, locked files).

---

## Milestone 10: Hardening + Documentation

Final polish. Performance testing. Documentation.

### Tasks

- [ ] Performance testing:
  - [ ] Benchmark: initial sync of 10,000 files
  - [ ] Benchmark: incremental sync (1 file changed out of 10,000)
  - [ ] Benchmark: large file (1GB) upload/download speed
  - [ ] Memory profiling: verify 4MB ceiling under load
  - [ ] Concurrent device stress test: 3 devices writing simultaneously
- [ ] Error recovery:
  - [ ] Network disconnect during upload → resume on reconnect (orphaned chunks, GC cleans up)
  - [ ] Disk full → pause sync, alert, resume when space available
  - [ ] Corrupt local file → re-download from remote
  - [ ] `skyfs sync --rebuild` → recreate local state from remote
- [ ] Logging:
  - [ ] `log/slog` structured logging
  - [ ] Log levels: debug, info, warn, error
  - [ ] `--log-level` flag
  - [ ] Log file: `~/.skyfs/skyfs.log`
- [ ] Update README:
  - [ ] v3 commands: sync, versions, restore, snapshots, status
  - [ ] Daemon usage
  - [ ] Selective sync examples
  - [ ] .skyfsignore documentation
- [ ] `go vet ./...` clean, `gofmt` clean, all tests pass
- [ ] `make reproduce` still deterministic
- [ ] Tests:
  - [ ] End-to-end: init → put files → sync → modify on device B → poll → local updated
  - [ ] Stress: 100 files modified rapidly → all synced correctly
  - [ ] Recovery: kill daemon mid-sync → restart → no data loss

### Acceptance

Production-ready sync daemon. Handles real-world edge cases. Documented.
Performance acceptable for 10K+ file repositories.

---

## Dependency Additions (v3)

```
new:
  github.com/fsnotify/fsnotify           filesystem watcher
  github.com/klauspost/compress/zstd      per-chunk compression
  github.com/sabhiram/go-gitignore        .skyfsignore pattern matching (evaluate)

unchanged from v2:
  github.com/aws/aws-sdk-go-v2            S3 client
  github.com/jotfs/fastcdc-go             content-defined chunking
  filippo.io/edwards25519                 Ed25519 → X25519
  modernc.org/sqlite                      local index
  golang.org/x/crypto/hkdf                key derivation
  stdlib for everything else
```

---

## Order of Implementation

```
1. Sync engine core    the algorithm — diff, upload, download
2. File watcher        fsnotify for local changes
3. Remote poller       S3 ops/ polling for remote changes
├── 1-3 are sequential, each builds on the last
4. Daemon mode         combines 1-3 into long-running process
5. Selective sync      namespace/prefix filters
├── 4-5 can overlap
6. Compression         zstd per-chunk, independent
7. Versioning          UI on existing ops log data
├── 6-7 independent of 4-5
8. Progress + status   CLI polish
9. Ignore patterns     real-world edge cases
10. Hardening          performance, recovery, docs
├── 8-10 are polish, order flexible
```

Milestones 1-4 are the critical path for a working sync daemon.
Milestones 5-7 add power features. Milestones 8-10 are production polish.

---

## V4 Thoughts

- **FUSE mount** — `skyfs mount ~/Sky`. Read-on-demand, write-through.
  Needs a local cache layer with LRU eviction. Different I/O model from
  sync (random access vs sequential). Go FUSE: `hanwen/go-fuse`.

- **Relay push** — skylink WebSocket notifications to replace S3 polling.
  Sub-second sync latency. Fallback to polling when relay is down.

- **Native macOS app** — SwiftUI desktop app (skyshare). Tray icon, sync
  status, sharing UI. Go backend as sidecar process via XPC or stdin/stdout.
  Separate repo.

- **iOS app** — SwiftUI, shares core UI with macOS. Selective sync only.
  Decrypts locally on device.

- **Multi-region replication** — replicate ops to multiple S3 buckets.
  Disaster recovery. Each region replays independently.

- **Bandwidth throttling** — token bucket rate limiter on uploads/downloads.
  `--max-upload 5MB/s` for metered connections.

- **P2P sync** — direct device-to-device transfer without S3 for LAN.
  Use mDNS for discovery. Encrypted tunnel between devices.

- **Agent event stream** — expose ops log as a subscription for skylink
  agents. Agent reads new ops, processes files, writes results back.
