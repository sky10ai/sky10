---
created: 2026-03-26
model: claude-opus-4-6
---

# Chunkless Ops Architecture

## Problem

After deploying v0.17.2 (integrity sweep fix), 101 files were stuck in an
infinite 2-second retry loop on the receiving machine. The reconciler
treated chunkless remote Put entries as download failures, triggering
`Poke()` every 2 seconds forever.

Root cause: the system conflates two concerns in a single Put operation:

1. **Tracking** -- "this file exists locally, upload is pending" (local)
2. **Replication** -- "this file is available, here are its chunks" (network)

A chunkless Put is tracking state. It records that a file was detected by
the watcher (`AppendLocal`) but the outbox worker hasn't uploaded it yet.
This entry leaked to S3 through the normal replication path, and compaction
baked it permanently into snapshots.

## How chunkless ops leak to S3

1. Watcher detects file -> `AppendLocal` writes Put with `chunks=nil`
2. Outbox worker uploads -> `store.Put()` writes a **new** op to S3 with
   chunks populated
3. Outbox worker confirms -> writes chunks back to local log

The S3 op in step 2 always has chunks. But the local log entry from step 1
can propagate to S3 via compaction: `LocalOpsLog.Compact()` serializes the
in-memory snapshot, which includes chunkless entries. When the S3
`OpsLog.Compact()` builds a snapshot, it faithfully preserves whatever is
in the snapshot -- including chunkless Puts that arrived before the chunked
version.

## Steelmanning chunkless ops on S3

Three arguments for keeping chunkless ops in S3:

### 1. Ownership reservation
"I claimed this path, don't overwrite." If two machines create the same
file simultaneously, the chunkless entry establishes causal priority.

**Counterargument**: Without a TTL, a crashed machine reserves paths
forever. And the CRDT already resolves conflicts via LWW -- a later Put
with chunks from the same device supersedes the chunkless one anyway.

### 2. CRDT causal ordering
Preserves the full edit history for complete state reconstruction.

**Counterargument**: The CRDT only cares about the *winning* entry per
path. A chunkless Put followed by a chunked Put for the same path is
superseded. No other device can act on a chunkless entry, so it carries
zero replication value.

### 3. Cross-machine crash recovery
If the source dies, the chunkless op tells others the file *should* exist.

**Counterargument**: Without chunks, nobody can reconstruct it. The op is
a promise that can never be fulfilled. When the source machine recovers,
the integrity sweep detects the chunkless entry and re-queues the upload.

**Conclusion**: None of these arguments hold. Chunkless entries are local
tracking state that should never leave the device.

## Solution

Three-layer defense:

### 1. Reconciler: skip chunkless remote entries (most impactful)

Filter chunkless Puts out of download targets *before* they reach
`downloadFile()`. They are never counted as failures, so `failed==0` and
no retry poke fires. Symlinks (legitimately chunkless) are excluded from
the filter.

### 2. S3 compaction: strip chunkless Puts from snapshots

`saveSnapshot()` now skips entries where `len(chunks)==0 && linkTarget==""`.
This prevents broken state from being baked into S3 snapshots permanently.

### 3. Local compaction: strip chunkless Puts from compacted file

`LocalOpsLog.Compact()` applies the same filter. The integrity sweep will
re-queue the files from disk if they still need uploading, so no data is
lost.

## Files changed

- `pkg/fs/reconciler.go` -- Skip chunkless remote Puts in reconcile loop
- `pkg/fs/opslog/opslog.go` -- Strip chunkless Puts in `saveSnapshot()`
- `pkg/fs/opslog/local.go` -- Strip chunkless Puts in `Compact()`
- `pkg/fs/reconciler_test.go` -- Two regression tests for reconciler
- `pkg/fs/opslog/compact_test.go` -- Two regression tests for S3 compaction
- `pkg/fs/opslog/local_test.go` -- One regression test for local compaction
- Existing compaction tests updated to include chunks in Put entries

## Key invariant established

> A Put entry without chunks is local tracking state. It must never be
> persisted in S3 snapshots, and remote machines must never treat it as
> a download failure.
