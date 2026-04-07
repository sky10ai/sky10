---
created: 2026-04-07
updated: 2026-04-07
---

# FS Sync Experience Improvements

Tracking sky10 improvements to file-sync UX, transfer behavior, and
sync observability.

This roadmap is intentionally framed around sky10's own needs and
current implementation. It does not depend on any external product or
design source.

## Goals

- Make uploads and downloads feel visible, controllable, and reliable
- Reduce confusing states during browser-originated file operations
- Surface sync phases clearly in the UI and RPC layer
- Improve refresh behavior so file views converge faster after changes
- Preserve the current sync architecture: local CRDT, snapshot exchange,
  baseline diffing, and conflict copies

## Current Gaps

- Browser uploads are blind multipart posts in
  [`web/src/pages/FileBrowser.tsx`](../../../web/src/pages/FileBrowser.tsx)
  and [`pkg/fs/rpc_http.go`](../../../pkg/fs/rpc_http.go)
- Browser uploads do not expose byte-level progress, cancel, or retry
- HTTP uploads write directly to the final watched path instead of a temp
  path plus atomic rename
- The UI mostly shows coarse states like `Live`, queue length, and a raw
  event stream in [`web/src/pages/Drives.tsx`](../../../web/src/pages/Drives.tsx)
  and [`web/src/pages/Activity.tsx`](../../../web/src/pages/Activity.tsx)
- sky10 has transfer progress primitives in
  [`pkg/transfer/transfer.go`](../../../pkg/transfer/transfer.go), but
  that data is not surfaced cleanly to the UI
- The outbox drains serially in
  [`pkg/fs/outbox_worker.go`](../../../pkg/fs/outbox_worker.go), which is
  simple and safe but can make one large upload dominate perceived sync time

## Ranked Improvements

### 1. Browser Upload Progress And Cancel

**Why first:** highest user-visible payoff with limited sync risk.

- Add byte-level upload progress to the file browser
- Add user cancel for active browser uploads
- Show per-file upload state instead of a generic `Uploading...` banner
- Return structured upload errors so the UI can render useful failure states

### 2. Atomic Browser Upload Writes

**Why second:** prevents confusing partial-file states and aligns browser
uploads with reconciler download behavior.

- Write browser-uploaded files to a temp location first
- Rename atomically into the drive after the write completes
- Avoid exposing partial files at watched paths during in-flight uploads
- Reuse the same safety standard already used by the reconciler

### 3. Richer Transfer Status In UI And RPC

**Why third:** users need to know whether sky10 is scanning, uploading,
downloading, reconciling, retrying, or blocked.

- Promote transfer phases to first-class UI state
- Expose statuses such as `scanning`, `uploading`, `downloading`,
  `reconciling`, `retrying`, `waiting`, and `conflict`
- Extend activity and drive summaries so they describe current work,
  not just pending counts
- Prefer concise structured status over raw event-log detail

### 4. First-Class Transfer Sessions

**Why fourth:** gives browser transfers durable identity and better
restart/cancel/retry semantics.

- Create a transfer-session model for browser-originated uploads
- Track `pending`, `uploading`, `completed`, `failed`, and `cancelled`
- Persist enough state to diagnose interrupted uploads
- Use this as the basis for explicit retry and cancellation controls

### 5. Faster File View Invalidation And Refresh

**Why fifth:** improves perceived responsiveness without changing sync
correctness.

- Invalidate file metadata views immediately on local or remote change
- Avoid waiting for periodic refresh when the daemon already knows state changed
- Prevent stale async rebuilds from repopulating old metadata
- Consider lightweight per-drive metadata snapshots or generation-based invalidation

### 6. Better Retry Semantics For User-Visible Failures

**Why sixth:** sky10 already retries internal work; the UI should make
that behavior legible.

- Surface `retrying` vs `failed` distinctly
- Expose a user-visible `retry now` action where it makes sense
- Show which file is failing and why
- Keep automatic retries for transient failures, but make them observable

### 7. Upload Scheduling And Bounded Concurrency

**Why later:** useful for throughput, but easier to get wrong than the
preceding items.

- Revisit strictly serial outbox upload draining
- Evaluate bounded concurrency for independent uploads
- Preserve upload-then-record ordering guarantees
- Keep correctness and debuggability ahead of peak throughput

### 8. Keep Derived Work Off The Sync Critical Path

**Why later:** important once sky10 adds richer search, preview, or
agent-facing indexing.

- Mark sync complete before secondary indexing or content analysis runs
- Do not let search, preview extraction, or transcription delay sync convergence
- Treat secondary work as asynchronous sidecars

## Recommended Sequence

1. Browser upload progress and cancel
2. Atomic browser upload writes
3. Richer transfer status in UI and RPC
4. First-class transfer sessions
5. Faster file view invalidation and refresh
6. Better retry semantics
7. Upload scheduling and bounded concurrency
8. Async sidecars for future indexing and derived content

## Non-Goals

- Do not replace the current snapshot-exchange sync model
- Do not weaken CRDT-based merge behavior or conflict-copy handling
- Do not rewrite sky10 around centralized authoritative metadata
- Do not add a broad compatibility API surface unless that becomes a
  product goal

## Existing Strengths To Preserve

- Local CRDT as the sync source of truth in
  [`pkg/fs/daemon_v2_5.go`](../../../pkg/fs/daemon_v2_5.go)
- Snapshot exchange and baseline diffing in
  [`pkg/fs/snapshot_poller.go`](../../../pkg/fs/snapshot_poller.go)
- Reconciler-based application of remote state in
  [`pkg/fs/reconciler.go`](../../../pkg/fs/reconciler.go)
- Upload-then-record behavior in
  [`pkg/fs/outbox_worker.go`](../../../pkg/fs/outbox_worker.go)
- Transfer progress and stall-detection primitives in
  [`pkg/transfer/transfer.go`](../../../pkg/transfer/transfer.go)
