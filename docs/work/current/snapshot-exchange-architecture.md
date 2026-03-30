---
created: 2026-03-29
model: claude-opus-4-6
status: proposed
---

# Snapshot Exchange Architecture

Replace the S3 ops log with per-device snapshot exchange. S3 becomes a blob
store and a snapshot store. Nothing else.

## Motivation

Every major bug in skyfs traces back to the S3 ops log:

- **Compaction deletes ops before all devices read them** — permanent divergence
- **Entries written before blobs exist** — download failures, integrity sweep,
  zero-byte blob hacks
- **Cursor tracking drift** — devices fall behind, miss ops
- **Individual op polling** — slow, fragile, scales poorly with op count

The ops log exists because we modeled sync as operation exchange (CmRDT).
But state exchange (CvRDT) is equivalent and eliminates the entire class of
bugs. Syncthing proves this works: no ops log, just state exchange between
peers. Dropbox validates blob-before-metadata: the commit is the atomic
visibility point.

## Core Principle

**S3 has two things: blobs and snapshots. No ops. No entries. Ever.**

Each device publishes its CRDT state as a snapshot. Other devices download,
merge (LWW), and reconcile against their local filesystem. The diff between
two snapshots tells you everything — adds, modifications, deletes.

## S3 Bucket Layout

**Current layout** (what exists today):

```
s3://bucket/
  sky10.schema                            ← single schema version file
  keys/
    manifest.key.enc
    namespaces/{ns}.ns.enc
    namespaces/{ns}.{deviceID}.ns.enc
  ops/{ts}-{device}-{seq}.enc             ← individual op entries (DYING)
  manifests/
    current.enc                           ← current manifest (DYING)
    snapshot-{ts}.enc                     ← periodic snapshots (DYING)
  blobs/ab/cd/{hash}.enc
  packs/pack_{seq}.enc
  pack-index.enc
  devices/{shortPubkeyID}.json            ← device registry (plaintext)
  invites/{inviteID}/waiting              ← invitation protocol
  invites/{inviteID}/pubkey
  invites/{inviteID}/granted
  debug/{deviceID}/{ts}.json              ← diagnostic dumps
```

**New layout:**

```
s3://bucket/
  keys/
    schema                                      ← key infrastructure version
    namespaces/{ns}.ns.enc
    namespaces/{ns}.{deviceID}.ns.enc

  devices/{shortPubkeyID}.json                  ← device registry (shared)
  invites/{inviteID}/...                        ← invitation protocol (shared)
  debug/{deviceID}/{ts}.json                    ← diagnostic dumps (shared)

  fs/
    schema                                      ← skyfs storage format version (shared across namespaces)
  fs/{nsID}/
    snapshots/{deviceID}/latest.enc             ← current state (sync reads this)
    snapshots/{deviceID}/{timestamp}.enc        ← historical snapshots (recovery)
    blobs/ab/cd/{hash}.enc                      ← file chunks
    packs/pack_{id}.enc                         ← bundled small chunks
    pack-index.enc                              ← chunk-to-pack mapping

  kv/{nsID}/
    schema                                      ← skykv storage format version
    snapshots/{deviceID}/latest.enc             ← current KV state
    snapshots/{deviceID}/{timestamp}.enc        ← historical KV snapshots
    values/{hash}.enc                           ← large KV values (rare)
```

Namespace IDs (`{nsID}`) are opaque identifiers, not human-readable names.
Human-readable names (e.g., "Test", "Node") are only used in logs, labels,
and UI. The mapping from ID to display name lives in namespace key metadata
or local config. This means renaming a drive is free (just change the label),
S3 paths don't leak drive names, and there are no encoding issues.

Everything under `fs/` and `kv/` is scoped per namespace. This gives:

- **Clean deletion** — drop an entire drive by deleting the namespace prefix
- **Per-namespace access control** — already how namespace keys work
- **Selective sync** — devices only sync namespaces they subscribe to
- **Independent retention** — different history policies per namespace
- **Self-contained blob GC** — only scan blobs within the namespace

Cross-namespace blob dedup is lost, but at this scale it's irrelevant.
The isolation is worth far more.

Shared infrastructure (`keys/`, `devices/`, `invites/`, `debug/`) lives at
the root. `devices/` is how a device discovers which snapshots to download:
list devices, then fetch `fs/{nsID}/snapshots/{deviceID}/latest.enc` for each.

## What Dies

| Component | Why |
|-----------|-----|
| S3 ops log (`ops/{ts}-{device}-{seq}.enc`) | Replaced by snapshot exchange |
| Poller reading individual ops | Replaced by snapshot download + merge |
| Compaction | Nothing to compact |
| Cursor tracking (`last_remote_op`) | No cursors needed |
| Integrity sweep | Entries only written after blob upload succeeds |
| `isNotFound` 403 hack | No blobless entries exist |
| `CatchUpFromSnapshot` second pass | Snapshot diff handles deletes naturally |

## What Stays

| Component | Notes |
|-----------|-------|
| Local ops.jsonl | Local source of truth, unchanged |
| Encryption + key hierarchy | Unchanged |
| Chunking, packing, blobs | Unchanged |
| Reconciler | Simpler — snapshot diff instead of ops-driven |
| Watcher | Unchanged |
| Outbox | Simpler — upload blob, write local entry, done |

## What's New

| Component | Purpose |
|-----------|---------|
| Snapshot uploader | Upload local CRDT snapshot to S3 after outbox drains |
| Snapshot poller | Download remote snapshots, merge into local CRDT |
| Per-domain schema files | `fs/schema`, `kv/schema` — evolve independently |

## Upload-Then-Record

**An entry is never written to the local CRDT until its blob exists in S3.**

Current (broken):
1. Watcher fires -> write entry to ops.jsonl -> queue outbox -> upload blob -> write S3 op

New:
1. Watcher fires -> queue outbox entry (with timestamp captured NOW)
2. Outbox worker uploads blob to S3
3. Outbox worker writes entry to local ops.jsonl (using captured timestamp)
4. Snapshot uploader publishes updated snapshot to S3

The timestamp is captured at event time (step 1), not upload completion time.
This preserves correct LWW resolution — modifications are ordered by when they
happened, not by how fast they uploaded.

**Invariant: every entry in the CRDT has a corresponding blob in S3.** No
phantom entries. No integrity sweep. No zero-byte blob problems.

Symlinks and empty directories have no blob — they skip step 2 but still
flow through the outbox for uniformity.

## Convergence Protocol

### Delete Detection: Baseline Diffing (No Tombstones)

Tombstones have an inherent problem: how long do you keep them? Forever
wastes space. Time-bounded risks missing deletes on offline devices.
Per-device tracking adds coordination complexity.

**Solution: no tombstones.** Each device stores the last-downloaded snapshot
for each remote device locally (the "baseline"). On poll, diff the remote
device's latest snapshot against the stored baseline:

- File in baseline but not in latest → **remote deleted it**
- File in latest but not in baseline → **remote added it**
- File in both, different checksum   → **remote modified it**
- File in both, same checksum        → **no change**

The baseline is saved after each successful sync. When a device comes back
online after being offline for any duration, it diffs all remote device
snapshots against its stored baselines and picks up every change — including
deletes that happened weeks ago. No tombstones needed. No retention policy.
No "how long" question.

**Local storage cost:** one snapshot file per remote device. Trivial.

### Startup Sequence

```
1. Load local CRDT from ops.jsonl
2. Seed from disk:
   - Scan local filesystem
   - Diff against LOCAL CRDT (before any remote merge)
   - File on disk, not in CRDT          -> new local file -> queue upload
   - File on disk, different checksum    -> modified       -> queue upload
   - File in CRDT, not on disk          -> local delete   -> write Delete entry
   - File in CRDT, on disk, match       -> in sync        -> skip
3. Merge remote snapshots (baseline diffing):
   - For each subscribed namespace:
     - Download fs/{nsID}/snapshots/{deviceID}/latest.enc for each known device
     - For each remote device:
       - Diff latest against stored baseline
       - Additions/modifications: merge into local CRDT (LWW)
       - Deletions: remove from local CRDT
       - Save latest as new baseline
4. Reconcile:
   - File in merged CRDT, not on disk            -> download blob
   - File removed from CRDT, on disk             -> delete from disk
   - Conflict (both local and remote changed)    -> keep winner, save conflict copy
5. Upload our snapshot to fs/{nsID}/snapshots/{ourDeviceID}/latest.enc
   (also save as fs/{nsID}/snapshots/{ourDeviceID}/{timestamp}.enc for history)
6. Start steady-state loops
```

**The order matters.** Seed BEFORE merge. This is how local deletes are
detected: if a file was in your local CRDT (from a previous sync) and is
now gone from disk, you deleted it. The local CRDT is the merge base.

If you merge remote first, a new file from another device enters the CRDT
before you've checked disk, and you'd mistake it for a local delete.

### Steady State

**Local change (daemon running):**
1. Watcher fires -> compute checksum -> queue outbox entry (timestamp = now)
2. Outbox worker uploads blob -> writes local ops.jsonl entry
3. Snapshot uploader publishes updated snapshot (debounced, only on change)

Snapshot upload is triggered by local state changes, not on a timer. No
changes = no upload = no bandwidth.

**Remote change (polling):**
1. Poll: download remote device snapshots (every N seconds)
2. Diff each against stored baseline -> detect adds, mods, deletes
3. Merge changes into local CRDT (LWW, conflicts produce conflict copies)
4. Reconcile: download new files, delete removed files
5. Save latest snapshots as new baselines
6. Upload our snapshot (only if merged state changed our CRDT)

### Convergence Examples

**New file:**
```
A creates file.txt -> uploads blob -> local entry -> uploads snapshot
B polls -> downloads A's snapshot -> diffs against baseline:
  file.txt is new -> merge into CRDT -> download blob
B saves A's snapshot as new baseline
B uploads own snapshot (now includes file.txt)
-> Converged in one poll cycle.
```

**Delete (daemon running):**
```
A deletes file.txt -> watcher fires -> Delete in local CRDT
A uploads snapshot (file.txt is simply absent)
B polls -> diffs A's latest against baseline:
  file.txt was in baseline, gone from latest -> A deleted it
B removes file.txt from CRDT and disk
B saves A's snapshot as new baseline
B uploads own snapshot (file.txt absent)
-> Converged in one poll cycle.
```

**Delete while daemon off:**
```
A's daemon stops. User deletes file.txt from disk.
A's daemon restarts:
  1. Seed: file.txt in local CRDT but not on disk -> local delete -> remove from CRDT
  2. Merge remote: B's snapshot still has file.txt
     But A's CRDT no longer has it (seed removed it)
     -> LWW: A's delete is at T=now, B's entry is at T=old -> A wins
  3. Upload snapshot (file.txt absent)
B polls -> diffs A's latest against baseline:
  file.txt was in baseline, gone from latest -> delete
-> Converged.
```

**Concurrent edit:**
```
A modifies file.txt at T=10, B modifies at T=12 (neither has polled yet)
A polls B's snapshot -> diff: file.txt changed -> merge: T=12 > T=10 -> B wins
  -> A downloads B's version
B polls A's snapshot -> diff: file.txt changed -> merge: T=12 > T=10 -> B wins
  -> B keeps its version
-> Both converge to B's version. A's orphan blob gets GC'd.
```

**Device offline for a month:**
```
B goes offline for 30 days. A creates, modifies, and deletes many files.
B comes online:
  1. Downloads A's latest snapshot
  2. Diffs against stored baseline (from 30 days ago)
  3. All 30 days of changes are captured in the diff — adds, mods, deletes
  4. Merge, reconcile, update baseline
-> Converged. No missed ops. No tombstone expiry. No special handling.
```

**New device joining:**
```
C starts with empty local CRDT, empty disk, no baselines.
C downloads A and B's latest snapshots.
No baselines exist -> treat entire snapshot as "all new" -> merge all entries.
C reconciles -> downloads all blobs.
C saves A and B's snapshots as initial baselines.
C uploads own snapshot -> now a full participant.
```

## Versioned Snapshots (History)

Snapshots are versioned, not overwritten. Each upload writes two copies:

```
fs/{nsID}/snapshots/{deviceID}/latest.enc       ← sync reads this
fs/{nsID}/snapshots/{deviceID}/{timestamp}.enc  ← history
```

The sync poller only reads `latest.enc`. Historical snapshots are untouched
by sync — they provide point-in-time file tree recovery.

Each historical snapshot maps file paths to blob checksums. To recover a
file from last Tuesday: find the snapshot closest to that time, read the
checksum, pull the blob from `fs/{nsID}/blobs/`. Full version history for free.

## Snapshot Format

The snapshot contains only live files. No tombstones — deletes are detected
by baseline diffing (see above).

```json
{
  "path": "documents/notes.md",
  "checksum": "sha256:abc123...",
  "size": 4096,
  "device": "device-A-id",
  "timestamp": 1711700000,
  "namespace": "default"
}
```

The snapshot is encrypted with the namespace key before upload.

## skykv Parallel

skykv uses the same snapshot-exchange model with different CRDT types:

| CRDT Type | Snapshot State | Merge |
|-----------|---------------|-------|
| LWW Register | `{key, value, timestamp}` | Highest timestamp wins |
| GCounter | `{key, {deviceA: N, deviceB: M}}` | Per-device max, sum for reads |
| PNCounter | Two GCounters (inc + dec) | Same as GCounter per counter |
| OR-Set | `{key, adds + unique tags}` | Union of adds, track removes by tag |

State-based CRDT merge (CvRDT) works for all types. The snapshot captures
the full state needed for correct merge. No operation log needed in S3.

Delete detection for KV uses the same baseline diffing as fs — diff the
remote device's latest KV snapshot against the stored baseline. Key absent
from latest but present in baseline = deleted.

## Conflict Copies

When LWW resolves a concurrent edit, the losing version is preserved as a
conflict copy instead of being silently discarded:

```
documents/notes.md                                    ← winner (higher timestamp)
documents/notes.conflict-{deviceID}-{timestamp}.md   ← loser
```

Conflict detection: during merge, if the same path was modified on both
the local device AND a remote device since the last baseline, that's a
conflict. LWW picks the winner. The loser's blob is already in S3 (both
sides uploaded before recording). The conflict copy is written to disk
and recorded in the local CRDT as a normal file — it syncs to all devices.

Users resolve conflicts manually by keeping one version and deleting the
other. The delete propagates normally.

## Blob GC and History Retention

**Default retention: 30 days, configurable.**

Historical snapshots older than the retention window are deleted. Blobs
not referenced by any remaining snapshot (current or historical, across
all devices) are eligible for deletion.

**GC process:**
1. List all snapshots across all devices in the namespace
2. Delete historical snapshots older than retention window
3. Collect all blob checksums referenced by remaining snapshots
4. List all blobs in `fs/{nsID}/blobs/`
5. Delete blobs not in the reference set

GC runs periodically (e.g., daily) or on demand. Any device can run it.

**Retention is per-namespace** — configurable independently per drive.

## Device Removal

Any device can remove a retired device (lost laptop, decommissioned machine):

1. Delete `devices/{deviceID}.json`
2. Delete `fs/{nsID}/snapshots/{deviceID}/` for all namespaces
3. Delete `kv/{nsID}/snapshots/{deviceID}/` for all namespaces
4. Other devices stop polling for the removed device on next cycle

The removed device's contributions to the CRDT are already reflected in
other devices' snapshots (from previous merges). Removing it just stops
future polling and cleans up S3 storage.

If the removed device comes back online, it would re-register via
`devices/` and upload fresh snapshots. Other devices would treat it as
a new device (no stored baseline) and do a full merge.

## What This Enables

1. **Guaranteed convergence** — snapshot exchange is idempotent and stateless.
   No cursors, no "missed ops," no compaction race conditions.
2. **Upload-then-record** — every CRDT entry has a blob. Downloads never fail
   due to missing data.
3. **Simpler codebase** — remove poller, compaction, cursor tracking, integrity
   sweep, S3 ops log management.
4. **Shared substrate for kv** — same snapshot-exchange model, different CRDT
   types. `pkg/opslog` becomes local-only, shared by both.
5. **Clean S3 layout** — `keys/`, `fs/`, `kv/`. Each domain owns its prefix.

## Migration Path

1. Build snapshot uploader and snapshot poller alongside existing ops log
2. Run both in parallel (dual-write) to validate convergence
3. Once validated, remove S3 ops log, compaction, cursor tracking
4. Restructure S3 bucket layout (`fs/`, `kv/` prefixes)
5. Bump `fs/schema` version

The local ops.jsonl format doesn't change. The daemon startup sequence
changes (seed-then-merge ordering). The outbox worker changes
(upload-then-record). Everything else simplifies or gets deleted.

## Research Validation

| Pattern | Validated By |
|---------|-------------|
| Blob before metadata | Dropbox (commit is atomic visibility point) |
| No ops log in transport | Syncthing (state exchange via Index messages) |
| LWW-Register-Map CRDT | Academic (Shapiro et al. 2011), Syncthing, Cassandra |
| State-based merge (CvRDT) | Academic (equivalent to op-based CmRDT) |
| Baseline diffing for deletes | Derived from Syncthing's index exchange model |
| Per-device state publication | Syncthing (per-device local model) |
