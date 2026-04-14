---
created: 2026-04-14
updated: 2026-04-14
model: gpt-5.4
---

# FS Hidden Transfer Workspace

This document is the implementation draft for Milestone 1 in
[`fs-p2p-core-and-agent-drives-plan.md`](./fs-p2p-core-and-agent-drives-plan.md).
It defines the per-drive hidden workspace that should sit between remote
transfer sources and the user-visible working tree.

## Why This Exists

The current code already has one good instinct: remote downloads are staged to
a temp file before being renamed into place. The problem is that the staging
area is still too weak and too global.

Current behavior:

- reconciler downloads into a process-wide temp dir under `/tmp/sky10/reconcile`
- browser uploads in [`pkg/fs/rpc_http.go`](../../../pkg/fs/rpc_http.go) write
  directly into the watched tree
- transfer state is not modeled as durable per-drive sessions
- there is no reusable per-drive object cache or retained verified content

That means we still mix too much of "user-visible filesystem state" with
"transfer-in-progress state", especially for browser uploads and future
peer-assisted pulls.

## Goals

The hidden transfer workspace should:

- keep transfer mechanics outside the watched root
- give remote downloads a durable, restart-safe place to land
- let browser uploads and other non-watcher writes use the same publish barrier
- make future peer-serving and chunk reuse possible
- stay compatible with `p2p-only`, `p2p+s3`, and local-only flows

## Non-Goals

This is not a permanent second plaintext mirror of the whole drive.

The workspace exists to stage, verify, resume, retain useful content, and
publish safely. Cached objects may be garbage-collected. The working tree
remains the user-visible state, not a copy of the hidden store.

## Recommended Layout

The workspace should live under the existing per-drive data dir returned by
`driveDataDir(driveID)`.

Recommended layout:

```text
<drive-data>/
  ops.jsonl
  outbox.jsonl
  transfer/
    staging/
    objects/
    sessions/
```

### `staging/`

Holds in-progress file materialization:

- remote downloads
- browser-upload temp files
- future peer-to-peer pull assembly
- publish candidates awaiting atomic rename

### `objects/`

Holds verified retained content:

- fully assembled verified files kept for reuse
- future chunk or object cache for peer serving and download reuse

This is a cache with durability value, not semantic truth.

### `sessions/`

Holds durable transfer bookkeeping:

- active transfer metadata
- resume markers
- source choice and retry metadata
- publish state if a restart happens between verification and rename

## Core Rules

1. **Nothing remote lands directly in the watched tree**
   Remote peer fetches and S3 recovery must always stage first.
2. **Externally initiated local writes should use the same barrier**
   Browser uploads and similar daemon-mediated writes should not stream
   directly into the watched tree.
3. **Verification before publish**
   A file becomes visible only after bytes are complete and verified.
4. **Publish is the only visible-tree write step**
   The working tree should only see final directory creation, atomic rename, or
   equivalent finalization.
5. **Workspace is per-drive**
   No global `/tmp/sky10/reconcile` staging area shared across drives.

## Recommended Flows

### Remote Download Flow

1. Reconciler or future pull planner creates a session record in
   `transfer/sessions/`.
2. Bytes download into `transfer/staging/`.
3. Content is verified against expected checksum or chunk/object identity.
4. If retained, content may be copied or promoted into `transfer/objects/`.
5. Final publish moves the verified file into the working tree.
6. Session state is marked complete and eventually garbage-collected.

### Browser Upload Flow

Today, browser uploads write straight into the drive root. That should change.

Recommended flow:

1. `rpc_http.go` receives multipart upload.
2. The upload streams into `transfer/staging/`.
3. The daemon finalizes the temp file.
4. The daemon publishes into the working tree by rename.
5. The watcher sees the final published file as a normal local change.

This keeps the watcher from observing a partially written browser upload.

### Local On-Disk Edits

Edits made directly by the user or tools in the working tree stay on the
current watcher-plus-scan path. They do not need the transfer workspace first.

The workspace is for daemon-mediated transfer and publish, not for all local
writes.

## What Should Move First

The first implementation slice should be intentionally small.

### Step 1: Per-Drive Staging Path

Replace the hardcoded `/tmp/sky10/reconcile` temp directory in
[`pkg/fs/reconciler.go`](../../../pkg/fs/reconciler.go) with a per-drive
staging path under `driveDataDir(driveID)`.

Immediate benefit:

- remote downloads stop relying on global temp state
- restarts and multi-drive behavior become easier to reason about
- Windows readiness improves because we stop assuming `/tmp`

### Step 2: HTTP Upload Publish Barrier

Update [`pkg/fs/rpc_http.go`](../../../pkg/fs/rpc_http.go) so uploads stage
under the same per-drive workspace before publishing into the working tree.

Immediate benefit:

- watcher never sees partial upload writes initiated by the daemon

### Step 3: Durable Session Records

Add simple per-file session metadata in `transfer/sessions/` before designing a
full DB-backed transfer planner.

Immediate benefit:

- restart can tell whether a staged file is orphaned, resumable, or ready to
  republish

## Interaction With Watcher Logic

Because the workspace lives outside the watched root, the watcher should not
need to observe or ignore it during normal operation.

That is preferable to adding more ignore patterns for temp names inside the
working tree.

Watcher responsibility should remain:

- observe user and tool edits in the working tree
- detect final published files as local state when the daemon intentionally
  creates them there

Watcher responsibility should not become:

- understanding transfer sessions
- distinguishing partial temp files from final content
- repairing daemon-owned temporary artifacts

## Interaction With Outbox

The outbox invariant should remain:

- prepare bytes first
- publish metadata after required local state exists

In the current S3-backed path, that already means "upload-then-record".

In the future peer-native path, the equivalent should be:

- stage and verify bytes locally
- make them servable from hidden local storage if needed
- then publish metadata to peers

The invariant changes shape by backend, but not in meaning.

## Interaction With Object Retention

The workspace should not force a full duplicate plaintext mirror.

Recommended retention policy:

- keep staged files only until publish or explicit recovery need ends
- retain verified objects opportunistically for reuse and peer serving
- garbage-collect retained objects when space pressure or age policy says so

The exact retention policy can come later. The important part is creating the
structural separation now.

## Crash Recovery Rules

On startup, the daemon should inspect `transfer/sessions/` and `transfer/staging/`.

Expected outcomes:

- if a session is incomplete and resumable, resume it or requeue it
- if a staged file is complete but unpublished, attempt publish again
- if a staged file has no valid session, clean it up conservatively
- if retained objects exist without active sessions, keep or GC them according
  to cache policy

The daemon should never treat orphaned staging bytes as user-visible drive
content.

## Windows Notes

This change is important for Windows readiness:

- stop assuming `/tmp`
- prefer per-drive workspace paths derived from config-managed storage roots
- atomic rename works best when staging and publish target are on the same
  volume

Per-drive staging under `driveDataDir` is safer than cross-volume temp files.

## Immediate Repo Touchpoints

- [`../../../pkg/fs/reconciler.go`](../../../pkg/fs/reconciler.go)
  Replace global temp staging with per-drive staging.
- [`../../../pkg/fs/daemon_v2_5.go`](../../../pkg/fs/daemon_v2_5.go)
  Create and wire transfer workspace paths during drive startup.
- [`../../../pkg/fs/rpc_http.go`](../../../pkg/fs/rpc_http.go)
  Stage browser uploads before publish.
- [`../../../pkg/fs/rpc_sync.go`](../../../pkg/fs/rpc_sync.go)
  Extend sync activity and health views to include staged or active transfer
  sessions.

## Suggested Test Additions

- reconciler download stages under per-drive workspace, not `/tmp`
- browser upload never leaves partial visible files in the working tree
- restart with staged unpublished file republishes or cleans up deterministically
- multiple drives do not collide in transfer temp paths

## Acceptance For This Milestone

Milestone 1 is complete when:

- remote downloads no longer use a global temp dir
- daemon-mediated uploads use the same publish barrier as remote downloads
- partial daemon-owned transfer files never appear in the working tree
- startup can explain whether leftover staged data is resumable, publishable, or
  garbage
