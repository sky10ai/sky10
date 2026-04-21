---
created: 2026-04-14
updated: 2026-04-14
model: gpt-5.4
---

# FS Sync Model And Invariants

This document is the Milestone 0 output for
[`fs-p2p-core-and-agent-drives-plan.md`](./fs-p2p-core-and-agent-drives-plan.md).
It defines the target sync model for `sky10` file sync with and without S3.

## Purpose

`sky10` needs one file-sync engine that:

- converges correctly in private-network P2P mode with no S3 at all
- gets more durable and easier to recover when S3 is enabled
- does not change merge semantics when S3 is turned on
- can support durable synced agent folders as ordinary file content

This document is intentionally about semantics and durable state, not exact
wire encoding or final database schema.

## Non-Goals

This document does not define:

- public or stranger-to-stranger sharing
- final permission UX for shared drives
- a mailbox-based file transfer object model
- final UI design for drives, activity, or agent migration

## Core Model

The file-sync engine has five distinct layers:

1. **Working tree**
   The user-visible drive directory. This is where files become visible and
   editable. It is not the transfer workspace.
2. **Hidden transfer store**
   A per-drive hidden area outside the watched root. It holds staged
   downloads, retained objects or chunks, and resumable transfer sessions.
3. **Durable metadata store**
   The local per-drive database for local file state, tombstones, peer state,
   desired global state, and transfer bookkeeping.
4. **Peers**
   Live sources of metadata and content over libp2p. Peers are first-class in
   the model and are sufficient for correctness.
5. **Optional S3 layer**
   A durable replica and bootstrap source. It improves durability, recovery,
   and fetchability, but does not define the sync semantics.

## Durable State Classes

### Durable Truth

This data must survive restart and is required for correctness:

- local per-path metadata
- local tombstones
- remote per-peer metadata summaries
- durable peer sync cursors or summaries
- durable transfer session state for in-progress remote materialization
- enough local content metadata to verify and publish downloaded data

### Rebuildable Cache

This data is useful but not semantic truth:

- retained objects or chunks in the hidden store
- local scan caches
- source-availability hints
- UI-friendly progress rollups

Loss of cache may slow recovery, but it must not change merge outcomes.

### External Replica

This data exists outside the local node but must not redefine correctness:

- peer-advertised metadata and content
- S3-stored metadata or blobs when S3 is enabled

Peers and S3 are sources. They are not alternate merge-rule engines.

## Required Invariants

The system should be treated as incorrect if any of these are violated.

1. **One semantic model**
   The same file history should resolve the same way with `p2p-only` and
   `p2p+s3`. S3 changes availability, not meaning.
2. **Explicit delete intent**
   Deletes are replicated state. Absence is never enough to prove deletion for
   long-offline peers.
3. **Causality beats clocks**
   Version vectors or equivalent causal metadata decide ordering when possible.
   Timestamps are tie-break hints or conflict-copy naming aids, not authority.
4. **Separate local, peer, and desired state**
   The engine must not collapse remote peer state directly into one mutable
   "current truth" record without retaining provenance.
5. **Persist before publish**
   Remote data is staged, verified, and durably recorded before it becomes
   visible in the working tree.
6. **No partial visibility**
   A user must never observe a partially downloaded remote file in the working
   tree.
7. **Working tree is not transfer state**
   The watcher must not be responsible for observing or repairing our own
   download and transfer mechanics.
8. **Periodic anti-entropy is mandatory**
   Correctness must not depend on watcher reliability, notify delivery, or one
   lucky reconnect.
9. **Conflict preserves user data**
   When concurrent edits cannot be cleanly ordered, the system must preserve
   both user-visible outcomes, usually with a deterministic conflict copy.
10. **Restart safety**
    Restart during upload, download, scan, or publish must lead to resumable or
    self-healing state, not silent divergence.

## Metadata Direction

FS should adopt the same reliability posture proven in KV hardening:

- use causal direction before wall-clock ordering
- replicate tombstones explicitly
- do summary-first anti-entropy instead of blind full replay
- run bounded periodic healing even with no new writes
- make health loud when local and remote state drift

FS is larger than KV, but it should not relearn the same failure modes.

## Backend Layering Direction

FS should adopt the same layering lessons proven by the mailbox work:

- keep one product model above multiple backends
- persist first, then deliver or publish
- separate control state from payload bytes
- keep the fast path and durable fallback path distinct
- expose lifecycle and stuck-state progress clearly

FS should reuse these layering rules without turning files into mailbox items.

## Per-Path State Model

Each logical path should have enough metadata to answer four questions:

1. What does the local device currently believe exists at this path?
2. What does each known peer claim exists at this path?
3. What version should the local node try to materialize next?
4. Which sources can currently provide the required content?

At a minimum, the durable metadata for a path should cover:

- path and kind: file, directory, tombstone, or symlink if supported
- content identity: checksum, content ID, or chunk/object references
- causal version metadata
- last publishing device
- modified-time hint for UX and tie-breaking only
- tombstone status and tombstone version
- conflict marker if this logical path currently requires conflict-copy logic
- source availability hints for local cache, peers, and S3

## State Separation

The engine should maintain three distinct views:

### Local State

What this node has published or materialized locally.

### Remote-Per-Peer State

What each peer last advertised. This must remain attributable to that peer so
the engine can reason about stale peers, reconnect deltas, and multi-source
availability.

### Desired State

The derived target view after comparing local and remote state according to the
merge rules. The reconciler and pull planner act against this view.

## Sync Semantics

### Local Change

The target flow for a local write is:

1. Detect local change by watcher or periodic scan.
2. Wait until the file is stable enough to process.
3. Hash or chunk the file and prepare local object references.
4. Persist local metadata and any required local object state.
5. Publish metadata to peers and, if enabled, to S3.
6. Mark transfer or publication health in the local metadata store.

Key rule: local metadata should not claim a file is published until the
required local transfer state exists to satisfy downstream fetches or retries.

### Remote Peer Change

The target flow for remote peer materialization is:

1. Receive peer metadata through full sync, summary exchange, or delta sync.
2. Update remote-per-peer state durably.
3. Recompute desired state for affected paths.
4. Plan the best source for missing data: local cache, existing local file
   reuse, live peer, or S3 if present.
5. Download or assemble into the hidden transfer store.
6. Verify content before promotion.
7. Publish into the working tree by atomic rename or equivalent.
8. Mark local state as materialized.

### Remote S3 Recovery

The target flow for S3-backed recovery is the same as peer recovery except the
chosen source is S3. The same metadata rules and publish barrier apply.

## Anti-Entropy Rules

The system should always have two healing paths:

- event-driven healing from watcher events, peer notifications, and reconnect
  summaries
- periodic bounded healing from scan and anti-entropy loops

Neither path is trusted alone. Convergence depends on both.

## Conflict Rules

The initial model should use deterministic conflict behavior:

- if one version causally dominates another, the dominated version loses
  without creating a conflict copy
- if versions are concurrent, preserve one at the original path and materialize
  the other as a conflict copy
- modify/delete concurrency should preserve the modified bytes and the delete
  intent, not silently drop one side
- timestamps and device IDs may help name conflict copies, but should not be
  the core merge authority

The winning path policy can stay simple at first if it is deterministic and
does not discard data.

## Transfer Workspace Rules

The hidden transfer store should exist per drive and stay outside the watched
tree.

The minimum conceptual layout is:

```text
<drive-data>/
  objects/
  staging/
  sessions/
  metadata/
```

Rules:

- `staging/` holds in-progress downloads and publish candidates
- `objects/` holds retained verified content or chunks
- `sessions/` holds resumable transfer state
- watcher logic must ignore the entire hidden store
- cleanup and garbage collection may reclaim cached objects, but not durable
  metadata needed for correctness

## Agents Drive As First-Class Content

The `Agents` drive is an ordinary synced drive that happens to hold durable
agent personality and runtime replication files.

Initial target layout:

```text
Agents/
  <agent-name-or-id>/
    SOUL.md
    MEMORY.md
    sky10.md
    notes/
    attachments/
```

This content should obey the same FS rules as any other drive:

- no special RPC-only hidden state pretending to be a file
- no agent-only sync engine
- same conflict, staging, publish, and recovery rules as ordinary files

## Agent File Contract

### `SOUL.md`

Primary purpose:

- durable identity, operating principles, and long-lived intent

Expected ownership:

- mostly human-authored
- optionally appended or refined by the agent runtime with explicit user intent

### `MEMORY.md`

Primary purpose:

- durable working memory, notes, and portable context worth carrying across
  machines

Expected ownership:

- mostly agent-authored, with humans allowed to curate or trim

### `sky10.md`

Primary purpose:

- replication contract for recreating the agent on another machine with nearly
  the same runtime behavior and setup assumptions

Expected ownership:

- mixed ownership
- some fields human-authored
- some fields runtime-authored
- some fields daemon-authored

Initial shape should be markdown with machine-readable front matter so humans
can read and edit it without losing structure.

Example shape:

```md
---
schema: sky10-agent/v1
agent_id: A-example123
display_name: Hermes Coder
owner_device_id: D-example123
runtime:
  family: claude_code
  version: 1.0.0
model:
  provider: anthropic
  name: claude-sonnet
  settings:
    reasoning: high
bootstrap:
  repo: sky10
  working_dir: ~/src/sky10
tools:
  required:
    - shell
    - git
connectors:
  required: []
field_ownership:
  human:
    - display_name
    - bootstrap
  runtime:
    - model
  daemon:
    - owner_device_id
---

# sky10 Agent Record

## Notes

Human-readable migration and setup notes go here.
```

Initial required `sky10.md` fields:

- stable agent ID
- display name
- owning device ID
- runtime family
- runtime version if known
- model provider and model name
- important setup or bootstrap assumptions
- required tools or connectors
- explicit field ownership

Unknown fields should be preserved during daemon or runtime updates.

## Current Code Areas Likely To Change

This model will likely reshape the current code in:

- [`../../../pkg/fs/daemon_v2_5.go`](../../../pkg/fs/daemon_v2_5.go)
- [`../../../pkg/fs/reconciler.go`](../../../pkg/fs/reconciler.go)
- [`../../../pkg/fs/watcher_handler.go`](../../../pkg/fs/watcher_handler.go)
- [`../../../pkg/fs/snapshot_poller.go`](../../../pkg/fs/snapshot_poller.go)
- [`../../../pkg/fs/opslog/opslog.go`](../../../pkg/fs/opslog/opslog.go)
- [`../../../pkg/fs/outbox_worker.go`](../../../pkg/fs/outbox_worker.go)
- [`../../../pkg/fs/rpc_http.go`](../../../pkg/fs/rpc_http.go)
- [`../../../pkg/kv/p2p.go`](../../../pkg/kv/p2p.go)

The current implementation still carries baseline-diff and timestamp-first
assumptions that this model intentionally moves away from.

## Open Questions For Milestone 1+

- Should the hidden transfer store retain fully assembled files, chunks, or
  both?
- How much causal metadata is enough for v1 without overcomplicating the local
  DB?
- Should `Agents` be auto-provisioned when the first local agent is created, or
  stay user-created but strongly suggested?
- Which `sky10.md` fields should the daemon be allowed to write automatically?
- What is the minimum Windows-safe path and symlink policy for agent folders?

## Acceptance For This Milestone

Milestone 0 is successful when:

- we can explain the same merge outcome with and without S3
- we have explicit delete and conflict rules that do not depend on absence or
  wall-clock ordering alone
- we have a clear publish barrier between hidden transfer state and the working
  tree
- we can describe how an `Agents/<id>/sky10.md` record helps recreate an agent
  on another machine
