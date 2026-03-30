---
created: 2026-03-29
model: claude-opus-4-6
status: proposed
---

# Implementation Plan: Snapshot Exchange Architecture

## Context

Every major sync bug traces to the S3 ops log: compaction losing ops, entries
before blobs exist, cursor drift. We're replacing it with per-device snapshot
exchange. S3 becomes blobs + snapshots only. The bucket is already wiped clean.

Full architecture doc: `docs/work/current/snapshot-exchange-architecture.md`

## Phase 1: Upload-Then-Record

**Goal:** Entries written to ops.jsonl only AFTER blob upload succeeds.

### `pkg/fs/watcher_handler.go`
- **Remove** `localLog.AppendLocal()` calls at L86-92, L117-121, L150-156, L179-183, L246-250
- Keep only `outbox.Append()` calls (already capture timestamp)
- Keep dedup check at L64 (`localLog.Lookup`) — reads CRDT for already-confirmed uploads
- For deletes/delete_dir: outbox only. OutboxWorker writes local log after confirming.

### `pkg/fs/outbox_worker.go`
- `uploadFile()` (L132): Already uploads then writes local log. Change L166-174 to ALWAYS write (not conditional on `result != nil`). Use outbox entry's `Timestamp` field, not `time.Now()`.
- Metadata ops (L182-249): Stop calling `store.writeOp()`. Instead, write to `localLog.AppendLocal()` with the outbox entry's timestamp. These have no blob — just local log + done.
- Add `AppendWithTimestamp` to LocalOpsLog or pass timestamp through Entry struct.

### `pkg/fs/daemon_v2_5.go` — `seedStateFromDisk()`
- New/modified files (L287-305): Write to outbox ONLY (not localLog). OutboxWorker handles local log after upload.
- Local deletes (L316-329): Write directly to localLog (no blob needed).
- Symlinks (L256-272): Outbox only.
- Empty dirs (L345-357): Outbox only.

### Tests
- `TestUploadThenRecord_NoCRDTBeforeUpload` — outbox entry exists, local log empty until drain
- `TestUploadThenRecord_TimestampPreserved` — entry timestamp matches event time, not upload time
- `TestUploadThenRecord_FailedUploadNoEntry` — S3 failure leaves local log clean
- Update `daemon_v2_5_test.go` tests that check local log after seed

---

## Phase 2: Kill S3 Ops Log

**Goal:** Remove all S3 op writing. Store becomes a pure blob uploader.

### `pkg/fs/skyfs.go`
- **Delete** `writeOp()` (L268-285)
- **Delete** `getOpsLog()` (L123-143) and `opsLog` field from Store struct
- **Simplify** `Put()` (L290): Remove the `writeOp()` call. Put just uploads chunks and returns metadata via `LastPutResult()`.
- **Delete** `loadCurrentState()` (L239-265) — loads from S3 ops, no longer needed
- **Delete** `Remove()`, `List()`, `Get()`, `Info()`, `SaveSnapshot()` — all depend on S3 ops. Keep `GetChunks()` (blob downloader for reconciler).

### `pkg/fs/opslog/opslog.go`
- **Delete** S3-facing methods: `Append()`, `ReadSince()`, `ReadBatched()`, `Compact()`, `saveSnapshot()`, `LoadLatestSnapshot()`, `writeEntry()`, `readEntries()`
- **Keep**: `Snapshot` struct, `FileInfo`, `DirInfo`, `Entry`, `EntryType`, `clockTuple`, `buildSnapshot()` — used by LocalOpsLog

### `pkg/fs/opslog/local.go`
- **Delete** `CatchUpFromSnapshot()` (L145-225)
- **Delete** `LastRemoteOp()` (L116) and `SetLastRemoteOp()` (L128) — no cursors
- **Keep** everything else: `Append`, `AppendLocal`, `Snapshot`, `Lookup`, `Compact` (local optimization)

### Delete files
- `pkg/fs/compact.go` — no compaction
- `pkg/fs/poller_v2.go` — replaced in Phase 4
- `pkg/fs/manifest.go` — if unused after removing SaveSnapshot

### `pkg/fs/outbox_worker.go`
- Metadata ops: already changed in Phase 1 to write local log only. Remove any remaining `store.writeOp()` references.

### Tests
- Delete: `compact_test.go`, `integration_compact_test.go`, poller tests
- Delete: tests for `CatchUpFromSnapshot`, `LastRemoteOp`
- Update: `daemon_v2_5_test.go` — remove `catchUpFromSnapshot` references
- Temporarily skip multi-device integration tests (restored in Phase 4)

---

## Phase 3: Snapshot Uploader

**Goal:** After outbox drains, upload local CRDT snapshot to S3.

### New file: `pkg/fs/snapshot_uploader.go`
```go
type SnapshotUploader struct {
    backend   adapter.Backend
    localLog  *opslog.LocalOpsLog
    deviceID  string
    nsID      string
    encKey    []byte
    logger    *slog.Logger
    notify    chan struct{}
}
```

- `Poke()` — signal state changed
- `Run(ctx)` — debounced loop (wait for poke, 1-2s debounce, upload)
- `Upload(ctx)` — serialize `localLog.Snapshot()`, encrypt with namespace key, upload to:
  - `fs/{nsID}/snapshots/{deviceID}/latest.enc`
  - `fs/{nsID}/snapshots/{deviceID}/{timestamp}.enc` (history)
- Snapshot format: reuse existing `manifestJSON` serialization from `opslog.go`
- Only live files in the uploaded snapshot (no tombstones/deleted map)

### New helpers: `pkg/fs/s3paths.go`
- `snapshotLatestKey(nsID, deviceID) string`
- `snapshotHistoryKey(nsID, deviceID string, ts int64) string`
- `namespacedBlobKey(nsID, hash string) string`
- `namespacedPackKey(nsID string, seq int) string`

### `pkg/fs/daemon_v2_5.go`
- Add `snapshotUploader` to daemon struct
- Wire `state.changed` events to `snapshotUploader.Poke()`
- Start uploader in `Run()`
- Upload snapshot at end of `SyncOnce()`

### Tests
- `TestSnapshotUploader_RoundTrip` — upload, download, decrypt, verify contents match
- `TestSnapshotUploader_History` — multiple uploads produce distinct history files
- `TestSnapshotUploader_Debounce` — rapid pokes produce single upload
- `TestSnapshotUploader_OnlyLiveFiles` — deleted files not in uploaded snapshot

---

## Phase 4: Snapshot Poller + Baseline Diffing

**Goal:** Replace PollerV2. Download remote snapshots, baseline diff, merge into local CRDT.

### New file: `pkg/fs/baseline.go`
```go
type BaselineStore struct {
    dir string  // ~/.sky10/fs/drives/{driveID}/baselines/
}
```
- `Load(deviceID) (*opslog.Snapshot, error)` — load stored baseline
- `Save(deviceID, snap) error` — save new baseline
- Baselines stored as decrypted JSON on local disk

### New file: `pkg/fs/snapshot_poller.go`
```go
type SnapshotPoller struct {
    backend        adapter.Backend
    localLog       *opslog.LocalOpsLog
    deviceID       string
    nsID           string
    encKey         []byte
    interval       time.Duration
    baselines      *BaselineStore
    logger         *slog.Logger
    pokeReconciler func()
    pokeUploader   func()
}
```

- `Run(ctx)` — poll loop on interval
- `pollOnce(ctx)`:
  1. List devices from `devices/` registry
  2. For each remote device: download `fs/{nsID}/snapshots/{deviceID}/latest.enc`
  3. Decrypt, load baseline, diff:
     - In latest not in baseline → remote add/modify → `localLog.Append(entry)` with LWW
     - In baseline not in latest → remote delete → `localLog.Append(Delete entry)`
     - Both modified locally and remotely → conflict → LWW winner + conflict copy
  4. Save latest as new baseline
  5. Poke reconciler + uploader if changes merged

### `pkg/fs/daemon_v2_5.go` — new startup sequence
```
1. Load local CRDT (ops.jsonl)
2. Seed from disk (diff against LOCAL CRDT, before merge)
3. Snapshot poller: pollOnce (download + baseline diff + merge)
4. Reconciler: reconcile (download new files, delete removed)
5. Snapshot uploader: upload (publish our state)
6. Start steady-state loops
```

### `pkg/fs/reconciler.go`
- **Delete** `integritySweep()` — no chunkless puts possible
- Simplify `reconcile()`: remove sweep timer, remove chunkless-put handling
- Download logic stays the same (diff CRDT snapshot vs disk)

### Integration tests
- `TestEndToEnd_TwoDeviceSync` — A creates file, uploads snapshot. B polls, downloads, verifies.
- `TestEndToEnd_DeletePropagation` — A deletes, B baseline-diffs, removes.
- `TestEndToEnd_ConcurrentEdit` — both modify, LWW resolves, conflict copy created.
- `TestEndToEnd_NewDeviceJoin` — C joins empty, gets everything.
- `TestEndToEnd_OfflineDevice` — B offline, A changes, B catches up.

---

## Phase 5: Per-Namespace Paths + Cleanup

**Goal:** Move blobs under `fs/{nsID}/`, add per-domain schemas, GC, cleanup dead code.

### `pkg/fs/chunk.go`
- Add namespace prefix support to `BlobKey()` (or add store-level `BlobPrefix`)

### `pkg/fs/pack.go`
- Same namespace prefix for pack paths

### `pkg/fs/schema.go`
- Write per-domain schema at `fs/{nsID}/schema` instead of `sky10.schema`

### New: `pkg/fs/gc.go`
- List all snapshots (current + historical) in namespace
- Delete historical snapshots older than retention (default 30 days)
- Collect referenced blob checksums from remaining snapshots
- Delete unreferenced blobs

### New: Device removal
- RPC `skyfs.removeDevice` — deletes `devices/{id}.json` and `fs/{nsID}/snapshots/{id}/`

### Dead code removal
- Delete `pkg/fs/op.go` S3 functions (WriteOp, ReadOps, etc.)
- Delete `pkg/fs/manifest.go` if unused
- Delete `pkg/fs/version.go` old functions, rewrite for snapshot history
- Clean up unused imports, types, test helpers

---

## Execution Order

```
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5
```

Strictly sequential. Each phase compiles and passes tests before moving on.
Phase 1 is the highest-value change (eliminates phantom entries).
Phase 4 is the hardest (full sync protocol change).
Phase 5 is cleanup and polish.

## Key Risks

1. **Watcher dedup after Phase 1**: File created → outbox entry → watcher fires
   again before upload. Dedup check (`localLog.Lookup`) won't find it (not in
   local log yet). Duplicate outbox entry. Safe but wasteful — outbox worker
   handles it idempotently.

2. **Timestamp preservation**: OutboxWorker MUST use the outbox entry's
   timestamp, not `time.Now()`, when writing to local log. Critical for LWW
   correctness.

3. **Snapshot size**: 16K files = ~2-3MB per snapshot. Acceptable. Upload only
   on change.

## Verification

After each phase:
- `gofmt -w` on changed files
- `go vet ./...`
- `go test ./... -count=1`
- After Phase 4: manual two-device sync test with MinIO
