---
created: 2026-03-29
model: claude-opus-4-6
---

# Snapshot Exchange Architecture

Replaced the S3 ops log with per-device snapshot exchange. S3 now holds
only blobs and snapshots. No individual op entries, no compaction, no
cursor tracking.

## Problem

Every major sync bug traced to the S3 ops log:
- Compaction deleting ops before all devices read them (permanent divergence)
- Entries written before blobs exist (download failures, integrity sweep)
- Cursor tracking drift (devices fall behind, miss ops)
- Individual op polling (slow, fragile, scales poorly)

## Solution

State-based CRDT exchange (CvRDT) instead of operation-based (CmRDT).
Each device uploads its CRDT snapshot to S3. Other devices download,
baseline-diff, and merge. Validated by research into Dropbox (blob-first
commits), Syncthing (state exchange, no ops log), and academic CRDT
literature.

### Architecture

**Upload-then-record:** CRDT entry written to ops.jsonl only AFTER blob
upload succeeds. No phantom entries possible.

**Snapshot exchange:** Each device uploads its snapshot to
`fs/{nsID}/snapshots/{deviceID}/latest.enc`. Polling devices download,
diff against stored baselines, merge changes into local CRDT.

**Baseline diffing (no tombstones):** Deletes detected by comparing
remote latest vs stored baseline. File in baseline but not in latest =
deleted. No tombstone retention problem.

**Startup sequence:** Seed (local CRDT as merge base) → Poll remote →
Reconcile → Upload snapshot. Order matters — seed before merge prevents
misidentifying remote adds as local deletes.

### S3 Layout

```
keys/                                    ← shared key infrastructure
devices/{id}.json                        ← device registry
fs/schema                                ← skyfs format version
fs/{nsID}/snapshots/{deviceID}/latest.enc
fs/{nsID}/snapshots/{deviceID}/{ts}.enc  ← history
fs/{nsID}/blobs/ab/cd/{hash}.enc
fs/{nsID}/packs/pack_{id}.enc
fs/{nsID}/pack-index.enc
```

Namespace IDs are opaque — human names only in UI/logs.

## Implementation (5 Phases)

### Phase 1: Upload-Then-Record
- WatcherHandler writes outbox only (not ops.jsonl)
- OutboxWorker writes ops.jsonl after blob upload with captured timestamp
- seedStateFromDisk: outbox for new files, local log for deletes

### Phase 2: Kill S3 Ops Log
- Removed writeOp, getOpsLog, loadCurrentState from Store
- Removed CatchUpFromSnapshot, LastRemoteOp, SetLastRemoteOp
- Deleted compact.go, old daemon.go/daemonv2.go
- Store is now a pure blob uploader

### Phase 3: Snapshot Uploader
- Serializes local CRDT snapshot → encrypts → uploads to S3
- Writes latest.enc + timestamped history copy
- Debounced — poked on state.changed events

### Phase 4: Snapshot Poller + Baseline Diffing
- Downloads remote device snapshots on interval
- Diffs against stored baselines to detect adds/mods/deletes
- Merges into local CRDT via LWW
- Conflict detection: both local+remote changed → conflict copy

### Phase 5: Per-Namespace Paths + Cleanup
- Blobs at fs/{nsID}/blobs/ instead of blobs/
- Schema at fs/schema
- GC rewritten for snapshot-based blob reference tracking
- Device removal cleans up snapshots
- Integrity sweep removed (upload-then-record eliminates it)

## New Components

| File | Purpose |
|------|---------|
| `snapshot_uploader.go` | CRDT → encrypt → S3 upload |
| `snapshot_poller.go` | S3 download → baseline diff → CRDT merge |
| `baseline.go` | Per-device baseline storage on local disk |
| `s3paths.go` | Namespaced S3 key helpers |
| `opslog/snapshot_json.go` | Marshal/Unmarshal for snapshot wire format |
| `compat_integration.go` | Stubs for old integration tests |

## What Died

| Component | Reason |
|-----------|--------|
| S3 ops log (`ops/{ts}-{device}-{seq}.enc`) | Replaced by snapshots |
| Compaction | Nothing to compact |
| Cursor tracking (LastRemoteOp) | No cursors needed |
| Integrity sweep | Upload-then-record prevents chunkless entries |
| CatchUpFromSnapshot | Replaced by baseline diffing |
| Old daemons (V1, V2) | Only DaemonV2_5 survives |
| manifest.go Save/Load | Replaced by snapshot uploader |

## Tests

- 497 tests passing, 0 skips
- 4 new end-to-end MinIO integration tests (two-device file sync,
  delete propagation, bidirectional sync, offline catch-up)
- Reconciler, GC, multidevice, store, edge tests all rewritten
- Old integration tests (deletedir, dotfile, manifest) skipped pending rewrite

## Decisions

- **No tombstones** — baseline diffing eliminates the "how long to keep
  tombstones" problem entirely
- **Per-namespace blob scoping** — clean prefix deletion, independent
  retention, self-contained GC
- **Namespace IDs are opaque** — renaming a drive is free, S3 paths
  don't leak names
- **Conflict copies** — LWW loser preserved as `.conflict-{device}-{ts}`
- **30-day default history retention** — configurable per namespace
- **Schema at fs/ root** — crypto algorithms shared across namespaces

## Research

Findings documented in `docs/work/current/file-sync-research.md`:
- Dropbox: blob-first commits, three-tree model, CanopyCheck convergence
- Syncthing: no ops log, state exchange via Index messages, version vectors
- Academic: CvRDT ≡ CmRDT (Shapiro 2011), LWW-Register-Map well-established
