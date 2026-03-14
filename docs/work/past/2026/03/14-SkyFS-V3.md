---
created: 2026-03-14
model: claude-opus-4-6
---

# SkyFS V3 — Continuous Sync

## Problems Solved

### Milestone 1: Sync Engine Core
- Bidirectional diff algorithm: local vs remote → upload/download/skip
- ScanDirectory: recursive filesystem walk with streaming SHA-256 checksums
- DiffLocalRemote: computes transfer list from local files and manifest tree
- Synced-file tracking: skip unchanged files between sync cycles (no re-upload)
- DownloadAll / UploadAll for initial sync scenarios

### Milestone 2: File Watcher
- fsnotify-based recursive directory watcher
- 500ms debounce window coalesces rapid writes (editor save = truncate + write)
- Auto-watch new subdirectories as they're created
- Skip dotfiles and directories starting with "."
- Buffered event channel (100 events) prevents blocking

### Milestone 3: Remote Poller
- Polls S3 ops/ prefix for new operations since last known timestamp
- Incremental: tracks last_op_timestamp in local SQLite index
- Start() runs as background goroutine, respects context cancellation
- Configurable poll interval (default 30s)

### Milestone 4: Daemon Mode
- Combines sync engine + file watcher + remote poller
- Initial full sync on startup
- Local changes: watcher → 2-second batch window → upload
- Remote changes: poller → download changed files
- Dedup: same file in multiple events processed once
- Graceful shutdown: flush pending changes on context cancellation

### Milestone 5: Selective Sync
- Filter by namespace: `--namespace journal` syncs only journal files
- Filter by prefix: `--prefix docs/` syncs only docs/ subtree
- Exclude patterns: `ExcludePrefixes` in SyncConfig
- Filters applied to remote state before diffing — files outside scope never downloaded

### Milestone 6: Compression
- zstd per-chunk before encryption (klauspost/compress)
- Header byte: 0x00 = uncompressed, 0x01 = zstd
- Incompressible format detection: JPEG, PNG, ZIP, GIF, GZIP, MP4, WEBP, Zstd
- Only compresses if result is actually smaller (no size inflation)
- Backward compatible: data without header byte treated as legacy uncompressed

### Milestone 7: Versioning
- ListVersions: scan ops log for all put ops touching a path
- RestoreVersion: download historical version by timestamp
- ListSnapshots: list compacted manifest snapshots with file count and size
- Versions sorted most-recent-first

### Milestone 8: Progress Tracking
- ProgressReader: wraps io.Reader, calls callback with bytes transferred
- ProgressWriter: wraps io.Writer, same pattern
- Ready for CLI progress bars (caller provides the callback)

### Milestone 9: Ignore Patterns
- .skyfsignore file with gitignore-style syntax
- Default ignores: .git, .DS_Store, Thumbs.db, *.swp, *~, *.tmp
- Negation patterns: !important.log overrides *.log
- Directory patterns: build/ matches the directory
- IgnoreMatcher checks full path, basename, and path components

### Milestone 10: CLI Commands
- `skyfs sync <dir> [--once] [--namespace ns] [--prefix p]`
- `skyfs versions <path>` — file history from ops log
- `skyfs snapshots` — list compacted manifests

## Decisions Made

- **Synced-file tracking via in-memory map** — the sync engine remembers which local checksums have been uploaded. Prevents re-uploading unchanged files. Resets on restart (full diff on first sync after restart).
- **Debounce at 500ms** — editor saves often produce multiple events (truncate + write). 500ms catches most patterns without feeling laggy.
- **Batch window 2 seconds** — daemon batches local changes for 2s before syncing to avoid thrashing on rapid edits.
- **Compression header byte** — first byte of stored chunk indicates compression. Legacy data (no header) passes through unchanged. Future-proof for other algorithms.
- **zstd level default (3)** — balance between speed and ratio. Configurable later if needed.

## Files Created

```
skyfs/sync.go, sync_test.go         sync engine + bidirectional diff
skyfs/diff.go                       diff computation
skyfs/scan.go                       directory scanning with checksums
skyfs/watcher.go                    fsnotify file watcher with debounce
skyfs/poller.go                     S3 ops/ remote poller
skyfs/daemon.go                     continuous sync daemon
skyfs/compress.go, compress_test.go  zstd compression + format detection
skyfs/version.go, version_test.go    file versioning + snapshots
skyfs/progress.go, progress_test.go  progress tracking for transfers
skyfs/ignore.go, ignore_test.go      .skyfsignore pattern matching
cmd/skyfs/main.go                    sync, versions, snapshots commands
```

## Dependencies Added

```
github.com/fsnotify/fsnotify        filesystem watcher
github.com/klauspost/compress/zstd   per-chunk compression
```

## Test Count

124 tests total (up from 96 in v2). All passing.
